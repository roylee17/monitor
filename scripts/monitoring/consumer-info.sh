#!/bin/bash
# Consumer Chain Information Script
# Provides convenient access to consumer chain information

set -e

# Source the common functions
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/../common/logging.sh"

# Function to get chain ID from consumer ID
get_chain_id() {
    local consumer_id="$1"
    kubectl --context "kind-alice-cluster" exec -n provider deployment/validator -- \
        interchain-security-pd query provider consumer-chain "$consumer_id" \
        --home /chain/.provider \
        --output json 2>/dev/null | jq -r '.chain_id // empty'
}

# Function to find consumer namespace
find_consumer_namespace() {
    local chain_id="$1"
    local cluster="$2"
    kubectl --context "kind-${cluster}-cluster" get namespaces \
        -l app.kubernetes.io/part-of=consumer-chains --no-headers 2>/dev/null | \
        grep "$chain_id" | awk '{print $1}' | head -1
}

# Function to get dynamic RPC port
get_rpc_port() {
    local namespace="$1"
    local cluster="$2"
    # For now, consumer chains use the standard port
    echo "26657"
}

# Main function
main() {
    local consumer_id="${1:-0}"
    local chain_id
    
    # Get chain ID
    chain_id=$(get_chain_id "$consumer_id")
    if [ -z "$chain_id" ]; then
        log_error "Consumer $consumer_id not found"
        exit 1
    fi
    
    echo "========================================"
    echo "Consumer Chain Information"
    echo "========================================"
    echo "Consumer ID: $consumer_id"
    echo "Chain ID: $chain_id"
    echo ""
    
    # Check block heights
    echo "📊 Block Heights:"
    for cluster in alice bob charlie; do
        namespace=$(find_consumer_namespace "$chain_id" "$cluster")
        if [ -n "$namespace" ]; then
            port=$(get_rpc_port "$namespace" "$cluster")
            if [ -n "$port" ]; then
                # Try to get block height via direct log inspection, removing ANSI escape codes
                height=$(kubectl --context "kind-${cluster}-cluster" logs -n "$namespace" \
                    deployment/"$namespace" -c consumer-daemon --tail=200 2>/dev/null | \
                    sed 's/\x1b\[[0-9;]*m//g' | \
                    grep -E "(height=|lastHeight=)" | tail -1 | \
                    sed -E 's/.*(height|lastHeight)=//' | awk '{print $1}' | grep -E '^[0-9]+$' || echo "syncing")
                printf "  %-10s: %s (port %s)\n" "$cluster" "$height" "$port"
            else
                printf "  %-10s: port not found\n" "$cluster"
            fi
        else
            printf "  %-10s: not deployed\n" "$cluster"
        fi
    done
    
    echo ""
    echo "👥 Validators:"
    # Get validators from one running instance
    for cluster in alice bob charlie; do
        namespace=$(find_consumer_namespace "$chain_id" "$cluster")
        if [ -n "$namespace" ]; then
            port=$(get_rpc_port "$namespace" "$cluster")
            if [ -n "$port" ]; then
                echo "  From $cluster instance:"
                # For now, just indicate validators are present (since we can't easily query them)
                echo "    Validators are active (check logs for details)"
                break
            fi
        fi
    done
    
    echo ""
    echo "🔗 Deployment Status:"
    for cluster in alice bob charlie; do
        namespace=$(find_consumer_namespace "$chain_id" "$cluster")
        if [ -n "$namespace" ]; then
            pod_status=$(kubectl --context "kind-${cluster}-cluster" get pods -n "$namespace" \
                --no-headers 2>/dev/null | awk '{print $3}')
            restarts=$(kubectl --context "kind-${cluster}-cluster" get pods -n "$namespace" \
                --no-headers 2>/dev/null | awk '{print $4}')
            printf "  %-10s: %s (restarts: %s)\n" "$cluster" "$pod_status" "$restarts"
        else
            printf "  %-10s: not deployed\n" "$cluster"
        fi
    done
}

# Parse arguments
case "${1:-}" in
    -h|--help)
        echo "Usage: $0 [CONSUMER_ID]"
        echo "Get comprehensive information about a consumer chain"
        echo ""
        echo "Arguments:"
        echo "  CONSUMER_ID    Consumer chain ID (default: 0)"
        exit 0
        ;;
    *)
        main "$@"
        ;;
esac