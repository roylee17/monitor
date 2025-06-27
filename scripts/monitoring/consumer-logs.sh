#!/bin/bash
# Consumer Chain Logs Script
# Shows logs for a consumer chain

set -e

# Source the common functions
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/../common/logging.sh"

# Function to get chain ID from consumer ID
get_chain_id() {
    local consumer_id="$1"
    kubectl --context "kind-alice-cluster" exec -n provider deployment/validator -- \
        interchain-security-pd query provider consumer-chain "$consumer_id" \
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

# Main function
main() {
    local consumer_id="${1:-0}"
    local cluster="${2:-bob}"
    local follow="${3:-true}"
    
    # Get chain ID
    local chain_id=$(get_chain_id "$consumer_id")
    if [ -z "$chain_id" ]; then
        log_error "Consumer $consumer_id not found"
        exit 1
    fi
    
    # Find namespace
    local namespace=$(find_consumer_namespace "$chain_id" "$cluster")
    if [ -z "$namespace" ]; then
        log_error "Consumer chain $chain_id (ID: $consumer_id) not deployed in $cluster cluster"
        log_info "Try one of these clusters where it's deployed:"
        for c in alice bob charlie; do
            ns=$(find_consumer_namespace "$chain_id" "$c")
            [ -n "$ns" ] && echo "  - $c"
        done
        exit 1
    fi
    
    # Show logs
    echo "📜 Showing logs for consumer $consumer_id ($chain_id) in $cluster cluster..."
    echo "Press Ctrl+C to stop following logs"
    echo ""
    
    if [ "$follow" = "true" ]; then
        kubectl --context "kind-${cluster}-cluster" -n "$namespace" \
            logs -f deployment/"$namespace" -c consumer-daemon
    else
        kubectl --context "kind-${cluster}-cluster" -n "$namespace" \
            logs deployment/"$namespace" -c consumer-daemon --tail=100
    fi
}

# Parse arguments
case "${1:-}" in
    -h|--help)
        echo "Usage: $0 [CONSUMER_ID] [CLUSTER] [--no-follow]"
        echo "Show logs for a consumer chain"
        echo ""
        echo "Arguments:"
        echo "  CONSUMER_ID    Consumer chain ID (default: 0)"
        echo "  CLUSTER        Cluster to check (default: bob)"
        echo "  --no-follow    Don't follow logs, just show last 100 lines"
        exit 0
        ;;
    *)
        follow="true"
        if [ "$3" = "--no-follow" ]; then
            follow="false"
        fi
        main "$1" "$2" "$follow"
        ;;
esac