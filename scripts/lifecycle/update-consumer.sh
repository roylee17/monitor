#!/bin/bash
# Update a consumer chain's parameters

set -e

# Source common functions
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/../common/logging.sh"
source "${SCRIPT_DIR}/consumer-utils.sh"

# Command line options
CONSUMER_ID=""
SPAWN_TIME=""
SPAWN_DELAY=""
METADATA_NAME=""
METADATA_DESC=""

# ============================================
# Functions
# ============================================

usage() {
    cat << EOF
Usage: $0 CONSUMER_ID [OPTIONS]

Update a consumer chain's parameters.

Arguments:
  CONSUMER_ID               The consumer chain ID to update

Options:
  -s, --spawn-time TIME     Set spawn time (e.g., '2025-01-01T00:00:00Z')
  -d, --delay SECONDS       Set spawn time to SECONDS from now
  -n, --name NAME           Update metadata name
  -D, --description DESC    Update metadata description
  -h, --help                Show this help message

Examples:
  $0 0 -d 120              # Update spawn time to 2 minutes from now
  $0 1 -n "Production"     # Update chain name
  $0 2 -s 2025-01-01T00:00:00Z -n "New Year Chain"
EOF
}

parse_arguments() {
    # Check for help
    if [[ "$1" == "-h" ]] || [[ "$1" == "--help" ]] || [[ $# -eq 0 ]]; then
        usage
        exit 0
    fi
    
    # First argument must be consumer ID
    CONSUMER_ID="$1"
    shift
    
    # Parse options
    while [[ $# -gt 0 ]]; do
        case $1 in
            -s|--spawn-time)
                SPAWN_TIME="$2"
                shift 2
                ;;
            -d|--delay)
                SPAWN_DELAY="$2"
                shift 2
                ;;
            -n|--name)
                METADATA_NAME="$2"
                shift 2
                ;;
            -D|--description)
                METADATA_DESC="$2"
                shift 2
                ;;
            *)
                log_error "Unknown option: $1"
                usage
                exit 1
                ;;
        esac
    done
    
    # Validate
    if ! validate_consumer_id "$CONSUMER_ID"; then
        exit 1
    fi
    
    if [ -n "$SPAWN_TIME" ] && [ -n "$SPAWN_DELAY" ]; then
        log_error "Cannot specify both --spawn-time and --delay"
        exit 1
    fi
}

create_update_json() {
    local consumer_id="$1"
    local consumer_info="$2"
    local spawn_time="$3"
    
    # Get current values
    local current_name current_desc
    current_name=$(echo "$consumer_info" | jq -r '.metadata.name // ""')
    current_desc=$(echo "$consumer_info" | jq -r '.metadata.description // ""')
    
    # Use provided values or keep current
    local name="${METADATA_NAME:-$current_name}"
    local desc="${METADATA_DESC:-$current_desc}"
    
    cat > /tmp/update_consumer.json << EOF
{
  "consumer_id": "${consumer_id}"
EOF

    # Add metadata if updating
    if [ -n "$METADATA_NAME" ] || [ -n "$METADATA_DESC" ]; then
        cat >> /tmp/update_consumer.json << EOF
,
  "metadata": {
    "name": "${name}",
    "description": "${desc}",
    "metadata": ""
  }
EOF
    fi

    # Add initialization parameters if updating spawn time
    if [ -n "$spawn_time" ]; then
        cat >> /tmp/update_consumer.json << EOF
,
  "initialization_parameters": {
    "initial_height": {
      "revision_number": 0,
      "revision_height": 1
    },
    "genesis_hash": "",
    "binary_hash": "",
    "spawn_time": "${spawn_time}",
    "unbonding_period": 86400000000000,
    "ccv_timeout_period": 259200000000000,
    "transfer_timeout_period": 3600000000000,
    "consumer_redistribution_fraction": "0.75",
    "blocks_per_distribution_transmission": 1000,
    "historical_entries": 10000
  }
EOF
    fi

    echo "}" >> /tmp/update_consumer.json
}

# ============================================
# Main
# ============================================

main() {
    parse_arguments "$@"
    
    # Get validator pod
    local pod
    pod=$(get_validator_pod)
    if [ -z "$pod" ]; then
        exit 1
    fi
    
    # Check consumer exists
    if ! consumer_exists "$CONSUMER_ID" "$pod"; then
        log_error "Consumer chain ${CONSUMER_ID} not found"
        exit 1
    fi
    
    # Get current info
    local consumer_info
    consumer_info=$(query_consumer_chain "$CONSUMER_ID" "$pod")
    
    print_header "Updating Consumer Chain ${CONSUMER_ID}"
    
    # Calculate spawn time if delay specified
    if [ -n "$SPAWN_DELAY" ]; then
        SPAWN_TIME=$(calculate_spawn_time "$SPAWN_DELAY")
        log_info "Calculated spawn time: $SPAWN_TIME"
    fi
    
    # Create update JSON
    create_update_json "$CONSUMER_ID" "$consumer_info" "$SPAWN_TIME"
    
    log_info "Update payload:"
    jq '.' /tmp/update_consumer.json
    
    # Copy JSON to pod
    kubectl cp /tmp/update_consumer.json "${DEFAULT_NAMESPACE}/${pod}":/tmp/update_consumer.json -c validator
    
    # Send update transaction
    log_info "Sending update transaction..."
    local tx_output
    tx_output=$(exec_on_validator "$pod" "$DEFAULT_NAMESPACE" \
        interchain-security-pd tx provider update-consumer /tmp/update_consumer.json \
        --from "${DEFAULT_VALIDATOR}" \
        --home /chain/.provider \
        --chain-id provider-1 \
        --keyring-backend test \
        --yes \
        --output json 2>&1)
    
    # Check transaction
    local tx_hash
    tx_hash=$(extract_tx_hash "$tx_output")
    if [ -z "$tx_hash" ]; then
        log_error "Failed to update consumer chain"
        echo "$tx_output"
        exit 1
    fi
    
    log_info "Transaction hash: ${tx_hash}"
    
    # Wait for transaction - use RAW_OUTPUT to get clean JSON
    local tx_result
    tx_result=$(RAW_OUTPUT=true wait_for_tx "$tx_hash" "$pod")
    if ! check_tx_success "$tx_result"; then
        log_error "Consumer update transaction failed"
        log_info "Use 'kubectl exec -n provider deployment/validator-alice -- interchain-security-pd q tx $tx_hash' to debug"
        exit 1
    fi
    
    log_success "Consumer chain updated successfully!"
    
    # Show updated details
    echo ""
    local updated_info
    updated_info=$(query_consumer_chain "$CONSUMER_ID" "$pod")
    display_consumer_details "$updated_info" "$pod"
}

# Run main
main "$@"