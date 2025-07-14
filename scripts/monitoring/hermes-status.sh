#!/bin/bash
set -e

# Source common functions
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/../utils/logging.sh"

# Help function
usage() {
    cat << EOF
Usage: $0 [OPTIONS]

Show Hermes relayer status for consumer chains

OPTIONS:
    -c, --chain CHAIN_ID     Show status for specific chain (optional)
    -n, --namespace NS       Namespace to check (optional, auto-detected)
    -v, --verbose           Show detailed information
    -h, --help              Show this help message

EXAMPLES:
    $0                      # Show status for all consumer chains
    $0 -c testchain-0       # Show status for specific chain
    $0 -c testchain-0 -v    # Show detailed status with logs

EOF
    exit 1
}

# Default values
CHAIN_ID=""
NAMESPACE=""
VERBOSE=false

# Container name for Hermes pods (standardized in deployment)
HERMES_CONTAINER="hermes"

# Check required tools
check_requirements() {
    if ! command -v jq &> /dev/null; then
        log_error "jq is required but not installed"
        exit 1
    fi

    if ! command -v kubectl &> /dev/null; then
        log_error "kubectl is required but not installed"
        exit 1
    fi
}

# Get all cluster contexts (not just Kind)
get_cluster_contexts() {
    # First try to get Kind clusters
    local kind_clusters=$(kubectl config get-contexts -o name 2>/dev/null | grep "^kind-.*-cluster$" || true)
    
    # If no Kind clusters found, get all contexts
    if [[ -z "${kind_clusters}" ]]; then
        kubectl config get-contexts -o name 2>/dev/null || true
    else
        echo "${kind_clusters}"
    fi
}

# Find which context contains a namespace
find_namespace_context() {
    local namespace=$1
    local contexts

    # Get contexts once to avoid repeated calls
    contexts=$(get_cluster_contexts)

    if [[ -z "${contexts}" ]]; then
        return 1
    fi

    while IFS= read -r context; do
        if kubectl --context "${context}" get namespace "${namespace}" &>/dev/null; then
            echo "${context}"
            return 0
        fi
    done <<< "${contexts}"

    return 1
}

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        -c|--chain)
            CHAIN_ID="$2"
            shift 2
            ;;
        -n|--namespace)
            NAMESPACE="$2"
            shift 2
            ;;
        -v|--verbose)
            VERBOSE=true
            shift
            ;;
        -h|--help)
            usage
            ;;
        *)
            log_error "Unknown option: $1"
            usage
            ;;
    esac
done

# Function to get Hermes status for a specific chain
get_hermes_status() {
    local chain_id=$1
    local namespace=$2

    log_info "Checking Hermes status for chain: ${chain_id}"

    # Find which context contains this namespace
    local context
    if ! context=$(find_namespace_context "${namespace}"); then
        log_warn "Could not find namespace ${namespace} in any cluster"
        return
    fi

    # Find Hermes deployment for this chain
    # Try specific pattern first, then use label selector
    local deployment_name="${chain_id}-hermes"
    local deployment_exists
    
    # Check if deployment exists with expected name
    if ! kubectl --context "${context}" get deployment "${deployment_name}" -n "${namespace}" &>/dev/null; then
        # Fall back to finding by labels
        deployment_name=$(kubectl --context "${context}" get deployments -n "${namespace}" \
            -l "app=hermes,chain-id=${chain_id}" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || echo "")
        
        if [[ -z "${deployment_name}" ]]; then
            log_warn "Hermes not deployed for chain ${chain_id}"
            return
        fi
    fi

    # Get deployment status with proper error handling
    local deployment_status
    if ! deployment_status=$(kubectl --context "${context}" get deployment "${deployment_name}" -n "${namespace}" -o json 2>/dev/null); then
        log_warn "Hermes not deployed for chain ${chain_id}"
        return
    fi

    # Extract deployment info atomically
    local replicas ready_replicas
    replicas=$(echo "${deployment_status}" | jq -r '.spec.replicas // 0')
    ready_replicas=$(echo "${deployment_status}" | jq -r '.status.readyReplicas // 0')

    log_info "Deployment: ${deployment_name}"
    log_info "Replicas: ${ready_replicas}/${replicas}"

    # Get pod status with label selector
    local pods pod_count
    if ! pods=$(kubectl --context "${context}" get pods -n "${namespace}" -l "app=hermes,chain-id=${chain_id}" -o json 2>/dev/null); then
        log_warn "Failed to get pod information"
        return
    fi

    pod_count=$(echo "${pods}" | jq -r '.items | length')

    if [[ ${pod_count} -eq 0 ]]; then
        log_warn "No Hermes pods found"
        return
    fi

    # Get first pod details
    local pod_name pod_phase pod_ready
    pod_name=$(echo "${pods}" | jq -r '.items[0].metadata.name')
    pod_phase=$(echo "${pods}" | jq -r '.items[0].status.phase')
    pod_ready=$(echo "${pods}" | jq -r '.items[0].status.conditions[] | select(.type=="Ready") | .status')

    log_info "Pod: ${pod_name}"
    log_info "Phase: ${pod_phase}"
    log_info "Ready: ${pod_ready}"

    # Check for CCV channel by querying Hermes directly
    local channel_output channel_exit_code
    channel_output=$(kubectl --context "${context}" exec "${pod_name}" -n "${namespace}" -c "${HERMES_CONTAINER}" -- \
        hermes query channels --chain "${chain_id}" 2>&1) && channel_exit_code=0 || channel_exit_code=$?

    if [[ ${channel_exit_code} -ne 0 ]]; then
        log_warn "CCV Channel: Failed to query (exit code: ${channel_exit_code})"
    else
        # Check for CCV channel in output (look for consumer port)
        # The output format has PortChannelId with channel_id and port_id on separate lines
        if echo "${channel_output}" | grep -q '"consumer"'; then
            # Extract the channel ID associated with consumer port
            local channel_id
            channel_id=$(echo "${channel_output}" | grep -B3 '"consumer"' | grep '"channel-' | grep -oE 'channel-[0-9]+' | head -1)
            log_info "CCV Channel: Created ✓ (consumer/${channel_id:-channel-?})"
        else
            log_warn "CCV Channel: Not found"
        fi
    fi

    # Show recent logs if verbose
    if [[ "${VERBOSE}" == "true" ]]; then
        echo ""
        log_info "Recent logs:"
        kubectl --context "${context}" logs "${pod_name}" -n "${namespace}" -c "${HERMES_CONTAINER}" --tail=20 2>/dev/null || log_error "Failed to get logs"
    fi

    echo ""
}

# Main logic
check_requirements

if [[ -n "${CHAIN_ID}" ]]; then
    # Specific chain requested
    if [[ -z "${NAMESPACE}" ]]; then
        # Try to auto-detect namespace
        # Look for namespaces containing the chain ID
        found_namespace=""
        for context in $(get_cluster_contexts); do
            found_namespace=$(kubectl --context "${context}" get namespaces -o json 2>/dev/null | \
                jq -r --arg chain "${CHAIN_ID}" '.items[] | select(.metadata.name | contains($chain)) | .metadata.name' | head -1)

            if [[ -n "${found_namespace}" ]]; then
                NAMESPACE="${found_namespace}"
                break
            fi
        done

        if [[ -z "${NAMESPACE}" ]]; then
            log_error "Could not auto-detect namespace for chain ${CHAIN_ID}"
            log_info "Please specify namespace with -n option"
            exit 1
        fi
    fi

    get_hermes_status "${CHAIN_ID}" "${NAMESPACE}"
else
    # Show status for all consumer chains
    log_info "Searching for all Hermes deployments..."

    # Find all namespaces with Hermes deployments across all clusters
    hermes_deployments=""
    for context in $(get_cluster_contexts); do
        # Find deployments with app=hermes label
        cluster_deployments=$(kubectl --context "${context}" get deployments --all-namespaces -l app=hermes -o json 2>/dev/null | \
            jq -r '.items[] | "\(.metadata.namespace) \(.metadata.name) \(.metadata.labels["chain-id"] // "unknown")"' || true)

        if [[ -n "${cluster_deployments}" ]]; then
            hermes_deployments="${hermes_deployments}${cluster_deployments}"$'\n'
        fi
    done

    # Remove empty lines and duplicates
    hermes_deployments=$(echo -n "${hermes_deployments}" | grep -v '^$' | sort -u || true)

    if [[ -z "${hermes_deployments}" ]]; then
        log_warn "No Hermes deployments found in any cluster"
        exit 0
    fi

    # Process each deployment
    while IFS=' ' read -r namespace deployment chain_id_label; do
        [[ -z "${namespace}" ]] && continue

        # Use chain ID from label if available, otherwise try to extract from name
        chain_id=""
        if [[ "${chain_id_label}" != "unknown" ]]; then
            chain_id="${chain_id_label}"
        else
            # Fall back to extracting from deployment name
            chain_id=${deployment%-hermes}
        fi
        
        get_hermes_status "${chain_id}" "${namespace}"
    done <<< "${hermes_deployments}"
fi

log_info "Hermes status check complete"
