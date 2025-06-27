#!/bin/bash
set -e

# Source common utilities
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/../common/logging.sh"

# Configuration
CLUSTERS=("alice" "bob" "charlie")

# Function to switch context
switch_context() {
    local cluster="$1"
    
    if [[ ! " ${CLUSTERS[@]} " =~ " ${cluster} " ]]; then
        log_error "Invalid cluster: $cluster"
        log_info "Valid clusters: ${CLUSTERS[*]}"
        exit 1
    fi
    
    local context="kind-${cluster}-cluster"
    
    log_info "Switching to context: $context"
    kubectl config use-context "$context"
    
    log_info "Current context set to: $context"
    log_info "You can now run kubectl commands against the $cluster cluster"
}

# Show current context
show_current() {
    local current
    current=$(kubectl config current-context 2>/dev/null || echo "none")
    log_info "Current context: $current"
}

# List all cluster contexts
list_contexts() {
    log_info "Available Kind cluster contexts:"
    for cluster in "${CLUSTERS[@]}"; do
        local context="kind-${cluster}-cluster"
        if kubectl config get-contexts -o name | grep -q "^$context$"; then
            echo "  - $context"
        fi
    done
}

# Show usage
usage() {
    echo "Usage: $0 [cluster|--current|--list]"
    echo ""
    echo "Switch kubectl context between Kind clusters"
    echo ""
    echo "Arguments:"
    echo "  cluster    - Switch to specified cluster (alice, bob, charlie)"
    echo "  --current  - Show current context"
    echo "  --list     - List available cluster contexts"
    echo ""
    echo "Examples:"
    echo "  $0 alice      # Switch to alice cluster"
    echo "  $0 --current  # Show current context"
    echo "  $0 --list     # List available contexts"
    exit 0
}

# Main execution
main() {
    case "${1:-}" in
        --help|-h)
            usage
            ;;
        --current)
            show_current
            ;;
        --list)
            list_contexts
            ;;
        "")
            show_current
            list_contexts
            ;;
        *)
            switch_context "$1"
            ;;
    esac
}

# Run main function
main "$@"