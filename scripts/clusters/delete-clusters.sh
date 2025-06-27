#!/bin/bash
set -e

# Source common utilities
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/../common/logging.sh"

# Configuration
CLUSTERS=("alice" "bob" "charlie")

# Function to delete a single cluster
delete_cluster() {
    local cluster_name="$1"
    
    if ! kind get clusters 2>/dev/null | grep -q "^${cluster_name}-cluster$"; then
        log_warn "Cluster ${cluster_name}-cluster does not exist, skipping deletion"
        return 0
    fi
    
    log_info "Deleting Kind cluster: ${cluster_name}-cluster"
    kind delete cluster --name="${cluster_name}-cluster"
    
    # Remove individual kubeconfig
    rm -f "$HOME/.kube/config-${cluster_name}"
    
    log_info "Successfully deleted cluster: ${cluster_name}-cluster"
}

# Main execution
main() {
    log_info "Deleting 3 Kind clusters"
    
    # Delete clusters sequentially to avoid kubeconfig lock conflicts
    for cluster in "${CLUSTERS[@]}"; do
        delete_cluster "$cluster"
    done
    
    # Remove merged kubeconfig
    rm -f "$HOME/.kube/config-multicluster"
    
    log_info "All clusters deleted successfully"
}

# Run main function
main "$@"