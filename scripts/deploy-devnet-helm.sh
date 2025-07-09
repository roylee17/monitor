#!/bin/bash

# Deploy devnet using Helm charts
#
# This script deploys a 3-validator devnet using the ics-validator Helm chart.
# Each validator runs in its own Kind cluster.

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
HELM_CHART="$PROJECT_ROOT/helm/ics-operator"

# Source common functions
source "${SCRIPT_DIR}/utils/logging.sh"

# Configuration
VALIDATORS=("alice" "bob" "charlie")
ASSETS_DIR="$PROJECT_ROOT/.devnet/assets"

# Check if helm is installed
check_helm() {
    if ! command -v helm &> /dev/null; then
        error "Helm is not installed. Please install Helm first."
    fi
    log_info "Helm version: $(helm version --short)"
}

# Check if assets exist
check_assets() {
    if [ ! -d "$ASSETS_DIR" ]; then
        error "Assets directory not found. Run generate-devnet.sh first."
    fi

    # Check for genesis file
    if [ ! -f "$ASSETS_DIR/alice/config/genesis.json" ]; then
        error "Genesis file not found. Run generate-devnet.sh first."
    fi
}

# Check if Kind clusters exist
check_clusters() {
    for validator in "${VALIDATORS[@]}"; do
        if ! kind get clusters | grep -q "^${validator}-cluster$"; then
            error "Kind cluster '${validator}-cluster' not found. Run create-clusters.sh first."
        fi
    done
    log_info "All Kind clusters are present"
}


# Build peer list for a specific validator (excluding itself)
build_peer_list_for() {
    local current_validator=$1
    local peers=""

    if [ -f "$ASSETS_DIR/node_ids.txt" ]; then
        # Use host gateway IP (accessible from within Kind containers)
        local host_gateway="192.168.97.1"

        while IFS=: read -r name node_id; do
            # Skip the current validator (don't connect to itself)
            [ "$name" = "$current_validator" ] && continue

            # Map validator names to their host-exposed ports
            local port
            case "$name" in
                alice) port=26656 ;;  # alice's P2P port on host
                bob) port=26666 ;;    # bob's P2P port on host
                charlie) port=26676 ;; # charlie's P2P port on host
                *) continue ;;  # Skip unknown validators
            esac

            if [ -n "$peers" ]; then
                peers+=","
            fi
            # Use host gateway IP with host-exposed ports
            peers+="${node_id}@${host_gateway}:${port}"
        done < "$ASSETS_DIR/node_ids.txt"
    fi

    echo "$peers"
}

# Deploy validator using Helm
deploy_validator() {
    local validator=$1
    local namespace="provider"  # Use same namespace as old deployment
    local context="kind-${validator}-cluster"

    log_info "Deploying $validator to cluster $context"

    # Build peers list excluding self
    local peers=$(build_peer_list_for "$validator")
    log_info "Peers for $validator: $peers"

    # Create namespace
    kubectl --context "$context" create namespace "$namespace" --dry-run=client -o yaml | kubectl --context "$context" apply -f -

    # Create a temporary values file with genesis and keys
    local temp_values=$(mktemp)
    cat > "$temp_values" <<EOF
chain:
  genesis:
    type: inline
    inline: |
$(cat "$ASSETS_DIR/alice/config/genesis.json" | sed 's/^/      /')
keys:
  type: explicit
  validatorKey: |
$(cat "$ASSETS_DIR/$validator/config/priv_validator_key.json" | sed 's/^/    /')
  nodeKey: |
$(cat "$ASSETS_DIR/$validator/config/node_key.json" | sed 's/^/    /')
peers:
  persistent:
$(echo "$peers" | tr ',' '\n' | sed 's/^/    - "/' | sed 's/$/"/')
EOF

    # Deploy using Helm
    helm upgrade --install "$validator" "$HELM_CHART" \
        --namespace "$namespace" \
        --kube-context "$context" \
        --values "$HELM_CHART/devnet-values.yaml" \
        --values "$HELM_CHART/values/devnet-${validator}.yaml" \
        --values "$temp_values"

    local result=$?
    rm -f "$temp_values"

    if [ $result -eq 0 ]; then
        log_info "Successfully deployed $validator"
    else
        error "Failed to deploy $validator"
    fi
}

# Check validator status
check_validator_status() {
    local validator=$1
    local namespace="provider"  # Use same namespace as deployment
    local context="kind-${validator}-cluster"

    log_info "Checking status for $validator"

    # Get pod status
    kubectl --context "$context" -n "$namespace" get pods

    # Check if validator is running
    local validator_pod=$(kubectl --context "$context" -n "$namespace" get pods -l app.kubernetes.io/component=validator -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
    if [ -n "$validator_pod" ]; then
        # Check if syncing
        kubectl --context "$context" -n "$namespace" exec "$validator_pod" -- \
            curl -s localhost:26657/status | jq -r '.result.sync_info.catching_up' || true
    fi
}

# Main execution
main() {
    log_info "Starting devnet deployment with Helm"

    # Run checks
    check_helm
    check_assets
    check_clusters

    # Load Docker image into Kind clusters
    log_info "Loading monitor image into Kind clusters..."
    for validator in "${VALIDATORS[@]}"; do
        kind load docker-image ics-monitor:latest --name "${validator}-cluster"
    done

    # Deploy each validator
    for validator in "${VALIDATORS[@]}"; do
        deploy_validator "$validator"
    done

    # Wait a bit for services to stabilize
    log_info "Waiting for services to stabilize..."
    sleep 10

    # Check status
    log_info "Checking deployment status..."
    for validator in "${VALIDATORS[@]}"; do
        check_validator_status "$validator"
    done

    log_info "Devnet deployment complete!"
    log_info ""
    log_info "To check status:"
    log_info "  helm list -A"
    log_info ""
    log_info "To view logs:"
    log_info "  kubectl --context kind-alice-cluster -n provider logs -l app.kubernetes.io/component=validator"
    log_info "  kubectl --context kind-alice-cluster -n provider logs -l app.kubernetes.io/component=monitor"
    log_info ""
    log_info "To uninstall:"
    log_info "  helm uninstall alice -n provider --kube-context kind-alice-cluster"
}

# Parse arguments
case "${1:-}" in
    -h|--help)
        echo "Usage: $0"
        echo ""
        echo "Deploy a 3-validator devnet using Helm charts."
        echo "Prerequisites:"
        echo "  - Helm installed"
        echo "  - Kind clusters created (alice-cluster, bob-cluster, charlie-cluster)"
        echo "  - Devnet assets generated (run devnet-coordinator.sh)"
        exit 0
        ;;
esac

# Run main function
main
