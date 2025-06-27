#!/usr/bin/env bash

# Register validator P2P endpoints in their descriptions
# This allows monitors to discover peer endpoints from chain state

set -e

# Source common functions and variables
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/../utils/common.sh"
source "$SCRIPT_DIR/../utils/logging.sh"

# Binary and chain configuration
BINARY="${BINARY:-interchain-security-pd}"
CHAIN_ID="${CHAIN_ID:-provider-1}"
ASSETS_DIR="${ASSETS_DIR:-$SCRIPT_DIR/../../testnet/assets}"

# Validators list
VALIDATORS=(alice bob charlie)

# Get LoadBalancer external IP/hostname for a validator
get_validator_loadbalancer_endpoint() {
    local validator=$1
    local context="kind-${validator}-cluster"
    
    # Get the LoadBalancer service external IP or hostname
    local endpoint=$(kubectl --context "$context" -n provider get svc p2p-loadbalancer \
        -o jsonpath='{.status.loadBalancer.ingress[0]}' 2>/dev/null)
    
    if [ -z "$endpoint" ] || [ "$endpoint" = "null" ]; then
        log_error "LoadBalancer has no external endpoint yet for $validator"
        return 1
    fi
    
    # Extract IP or hostname from the JSON object
    local ip=$(echo "$endpoint" | jq -r '.ip // empty')
    local hostname=$(echo "$endpoint" | jq -r '.hostname // empty')
    
    if [ -n "$ip" ]; then
        echo "$ip"
    elif [ -n "$hostname" ]; then
        echo "$hostname"
    else
        log_error "LoadBalancer has ingress but no IP or hostname for $validator"
        return 1
    fi
}

# Update validator description with P2P endpoint
update_validator_endpoint() {
    local validator=$1
    local endpoint=$2
    local context="kind-${validator}-cluster"
    
    log_info "Updating $validator with P2P endpoint: $endpoint"
    
    # Build the description with P2P endpoint
    local description="Validator $validator
p2p=$endpoint"
    
    # Create the transaction to edit validator
    kubectl --context "$context" -n provider exec deployment/validator -- \
        "$BINARY" tx staking edit-validator \
        --details "$description" \
        --from "$validator" \
        --chain-id "$CHAIN_ID" \
        --keyring-backend test \
        --home /chain/.provider \
        --yes \
        --output json || {
        log_error "Failed to update validator $validator"
        return 1
    }
    
    log_info "Successfully updated $validator"
}

# Query validator to verify endpoint
verify_validator_endpoint() {
    local validator=$1
    local context="kind-${validator}-cluster"
    
    # Get validator operator address
    local val_addr=$(kubectl --context "$context" -n provider exec deployment/validator -- \
        "$BINARY" keys show "$validator" --keyring-backend test --bech val --home /chain/.provider -a)
    
    # Query validator info
    local details=$(kubectl --context "$context" -n provider exec deployment/validator -- \
        "$BINARY" query staking validators --home /chain/.provider --output json | \
        jq --arg addr "$val_addr" -r '.validators[] | select(.operator_address == $addr) | .description.details')
    
    if echo "$details" | grep -q "p2p="; then
        local endpoint=$(echo "$details" | grep "p2p=" | sed 's/.*p2p=//')
        log_info "Verified $validator endpoint: $endpoint"
        return 0
    else
        log_error "No P2P endpoint found for $validator"
        return 1
    fi
}

# Main execution
main() {
    log_info "Registering validator P2P endpoints..."
    
    # Wait for chain to be ready
    log_info "Waiting for chain to be ready..."
    sleep 5
    
    # Wait for LoadBalancers to get external IPs
    log_info "Waiting for LoadBalancer services to get external endpoints..."
    for validator in "${VALIDATORS[@]}"; do
        local context="kind-${validator}-cluster"
        local attempts=0
        local max_attempts=30
        
        while [ $attempts -lt $max_attempts ]; do
            local endpoint
            endpoint=$(kubectl --context "$context" -n provider get svc p2p-loadbalancer \
                -o jsonpath='{.status.loadBalancer.ingress[0]}' 2>/dev/null)
            
            if [ -n "$endpoint" ] && [ "$endpoint" != "null" ]; then
                log_info "LoadBalancer ready for $validator"
                break
            fi
            
            attempts=$((attempts + 1))
            if [ $attempts -eq $max_attempts ]; then
                log_error "LoadBalancer for $validator did not get external endpoint in time"
            else
                echo -n "."
                sleep 2
            fi
        done
    done
    echo ""
    
    # Update each validator with their LoadBalancer endpoint
    for validator in "${VALIDATORS[@]}"; do
        # Get the validator's LoadBalancer endpoint
        local lb_endpoint
        lb_endpoint=$(get_validator_loadbalancer_endpoint "$validator")
        if [ -z "$lb_endpoint" ]; then
            log_error "Failed to get LoadBalancer endpoint for $validator, skipping"
            continue
        fi
        
        log_info "Validator $validator LoadBalancer endpoint: $lb_endpoint"
        
        # Register the endpoint
        update_validator_endpoint "$validator" "$lb_endpoint"
        
        # Wait for transaction to be processed
        sleep 2
    done
    
    # Verify all validators
    log_info "Verifying validator endpoints..."
    for validator in "${VALIDATORS[@]}"; do
        verify_validator_endpoint "$validator"
    done
    
    log_info "Validator endpoint registration complete!"
    log_info ""
    log_info "Validators can now discover each other's P2P endpoints from chain state."
    log_info "Consumer chains will use these endpoints with calculated port offsets."
}

# Handle script arguments
case "${1:-}" in
    -h|--help)
        echo "Usage: $0"
        echo ""
        echo "Register validator P2P endpoints in their on-chain descriptions."
        echo "This allows monitors to discover peer endpoints from chain state."
        exit 0
        ;;
esac

# Run main function
main "$@"