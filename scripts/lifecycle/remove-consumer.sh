#!/bin/bash
# Remove a consumer chain

set -e

# Source common functions
THIS_SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${THIS_SCRIPT_DIR}/../utils/logging.sh"
source "${THIS_SCRIPT_DIR}/consumer-utils.sh"

# Command line options
CONSUMER_ID=""
FORCE=false

# ============================================
# Functions
# ============================================

usage() {
    cat << EOF
Usage: $0 CONSUMER_ID [OPTIONS]

Remove a consumer chain by ID.

Arguments:
  CONSUMER_ID          The consumer chain ID to remove

Options:
  -f, --force          Skip confirmation prompt
  -h, --help           Show this help message

Examples:
  $0 1                 # Remove consumer with ID 1
  $0 2 -f              # Remove consumer 2 without confirmation

Note: In ICS v7, consumer chains are typically removed through:
  - Governance proposal
  - Natural expiration
  - Owner action (if permitted)
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
            -f|--force)
                FORCE=true
                shift
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
}

confirm_removal() {
    local chain_id="$1"
    local consumer_id="$2"
    
    if [ "$FORCE" = true ]; then
        return 0
    fi
    
    read -p "Are you sure you want to remove consumer chain $chain_id (ID: $consumer_id)? [y/N] " -n 1 -r
    echo ""
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        log_info "Removal cancelled"
        exit 0
    fi
}

attempt_removal() {
    local consumer_id="$1"
    local pod="$2"
    local consumer_info="$3"
    
    # Check phase first
    local phase
    phase=$(echo "$consumer_info" | jq -r '.phase // "N/A"')
    if [ "$phase" != "CONSUMER_PHASE_LAUNCHED" ] && [ "$phase" != "3" ]; then
        log_error "Consumer chain must be in LAUNCHED phase to remove"
        log_info "Current phase: $phase"
        log_info "Use './scripts/lifecycle/force-launch-consumer.sh $consumer_id' to launch it first"
        return 1
    fi
    
    # Get owner address
    local owner
    owner=$(echo "$consumer_info" | jq -r '.owner_address // ""')
    if [ -z "$owner" ]; then
        log_error "Could not determine consumer chain owner"
        return 1
    fi
    
    # Determine which validator key owns this chain
    local owner_key=""
    case "$owner" in
        "cosmos1zaavvzxez0elundtn32qnk9lkm8kmcszzsv80v")
            owner_key="alice"
            ;;
        "cosmos15d2qdwxqx6qjq9nknrx73lfaugxvmzx6ltvs9d")
            owner_key="bob"
            ;;
        "cosmos1pz2trypc6e25hcwzn4h7jyqc57cr0qg4xcwepu")
            owner_key="charlie"
            ;;
        *)
            log_error "Unknown owner address: $owner"
            log_info "Chain must be removed by its owner"
            return 1
            ;;
    esac
    
    # Get the correct validator pod
    local owner_pod
    owner_pod=$(get_validator_pod "$owner_key")
    if [ -z "$owner_pod" ]; then
        log_error "Could not find pod for owner validator: $owner_key"
        return 1
    fi
    
    # Send removal transaction as the owner
    log_info "Sending removal transaction as owner ($owner_key)..."
    local tx_output
    tx_output=$(exec_on_validator "$owner_pod" "$DEFAULT_NAMESPACE" \
        interchain-security-pd tx provider remove-consumer "${consumer_id}" \
        --from ${owner_key} \
        --home /chain/.provider \
        --chain-id provider-1 \
        --keyring-backend test \
        --yes \
        --output json 2>&1)
    
    echo "$tx_output"
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
    
    # Get consumer info
    local consumer_info chain_id phase
    consumer_info=$(query_consumer_chain "$CONSUMER_ID" "$pod")
    chain_id=$(echo "$consumer_info" | jq -r '.chain_id // "N/A"')
    phase=$(echo "$consumer_info" | jq -r '.phase // "N/A"')
    
    print_header "Consumer Chain Removal"
    print_kv "Consumer ID" "$CONSUMER_ID"
    print_kv "Chain ID" "$chain_id"
    print_kv "Current Phase" "$phase"
    echo ""
    
    # Confirm removal
    confirm_removal "$chain_id" "$CONSUMER_ID"
    
    # Attempt removal
    local tx_output
    if ! tx_output=$(attempt_removal "$CONSUMER_ID" "$pod" "$consumer_info"); then
        exit 1
    fi
    
    # Check transaction
    local tx_hash
    tx_hash=$(extract_tx_hash "$tx_output")
    if [ -z "$tx_hash" ]; then
        log_warn "Could not initiate removal transaction"
        log_info "Consumer chains in ICS v7 are typically removed through:"
        print_item "Governance proposal"
        print_item "Natural expiration"
        print_item "Owner action (if permitted)"
        echo ""
        echo "$tx_output"
        exit 1
    fi
    
    log_info "Transaction hash: ${tx_hash}"
    
    # Wait for transaction - use RAW_OUTPUT to get clean JSON
    local tx_result
    tx_result=$(RAW_OUTPUT=true wait_for_tx "$tx_hash" "$pod")
    if ! check_tx_success "$tx_result"; then
        exit 1
    fi
    
    log_success "Removal/stop transaction successful!"
    
    # Check final status
    echo ""
    log_info "Checking final consumer status..."
    local final_info final_phase
    final_info=$(query_consumer_chain "$CONSUMER_ID" "$pod" 2>/dev/null || echo "{}")
    final_phase=$(echo "$final_info" | jq -r '.phase // "NOT_FOUND"')
    
    if [ "$final_phase" = "NOT_FOUND" ]; then
        log_success "Consumer chain removed"
    else
        log_info "Consumer phase: $final_phase"
    fi
    
    # List remaining consumers
    echo ""
    log_info "Remaining consumer chains:"
    local chains chain_count
    chains=$(query_consumer_chains "$pod")
    chain_count=$(echo "$chains" | jq '.chains | length' 2>/dev/null || echo "0")
    
    if [ "$chain_count" -eq 0 ]; then
        print_item "No consumer chains found"
    else
        echo "$chains" | jq -c '.chains[]' 2>/dev/null | while IFS= read -r chain; do
            display_consumer_summary "$chain"
        done
    fi
}

# Run main
main "$@"