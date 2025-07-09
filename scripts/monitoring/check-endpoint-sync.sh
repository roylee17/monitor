#!/usr/bin/env bash
# Check if validator endpoints are in sync and optionally fix them

set -e

# Source common functions and variables
THIS_SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$THIS_SCRIPT_DIR/../utils/common.sh"
source "$THIS_SCRIPT_DIR/../utils/logging.sh"

# Configuration
BINARY="${BINARY:-interchain-security-pd}"
NAMESPACE="${NAMESPACE:-provider}"
VALIDATORS=(alice bob charlie)

# Fix mode
FIX_MODE="${FIX_MODE:-false}"

# ============================================
# Helper Functions
# ============================================

# Get on-chain P2P endpoint
get_onchain_endpoint() {
    local validator=$1
    local context="kind-${validator}-cluster"

    # Get validator operator address
    local val_addr
    val_addr=$(kubectl --context "$context" -n "$NAMESPACE" exec deployment/validator -c validator -- \
        "$BINARY" keys show "$validator" --keyring-backend test --bech val --home /chain/.provider -a 2>/dev/null) || {
        echo ""
        return 1
    }

    # Query validator and extract P2P endpoint
    kubectl --context "$context" -n "$NAMESPACE" exec deployment/validator -c validator -- \
        "$BINARY" query staking validators --home /chain/.provider --output json 2>/dev/null | \
        jq -r --arg addr "$val_addr" '.validators[] | select(.operator_address == $addr) | .description.details' | \
        grep -oE "p2p=[^ ]+" | cut -d= -f2 || echo ""
}

# Get LoadBalancer endpoint
get_k8s_endpoint() {
    local validator=$1
    local context="kind-${validator}-cluster"

    kubectl --context "$context" -n "$NAMESPACE" get svc p2p-loadbalancer \
        -o jsonpath='{.status.loadBalancer.ingress[0].ip}{.status.loadBalancer.ingress[0].hostname}' 2>/dev/null || echo ""
}

# Check if endpoints match
check_endpoint_sync() {
    local validator=$1
    local onchain=$2
    local k8s=$3

    if [ -z "$onchain" ] && [ -z "$k8s" ]; then
        echo "NO_ENDPOINTS"
    elif [ -z "$onchain" ]; then
        echo "MISSING_ONCHAIN"
    elif [ -z "$k8s" ]; then
        echo "MISSING_K8S"
    elif [ "$onchain" = "$k8s" ]; then
        echo "SYNCED"
    else
        echo "MISMATCH"
    fi
}

# Update on-chain endpoint
update_onchain_endpoint() {
    local validator=$1
    local endpoint=$2
    local context="kind-${validator}-cluster"

    log_info "Updating $validator on-chain endpoint to: $endpoint"

    # Build description
    local description="Validator $validator - p2p=$endpoint"

    # Execute transaction
    local tx_output
    tx_output=$(kubectl --context "$context" -n "$NAMESPACE" exec deployment/validator -c validator -- \
        "$BINARY" tx staking edit-validator \
        --details "$description" \
        --from "$validator" \
        --chain-id provider-1 \
        --keyring-backend test \
        --home /chain/.provider \
        --yes \
        --output json 2>&1) || {
        log_error "Failed to update validator $validator"
        return 1
    }

    # Extract transaction hash
    local tx_hash
    tx_hash=$(echo "$tx_output" | grep -E '^\{.*\}$' | jq -r '.txhash // empty' 2>/dev/null)

    if [ -n "$tx_hash" ] && [ "$tx_hash" != "empty" ]; then
        log_info "Transaction submitted: $tx_hash"
        return 0
    else
        log_error "Failed to submit transaction"
        return 1
    fi
}

# ============================================
# Main Function
# ============================================

main() {
    local out_of_sync=0
    local total_checked=0

    log_info "🔍 Checking validator endpoint synchronization..."
    echo ""

    # Table header
    printf "%-10s %-20s %-20s %-15s\n" "VALIDATOR" "ON-CHAIN" "KUBERNETES" "STATUS"
    printf "%-10s %-20s %-20s %-15s\n" "---------" "--------" "----------" "------"

    # Check each validator
    for validator in "${VALIDATORS[@]}"; do
        total_checked=$((total_checked + 1))

        # Get endpoints
        local onchain_endpoint
        onchain_endpoint=$(get_onchain_endpoint "$validator")

        local k8s_endpoint
        k8s_endpoint=$(get_k8s_endpoint "$validator")

        # Check sync status
        local status
        status=$(check_endpoint_sync "$validator" "$onchain_endpoint" "$k8s_endpoint")

        # Format output
        local status_icon=""
        case "$status" in
            SYNCED)
                status_icon="✅"
                ;;
            MISMATCH|MISSING_ONCHAIN)
                status_icon="⚠️"
                out_of_sync=$((out_of_sync + 1))
                ;;
            NO_ENDPOINTS|MISSING_K8S)
                status_icon="❌"
                out_of_sync=$((out_of_sync + 1))
                ;;
        esac

        printf "%-10s %-20s %-20s %-15s\n" \
            "$validator" \
            "${onchain_endpoint:-<not set>}" \
            "${k8s_endpoint:-<no endpoint>}" \
            "$status_icon $status"

        # Fix if requested and needed
        if [ "$FIX_MODE" = "true" ] && [ "$status" = "MISSING_ONCHAIN" ] && [ -n "$k8s_endpoint" ]; then
            echo ""
            update_onchain_endpoint "$validator" "$k8s_endpoint"
            echo ""
        elif [ "$FIX_MODE" = "true" ] && [ "$status" = "MISMATCH" ] && [ -n "$k8s_endpoint" ]; then
            echo ""
            log_warn "Endpoint mismatch for $validator: on-chain=$onchain_endpoint, k8s=$k8s_endpoint"
            update_onchain_endpoint "$validator" "$k8s_endpoint"
            echo ""
        fi
    done

    echo ""
    log_info "Summary: $out_of_sync out of $total_checked validators need attention"

    if [ "$out_of_sync" -gt 0 ] && [ "$FIX_MODE" != "true" ]; then
        echo ""
        echo "To fix out-of-sync endpoints, run:"
        echo "  FIX_MODE=true $0"
    fi

    # Exit with error if any validators are out of sync
    if [ "$out_of_sync" -gt 0 ]; then
        exit 1
    fi
}

# Handle arguments
case "${1:-}" in
    -h|--help)
        echo "Usage: $0 [--fix]"
        echo ""
        echo "Check if validator P2P endpoints are synchronized between on-chain and Kubernetes."
        echo ""
        echo "Options:"
        echo "  --fix    Automatically update on-chain endpoints to match Kubernetes"
        echo ""
        echo "Environment variables:"
        echo "  FIX_MODE=true    Same as --fix flag"
        exit 0
        ;;
    --fix)
        FIX_MODE=true
        ;;
esac

# Run main function
main
