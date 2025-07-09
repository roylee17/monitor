#!/usr/bin/env bash
# Query and report validator P2P endpoints from both on-chain and Kubernetes sources

set -e

# Source common functions and variables
THIS_SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$THIS_SCRIPT_DIR/../utils/common.sh"
source "$THIS_SCRIPT_DIR/../utils/logging.sh"

# Binary and chain configuration
BINARY="${BINARY:-interchain-security-pd}"
CHAIN_ID="${CHAIN_ID:-provider-1}"
NAMESPACE="${NAMESPACE:-provider}"

# Validators list
VALIDATORS=(alice bob charlie)

# ============================================
# Query Functions
# ============================================

# Get validator info from chain
get_validator_info() {
    local validator=$1
    local context="kind-${validator}-cluster"

    # Get validator operator address
    local val_addr
    val_addr=$(kubectl --context "$context" -n "$NAMESPACE" exec deployment/validator -c validator -- \
        "$BINARY" keys show "$validator" --keyring-backend test --bech val --home /chain/.provider -a 2>/dev/null) || {
        echo ""
        return 1
    }

    # Query all validators and filter for this one
    kubectl --context "$context" -n "$NAMESPACE" exec deployment/validator -c validator -- \
        "$BINARY" query staking validators --home /chain/.provider --output json 2>/dev/null | \
        jq --arg addr "$val_addr" '.validators[] | select(.operator_address == $addr)'
}

# Extract P2P endpoint from validator description
extract_p2p_endpoint() {
    local description=$1
    echo "$description" | grep -oE "p2p=[^ ]+" | cut -d= -f2 || echo ""
}

# Get LoadBalancer endpoint for a validator
get_loadbalancer_endpoint() {
    local validator=$1
    local context="kind-${validator}-cluster"

    # Get the LoadBalancer service external IP or hostname
    local endpoint
    endpoint=$(kubectl --context "$context" -n "$NAMESPACE" get svc p2p-loadbalancer \
        -o jsonpath='{.status.loadBalancer.ingress[0]}' 2>/dev/null) || {
        echo ""
        return 1
    }

    if [ -z "$endpoint" ] || [ "$endpoint" = "null" ]; then
        echo ""
        return 1
    fi

    # Extract IP or hostname from the JSON object
    local ip=$(echo "$endpoint" | jq -r '.ip // empty' 2>/dev/null)
    local hostname=$(echo "$endpoint" | jq -r '.hostname // empty' 2>/dev/null)

    if [ -n "$ip" ]; then
        echo "$ip"
    elif [ -n "$hostname" ]; then
        echo "$hostname"
    else
        echo ""
    fi
}

# Get LoadBalancer service details
get_loadbalancer_details() {
    local validator=$1
    local context="kind-${validator}-cluster"

    kubectl --context "$context" -n "$NAMESPACE" get svc p2p-loadbalancer -o json 2>/dev/null | \
        jq '{
            name: .metadata.name,
            type: .spec.type,
            port: .spec.ports[0].port,
            targetPort: .spec.ports[0].targetPort,
            externalIP: .status.loadBalancer.ingress[0].ip // .status.loadBalancer.ingress[0].hostname // "pending"
        }' 2>/dev/null || echo "{}"
}

# Query transaction history for edit-validator transactions
get_recent_endpoint_updates() {
    local validator=$1
    local context="kind-${validator}-cluster"
    local limit=${2:-5}

    # Get validator address
    local val_addr
    val_addr=$(kubectl --context "$context" -n "$NAMESPACE" exec deployment/validator -c validator -- \
        "$BINARY" keys show "$validator" --keyring-backend test --home /chain/.provider -a 2>/dev/null) || {
        echo "[]"
        return
    }

    # Query recent transactions (this is a simplified version - actual implementation would need to filter by message type)
    kubectl --context "$context" -n "$NAMESPACE" exec deployment/validator -c validator -- \
        "$BINARY" query txs --events "message.sender='$val_addr'" --limit "$limit" --home /chain/.provider --output json 2>/dev/null | \
        jq '.txs // []' || echo "[]"
}

# ============================================
# Reporting Functions
# ============================================

# Display endpoint comparison
display_endpoint_comparison() {
    log_info "📊 Validator P2P Endpoint Status"
    echo ""
    printf "%-10s %-20s %-20s %-10s\n" "VALIDATOR" "ON-CHAIN ENDPOINT" "K8S LOADBALANCER" "STATUS"
    printf "%-10s %-20s %-20s %-10s\n" "---------" "-----------------" "----------------" "------"

    for validator in "${VALIDATORS[@]}"; do
        # Get on-chain endpoint
        local validator_info
        validator_info=$(get_validator_info "$validator")
        local on_chain_endpoint=""
        if [ -n "$validator_info" ]; then
            local description=$(echo "$validator_info" | jq -r '.description.details // ""')
            on_chain_endpoint=$(extract_p2p_endpoint "$description")
        fi

        # Get LoadBalancer endpoint
        local k8s_endpoint
        k8s_endpoint=$(get_loadbalancer_endpoint "$validator")

        # Determine status
        local status="❌ MISSING"
        if [ -n "$on_chain_endpoint" ] && [ -n "$k8s_endpoint" ]; then
            if [ "$on_chain_endpoint" = "$k8s_endpoint" ]; then
                status="✅ SYNCED"
            else
                status="⚠️  MISMATCH"
            fi
        elif [ -n "$on_chain_endpoint" ]; then
            status="⚠️  NO K8S"
        elif [ -n "$k8s_endpoint" ]; then
            status="⚠️  NOT SET"
        fi

        printf "%-10s %-20s %-20s %-10s\n" \
            "$validator" \
            "${on_chain_endpoint:-<not registered>}" \
            "${k8s_endpoint:-<no endpoint>}" \
            "$status"
    done
    echo ""
}

# Display detailed validator information
display_validator_details() {
    local validator=$1

    log_info "🔍 Detailed Information for Validator: $validator"
    echo ""

    # Get validator info
    local validator_info
    validator_info=$(get_validator_info "$validator")

    if [ -z "$validator_info" ]; then
        log_error "Failed to get validator info for $validator"
        return 1
    fi

    # Display validator details
    echo "📝 On-Chain Information:"
    echo "$validator_info" | jq '{
        moniker: .description.moniker,
        details: .description.details,
        operator_address: .operator_address,
        consensus_pubkey: .consensus_pubkey,
        status: .status,
        tokens: .tokens,
        commission_rate: .commission.commission_rates.rate
    }'
    echo ""

    # Display LoadBalancer details
    echo "🌐 Kubernetes LoadBalancer:"
    get_loadbalancer_details "$validator"
    echo ""

    # Display recent transactions
    echo "📜 Recent Endpoint Updates:"
    local txs
    txs=$(get_recent_endpoint_updates "$validator" 3)
    if [ "$txs" = "[]" ]; then
        echo "  No recent edit-validator transactions found"
    else
        echo "$txs" | jq -r '.[] | "  TX: \(.txhash[0:16])... Height: \(.height) Time: \(.timestamp)"'
    fi
    echo ""
}

# Display LoadBalancer services across all clusters
display_loadbalancer_services() {
    log_info "🔗 LoadBalancer Services Status"
    echo ""

    for validator in "${VALIDATORS[@]}"; do
        local context="kind-${validator}-cluster"
        echo "Cluster: $validator"

        # Get service details
        local svc_json
        svc_json=$(kubectl --context "$context" -n "$NAMESPACE" get svc p2p-loadbalancer -o json 2>/dev/null)

        if [ -n "$svc_json" ]; then
            echo "$svc_json" | jq -r '
                "  Service: \(.metadata.name)",
                "  Type: \(.spec.type)",
                "  Port: \(.spec.ports[0].port) -> \(.spec.ports[0].targetPort)",
                "  External IP: \(.status.loadBalancer.ingress[0].ip // .status.loadBalancer.ingress[0].hostname // "pending")",
                "  Selector: \(.spec.selector | to_entries | map("\(.key)=\(.value)") | join(", "))"
            '
        else
            echo "  ❌ LoadBalancer service not found"
        fi
        echo ""
    done
}

# ============================================
# Main Function
# ============================================

main() {
    local mode="${1:-summary}"
    local validator="${2:-}"

    case "$mode" in
        summary)
            display_endpoint_comparison
            ;;
        details)
            if [ -z "$validator" ]; then
                log_error "Validator name required for details mode"
                echo "Usage: $0 details <validator>"
                exit 1
            fi
            display_validator_details "$validator"
            ;;
        services)
            display_loadbalancer_services
            ;;
        all)
            display_endpoint_comparison
            echo ""
            display_loadbalancer_services
            ;;
        *)
            echo "Usage: $0 [mode] [validator]"
            echo ""
            echo "Modes:"
            echo "  summary    - Show endpoint comparison table (default)"
            echo "  details    - Show detailed info for a specific validator"
            echo "  services   - Show LoadBalancer service details"
            echo "  all        - Show all information"
            echo ""
            echo "Examples:"
            echo "  $0                    # Show summary"
            echo "  $0 details alice      # Show details for alice"
            echo "  $0 services           # Show LoadBalancer services"
            echo "  $0 all                # Show everything"
            exit 1
            ;;
    esac
}

# Run main function
main "$@"
