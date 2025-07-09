#!/bin/bash
set -e

# Source common utilities
THIS_SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$THIS_SCRIPT_DIR/../utils/logging.sh"

# Configuration
KIND_CONFIG_DIR="$THIS_SCRIPT_DIR/kind"
CLUSTERS=("alice" "bob" "charlie")

# Function to create a single cluster
create_cluster() {
    local cluster_name="$1"
    local config_file="$KIND_CONFIG_DIR/${cluster_name}-cluster.yaml"

    if kind get clusters | grep -q "^${cluster_name}-cluster$"; then
        log_warn "Cluster ${cluster_name}-cluster already exists, skipping creation"
        return 0
    fi

    log_info "Creating Kind cluster: ${cluster_name}-cluster"
    if ! kind create cluster --quiet --config="$config_file"; then
        log_error "Failed to create cluster: ${cluster_name}-cluster"
        return 1
    fi

    # Get kubeconfig for the cluster
    log_info "Exporting kubeconfig for ${cluster_name}-cluster"
    kind export kubeconfig --name="${cluster_name}-cluster" --kubeconfig="$HOME/.kube/config-${cluster_name}"

    log_info "Successfully created cluster: ${cluster_name}-cluster"
}

# Main execution
main() {
    log_info "Creating 3 Kind clusters for multi-cluster testnet"

    # Check if kind is installed
    if ! command -v kind &> /dev/null; then
        log_error "kind is not installed. Please install kind first."
        exit 1
    fi

    # Ensure kind network exists with correct subnet
    "$THIS_SCRIPT_DIR/create-kind-network.sh"

    # Create clusters in parallel
    local pids=()
    for cluster in "${CLUSTERS[@]}"; do
        create_cluster "$cluster" &
        pids+=($!)
    done

    # Wait for all clusters to be created and check for failures
    local failed=0
    for pid in "${pids[@]}"; do
        if ! wait "$pid"; then
            failed=1
        fi
    done

    if [ $failed -eq 1 ]; then
        log_error "One or more cluster creations failed"
        exit 1
    fi

    log_info "All clusters created successfully"

    # Create a merged kubeconfig
    log_info "Creating merged kubeconfig"
    KUBECONFIG=""
    for cluster in "${CLUSTERS[@]}"; do
        KUBECONFIG="$KUBECONFIG:$HOME/.kube/config-${cluster}"
    done
    KUBECONFIG="${KUBECONFIG:1}" # Remove leading colon
    export KUBECONFIG
    kubectl config view --flatten > "$HOME/.kube/config-multicluster"

    log_info "Merged kubeconfig saved to $HOME/.kube/config-multicluster"
    log_info "To use: export KUBECONFIG=$HOME/.kube/config-multicluster"
}

# Run main function
main "$@"
