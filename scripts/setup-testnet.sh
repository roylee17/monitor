#!/bin/bash

# Complete Testnet Setup Script
#
# This script combines the testnet coordinator and K8s manifest generation
# to provide a complete testnet setup workflow.

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Source common functions
source "${SCRIPT_DIR}/common/logging.sh"

# Override log_info to include timestamp (using variables from logging.sh)
log_info() {
    echo -e "${COLOR_GREEN}[$(date +'%Y-%m-%d %H:%M:%S')]${COLOR_RESET} $*"
}

# Show usage
usage() {
    echo "Usage: $0 [OPTIONS]"
    echo ""
    echo "Complete testnet setup including genesis ceremony and K8s manifest generation."
    echo ""
    echo "Options:"
    echo "  -t, --time TIME         Use fixed genesis time (e.g., '2025-01-01T00:00:00Z')"
    echo "  -s, --save-keys         Save P2P/consensus keys to backup for consistent node IDs"
    echo "  -d, --deploy            Deploy to Kubernetes after generation"
    echo "  -h, --help              Show this help message"
    echo ""
    echo "Examples:"
    echo "  $0                      # Generate with current time"
    echo "  $0 -s                   # Generate and save keys for consistency"
    echo "  $0 -t '2025-01-01T00:00:00Z' -s  # Fully deterministic setup"
    echo "  $0 -s -d                # Generate and deploy to Kubernetes"
    exit 0
}

# Parse command line arguments
COORDINATOR_ARGS=()
DEPLOY=false

while [[ $# -gt 0 ]]; do
    case $1 in
        -t|--time)
            COORDINATOR_ARGS+=("-t" "$2")
            shift 2
            ;;
        -s|--save-keys)
            COORDINATOR_ARGS+=("-s")
            shift
            ;;
        -d|--deploy)
            DEPLOY=true
            shift
            ;;
        -h|--help)
            usage
            ;;
        *)
            echo "Unknown option: $1"
            usage
            ;;
    esac
done

# Main execution
main() {
    log_info "Starting complete testnet setup..."
    echo ""
    
    # Step 1: Run testnet coordinator
    log_info "Step 1: Running genesis ceremony..."
    "$SCRIPT_DIR/testnet-coordinator.sh" "${COORDINATOR_ARGS[@]}"
    echo ""
    
    # Step 2: Generate Kubernetes manifests
    log_info "Step 2: Generating Kubernetes manifests..."
    "$SCRIPT_DIR/generate-k8s-manifests.sh"
    echo ""
    
    # Step 3: Optional deployment
    if [ "$DEPLOY" = true ]; then
        log_info "Step 3: Deploying to Kubernetes..."
        
        # Check if kubectl is available
        if ! command -v kubectl &> /dev/null; then
            warn "kubectl not found. Skipping deployment."
            warn "To deploy manually, run: kubectl apply -f k8s/testnet/generated/"
        else
            kubectl apply -f "$SCRIPT_DIR/../k8s/testnet/generated/"
            echo ""
            
            # Wait for pods to be ready
            log_info "Waiting for validators to be ready..."
            kubectl wait --for=condition=ready pod -l role=validator -n provider --timeout=120s || {
                warn "Validators not ready after 120s. Check pod status with:"
                warn "  kubectl get pods -n provider"
            }
        fi
    else
        log_info "Setup complete! To deploy to Kubernetes, run:"
        log_info "  kubectl apply -f k8s/testnet/generated/"
        log_info ""
        log_info "Or run this script with -d flag to deploy automatically."
    fi
    
    echo ""
    log_info "Testnet setup finished!"
    log_info ""
    log_info "Useful commands:"
    log_info "  kubectl get pods -n provider                    # Check pod status"
    log_info "  kubectl logs -n provider deploy/validator-alice # View validator logs"
    log_info "  kubectl logs -n provider deploy/monitor         # View monitor logs"
    log_info "  ./scripts/lifecycle/create-consumer.sh -c testchain1 -d 2 # Create consumer chain"
}

# Run main function
main