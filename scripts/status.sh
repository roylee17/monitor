#!/bin/bash
# Display comprehensive status of the deployment

set -e

# Source common functions
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common/logging.sh"
source "${SCRIPT_DIR}/lifecycle/consumer-utils.sh"

# Command line options
SHOW_PROVIDER=true
SHOW_CONSUMERS=true
VERBOSE=false
OUTPUT_JSON=false
NAMESPACE_OVERRIDE=""

# ============================================
# Functions
# ============================================

usage() {
    cat << EOF
Usage: $0 [OPTIONS]

Display comprehensive status of the ICS deployment.

Options:
  -p, --provider       Show only provider status
  -c, --consumers      Show only consumer chains status
  -v, --verbose        Show detailed information
  -j, --json           Output in JSON format
  -n, --namespace NS   Override provider namespace (default: $DEFAULT_NAMESPACE)
  -h, --help           Show this help message

Examples:
  $0                   # Show full status
  $0 -p                # Show only provider status
  $0 -c -v             # Show detailed consumer chain status
  $0 -j                # Output status as JSON
EOF
}

parse_arguments() {
    while [[ $# -gt 0 ]]; do
        case $1 in
            -p|--provider)
                SHOW_PROVIDER=true
                SHOW_CONSUMERS=false
                shift
                ;;
            -c|--consumers)
                SHOW_PROVIDER=false
                SHOW_CONSUMERS=true
                shift
                ;;
            -v|--verbose)
                VERBOSE=true
                shift
                ;;
            -j|--json)
                OUTPUT_JSON=true
                shift
                ;;
            -n|--namespace)
                NAMESPACE_OVERRIDE="$2"
                shift 2
                ;;
            -h|--help)
                usage
                exit 0
                ;;
            *)
                log_error "Unknown option: $1"
                usage
                exit 1
                ;;
        esac
    done
    
    # Apply namespace override if provided
    if [ -n "$NAMESPACE_OVERRIDE" ]; then
        DEFAULT_NAMESPACE="$NAMESPACE_OVERRIDE"
    fi
}

# ============================================
# Provider Status Functions
# ============================================

get_provider_pods() {
    local namespace="$1"
    kubectl -n "$namespace" get pods -o json 2>/dev/null || echo '{}'
}

get_validator_status() {
    local pod="$1"
    local namespace="$2"
    
    # Get validators
    local validators
    validators=$(kubectl -n "$namespace" exec "pod/$pod" -c validator -- \
        curl -s http://localhost:26657/validators 2>/dev/null || echo '{}')
    
    # Get sync status
    local status
    status=$(kubectl -n "$namespace" exec "pod/$pod" -c validator -- \
        curl -s http://localhost:26657/status 2>/dev/null || echo '{}')
    
    echo "{\"validators\": $validators, \"status\": $status}"
}

display_provider_status() {
    local namespace="$1"
    local verbose="$2"
    
    print_header "Provider Status"
    print_kv "Namespace" "$namespace"
    
    # Get pods
    local pods_json pod_count
    pods_json=$(get_provider_pods "$namespace")
    pod_count=$(echo "$pods_json" | jq '.items | length' 2>/dev/null || echo "0")
    
    if [ "$pod_count" -eq "0" ]; then
        log_error "No pods found in namespace $namespace"
        return 1
    fi
    
    # Provider pods
    print_subheader "Provider Pods"
    echo "$pods_json" | jq -r '.items[] | 
        select(.metadata.labels.role == "validator") | 
        "\(.metadata.name)\t\(.status.phase)\t\(.status.podIP // "N/A")"' | \
        while IFS=$'\t' read -r name phase ip; do
            local color="${COLOR_GREEN}"
            [ "$phase" != "Running" ] && color="${COLOR_RED}"
            printf "  %-30s ${color}%-10s${COLOR_RESET} %s\n" "$name" "$phase" "$ip"
        done
    
    # Monitor pods
    print_subheader "Monitor Pods"
    echo "$pods_json" | jq -r '.items[] | 
        select(.metadata.labels.app == "monitor") | 
        "\(.metadata.name)\t\(.status.phase)\t\(.status.podIP // "N/A")"' | \
        while IFS=$'\t' read -r name phase ip; do
            local color="${COLOR_GREEN}"
            [ "$phase" != "Running" ] && color="${COLOR_RED}"
            printf "  %-30s ${color}%-10s${COLOR_RESET} %s\n" "$name" "$phase" "$ip"
        done
    
    # Get a running validator pod
    local validator_pod
    validator_pod=$(get_any_validator_pod "$namespace")
    if [ -z "$validator_pod" ]; then
        log_warn "No running validator pods found"
        return 0
    fi
    
    # Validator and blockchain status
    local val_status
    val_status=$(get_validator_status "$validator_pod" "$namespace")
    
    print_subheader "Validators"
    echo "$val_status" | jq -r '.validators.result.validators[]? // empty | 
        "  Address: \(.address[0:16])... Power: \(.voting_power)"'
    
    print_subheader "Blockchain Status"
    echo "$val_status" | jq -r '.status.result.sync_info // {} | 
        "  Height: \(.latest_block_height // "N/A")
  Time:   \(.latest_block_time // "N/A")
  Hash:   \(.latest_block_hash[0:16] // "N/A")..."'
    
    if [ "$verbose" = "true" ]; then
        print_subheader "Network Info"
        echo "$val_status" | jq -r '.status.result.node_info // {} |
            "  Network: \(.network // "N/A")
  Version: \(.version // "N/A")
  Moniker: \(.moniker // "N/A")"'
    fi
}

# ============================================
# Consumer Status Functions
# ============================================

display_consumer_status() {
    local namespace="$1"
    local verbose="$2"
    
    print_header "Consumer Chains Status"
    
    # Get validator pod
    local pod
    pod=$(get_any_validator_pod "$namespace")
    if [ -z "$pod" ]; then
        log_error "No validator pod available to query consumer chains"
        return 1
    fi
    
    # Get consumer chains
    local chains chain_count
    chains=$(query_consumer_chains "$pod" "$namespace")
    chain_count=$(echo "$chains" | jq '.chains | length' 2>/dev/null || echo "0")
    
    log_info "Total consumer chains: $chain_count"
    
    if [ "$chain_count" -gt "0" ]; then
        print_subheader "Consumer Chains"
        
        if [ "$verbose" = "true" ]; then
            # Detailed view
            echo "$chains" | jq -c '.chains[]' | while IFS= read -r chain; do
                display_consumer_details "$chain" "$pod" "$namespace"
                echo ""
            done
        else
            # Summary view
            printf "  %-4s %-30s %-25s\n" "ID" "Chain ID" "Phase"
            printf "  %-4s %-30s %-25s\n" "----" "------------------------------" "-------------------------"
            echo "$chains" | jq -c '.chains[]' | while IFS= read -r chain; do
                display_consumer_summary "$chain"
            done
        fi
    fi
    
    # Consumer namespace pods
    print_subheader "Consumer Chain Pods"
    local consumer_pods consumer_pod_count
    consumer_pods=$(kubectl -n "$CONSUMER_NAMESPACE" get pods -o json 2>/dev/null || echo '{}')
    consumer_pod_count=$(echo "$consumer_pods" | jq '.items | length' 2>/dev/null || echo "0")
    
    if [ "$consumer_pod_count" -eq "0" ]; then
        log_info "No consumer chain pods running"
    else
        echo "$consumer_pods" | jq -r '.items[] | 
            "\(.metadata.name)\t\(.status.phase)\t\(.metadata.labels.chain // "N/A")"' | \
            while IFS=$'\t' read -r name phase chain; do
                local color="${COLOR_GREEN}"
                [ "$phase" != "Running" ] && color="${COLOR_RED}"
                printf "  %-40s ${color}%-10s${COLOR_RESET} %s\n" "$name" "$phase" "$chain"
            done
    fi
}

# ============================================
# JSON Output Functions
# ============================================

output_json_status() {
    local namespace="$1"
    
    # Get provider pods
    local pods
    pods=$(get_provider_pods "$namespace")
    
    # Get validator pod for queries
    local validator_pod
    validator_pod=$(get_any_validator_pod "$namespace")
    
    # Initialize JSON object
    local json_output="{}"
    
    # Add provider info
    json_output=$(echo "$json_output" | jq --arg ns "$namespace" '. + {
        "provider": {
            "namespace": $ns,
            "pods": []
        }
    }')
    
    # Add pods
    if [ -n "$pods" ] && [ "$pods" != "{}" ]; then
        json_output=$(echo "$json_output" | jq --argjson pods "$pods" '
            .provider.pods = ($pods.items // [] | map({
                name: .metadata.name,
                phase: .status.phase,
                ip: .status.podIP,
                role: .metadata.labels.role,
                app: .metadata.labels.app
            }))')
    fi
    
    # Add validator and blockchain status if available
    if [ -n "$validator_pod" ]; then
        local val_status
        val_status=$(get_validator_status "$validator_pod" "$namespace")
        json_output=$(echo "$json_output" | jq --argjson status "$val_status" '
            .provider.validators = ($status.validators.result.validators // []) |
            .provider.blockchain = $status.status.result.sync_info')
        
        # Add consumer chains
        local chains
        chains=$(query_consumer_chains "$validator_pod" "$namespace")
        json_output=$(echo "$json_output" | jq --argjson chains "$chains" '
            .consumer_chains = ($chains.chains // [])')
    fi
    
    # Add consumer namespace pods
    local consumer_pods
    consumer_pods=$(kubectl -n "$CONSUMER_NAMESPACE" get pods -o json 2>/dev/null || echo '{}')
    json_output=$(echo "$json_output" | jq --argjson pods "$consumer_pods" --arg ns "$CONSUMER_NAMESPACE" '
        .consumer_namespace = {
            "namespace": $ns,
            "pods": ($pods.items // [] | map({
                name: .metadata.name,
                phase: .status.phase,
                chain: .metadata.labels.chain
            }))
        }')
    
    echo "$json_output" | jq '.'
}

# ============================================
# Main
# ============================================

main() {
    parse_arguments "$@"
    
    if [ "$OUTPUT_JSON" = "true" ]; then
        output_json_status "$DEFAULT_NAMESPACE"
        exit 0
    fi
    
    # Show both sections by default
    if [ "$SHOW_PROVIDER" = "true" ] && [ "$SHOW_CONSUMERS" = "true" ]; then
        print_header "Deployment Status"
        print_kv "Provider Namespace" "$DEFAULT_NAMESPACE"
        print_kv "Consumer Namespace" "$CONSUMER_NAMESPACE"
        echo ""
    fi
    
    if [ "$SHOW_PROVIDER" = "true" ]; then
        display_provider_status "$DEFAULT_NAMESPACE" "$VERBOSE"
        [ "$SHOW_CONSUMERS" = "true" ] && echo ""
    fi
    
    if [ "$SHOW_CONSUMERS" = "true" ]; then
        display_consumer_status "$DEFAULT_NAMESPACE" "$VERBOSE"
    fi
}

# Run main
main "$@"