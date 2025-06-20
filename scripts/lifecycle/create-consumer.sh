#!/bin/bash
# Create a consumer chain for Interchain Security testing

set -e

# Source common functions
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/../common/logging.sh"
source "${SCRIPT_DIR}/consumer-utils.sh"

# Command line options
CHAIN_ID=""
SPAWN_DELAY=$DEFAULT_SPAWN_DELAY
AUTO_OPT_IN=false
METADATA_NAME=""
METADATA_DESC=""

# ============================================
# Functions
# ============================================

usage() {
    cat << EOF
Usage: $0 [OPTIONS]

Create a consumer chain for Interchain Security testing.

Options:
  -c, --chain-id ID         Consumer chain ID (default: auto-generated)
  -s, --seconds SECONDS     Spawn delay in seconds (default: $DEFAULT_SPAWN_DELAY)
  -n, --name NAME           Metadata name for the chain
  -d, --description DESC    Metadata description
  -o, --opt-in              Auto opt-in all validators
  -h, --help                Show this help message

Examples:
  $0                       # Create with defaults
  $0 -c testnet-1 -s 120   # Custom chain ID, 2 minute spawn delay
  $0 -n "My Chain" -o      # Named chain with auto opt-in
EOF
}

parse_arguments() {
    while [[ $# -gt 0 ]]; do
        case $1 in
            -c|--chain-id)
                CHAIN_ID="$2"
                shift 2
                ;;
            -s|--seconds)
                SPAWN_DELAY="$2"
                shift 2
                ;;
            -n|--name)
                METADATA_NAME="$2"
                shift 2
                ;;
            -d|--description)
                METADATA_DESC="$2"
                shift 2
                ;;
            -o|--opt-in)
                AUTO_OPT_IN=true
                shift
                ;;
            -h|--help)
                usage
                exit 0
                ;;
            *)
                log_error "Unknown option: $1"
                usage
                exit 1
                ;;
        esac
    done
}

get_next_consumer_id() {
    local pod="$1"
    
    local chains=$(query_consumer_chains "$pod")
    local highest_id=$(echo "$chains" | jq -r '.chains[].consumer_id' 2>/dev/null | sort -n | tail -1)
    
    if [ -z "$highest_id" ]; then
        echo "0"
    else
        echo $((highest_id + 1))
    fi
}

generate_chain_id() {
    local next_id="$1"
    
    if [ -z "$CHAIN_ID" ]; then
        local timestamp=$(date +%s)
        CHAIN_ID="consumer-${next_id}-${timestamp}-0"
        log_info "Generated chain ID: ${CHAIN_ID}"
    else
        # Ensure chain ID ends with revision number
        if [[ ! "$CHAIN_ID" =~ -[0-9]+$ ]]; then
            CHAIN_ID="${CHAIN_ID}-0"
            log_info "Added revision number: ${CHAIN_ID}"
        fi
    fi
}

create_consumer_json() {
    local chain_id="$1"
    local spawn_time="$2"
    local next_id="$3"
    
    # Use defaults if not provided
    local name="${METADATA_NAME:-Test Consumer Chain $next_id}"
    local desc="${METADATA_DESC:-Consumer chain created with ID $next_id}"
    
    cat > /tmp/consumer.json << EOF
{
  "chain_id": "${chain_id}",
  "metadata": {
    "name": "${name}",
    "description": "${desc}",
    "metadata": "test"
  },
  "initialization_parameters": {
    "initial_height": {
      "revision_number": 0,
      "revision_height": 1
    },
    "genesis_hash": "bm90X3VzZWQ=",
    "binary_hash": "bm90X3VzZWQ=",
    "spawn_time": "${spawn_time}",
    "unbonding_period": 86400000000000,
    "ccv_timeout_period": 259200000000000,
    "transfer_timeout_period": 3600000000000,
    "consumer_redistribution_fraction": "0.75",
    "blocks_per_distribution_transmission": 1000,
    "historical_entries": 10000
  },
  "power_shaping_parameters": {
    "top_n": 0
  }
}
EOF
}

opt_in_validators() {
    local consumer_id="$1"
    
    log_info "Auto opt-in enabled, opting in all validators..."
    
    local validators=("alice" "bob" "charlie")
    for validator in "${validators[@]}"; do
        local pod=$(get_validator_pod "$validator" 2>/dev/null)
        if [ -n "$pod" ]; then
            local output=$(send_transaction "provider opt-in ${consumer_id}" "$validator" "$pod" 2>&1)
            if [ $? -eq 0 ]; then
                log_success "  ✓ ${validator} opted in"
            else
                log_warn "  ✗ Failed to opt-in ${validator}"
            fi
        fi
    done
}

# ============================================
# Main
# ============================================

main() {
    parse_arguments "$@"
    
    # Get validator pod
    local pod=$(get_validator_pod)
    if [ -z "$pod" ]; then
        exit 1
    fi
    
    # Get next consumer ID
    log_info "Querying existing consumer chains..."
    local next_id=$(get_next_consumer_id "$pod")
    log_info "Next consumer ID will be: ${next_id}"
    
    # Generate chain ID
    generate_chain_id "$next_id"
    
    # Calculate spawn time
    local spawn_time=$(calculate_spawn_time "$SPAWN_DELAY")
    local current_time=$(get_current_time)
    log_info "Current time: ${current_time}"
    log_info "Spawn time: ${spawn_time} (${SPAWN_DELAY} seconds from now)"
    
    # Create consumer JSON
    create_consumer_json "$CHAIN_ID" "$spawn_time" "$next_id"
    
    print_header "Creating Consumer Chain: ${CHAIN_ID}"
    
    # Copy JSON to pod
    kubectl cp /tmp/consumer.json ${DEFAULT_NAMESPACE}/${pod}:/tmp/consumer.json -c validator
    
    # Send create transaction
    log_info "Sending create-consumer transaction..."
    local tx_output=$(exec_on_validator "$pod" "$DEFAULT_NAMESPACE" \
        interchain-security-pd tx provider create-consumer /tmp/consumer.json \
        --from "${DEFAULT_VALIDATOR}" \
        --home /chain/.provider \
        --chain-id provider-1 \
        --keyring-backend test \
        --yes \
        --output json 2>&1)
    
    # Extract and check transaction hash
    local tx_hash=$(extract_tx_hash "$tx_output")
    if [ -z "$tx_hash" ]; then
        log_error "Failed to create consumer chain"
        echo "$tx_output"
        exit 1
    fi
    
    log_info "Transaction hash: ${tx_hash}"
    
    # Wait for transaction - use RAW_OUTPUT to get clean JSON
    local tx_result=$(RAW_OUTPUT=true wait_for_tx "$tx_hash" "$pod")
    if ! check_tx_success "$tx_result"; then
        log_error "Consumer creation transaction failed"
        log_info "Use 'kubectl exec -n provider deployment/validator-alice -- interchain-security-pd q tx $tx_hash' to debug"
        exit 1
    fi
    
    # Extract consumer ID from events
    local consumer_id=$(echo "$tx_result" | \
        jq -r '.events[] | select(.type=="create_consumer") | .attributes[] | select(.key=="consumer_id") | .value' 2>/dev/null | \
        head -1)
    
    if [ -z "$consumer_id" ]; then
        consumer_id=$next_id
    fi
    
    log_success "Consumer chain created with ID: ${consumer_id}"
    
    # Auto opt-in if requested
    if [ "$AUTO_OPT_IN" = true ]; then
        opt_in_validators "$consumer_id"
    fi
    
    # Display final details
    echo ""
    local consumer_info=$(query_consumer_chain "$consumer_id" "$pod")
    display_consumer_details "$consumer_info" "$pod"
}

# Run main
main "$@"