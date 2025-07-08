#!/bin/bash
# List consumer chains with optional detailed view

set -e

# Source common functions
THIS_SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${THIS_SCRIPT_DIR}/../utils/logging.sh"
source "${THIS_SCRIPT_DIR}/consumer-utils.sh"

# Command line options
SHOW_DETAILS=false
SHOW_INFO=false
CONSUMER_ID=""

# ============================================
# Functions
# ============================================

usage() {
    cat << EOF
Usage: $0 [OPTIONS] [CONSUMER_ID]

List consumer chains or show details of a specific chain.

Arguments:
  CONSUMER_ID          Show details for specific consumer (optional)

Options:
  -d, --details        Show detailed information for all chains
  -i, --info           Show summary information (count, IDs, etc.)
  -h, --help           Show this help message

Examples:
  $0                   # List all consumer chains
  $0 -d                # List all chains with details
  $0 -i                # Show summary information
  $0 0                 # Show details for consumer 0
EOF
}

parse_arguments() {
    while [[ $# -gt 0 ]]; do
        case $1 in
            -d|--details)
                SHOW_DETAILS=true
                shift
                ;;
            -i|--info)
                SHOW_INFO=true
                shift
                ;;
            -h|--help)
                usage
                exit 0
                ;;
            -*)
                log_error "Unknown option: $1"
                usage
                exit 1
                ;;
            *)
                CONSUMER_ID="$1"
                shift
                ;;
        esac
    done
}

list_all_consumers() {
    local pod="$1"
    local show_details="$2"
    
    # Get all consumer chains
    local chains_json chain_count
    chains_json=$(query_consumer_chains "$pod")
    chain_count=$(echo "$chains_json" | jq '.chains | length' 2>/dev/null || echo "0")
    
    if [ "${RAW_OUTPUT}" = "true" ]; then
        # In raw mode, output the JSON directly
        echo "$chains_json"
        return
    fi
    
    print_header "Consumer Chains (Total: $chain_count)"
    
    if [ "$chain_count" -eq 0 ]; then
        log_info "No consumer chains found"
        return
    fi
    
    if [ "$show_details" = false ]; then
        # Simple list view
        printf "  %-4s %-30s %-25s\n" "ID" "Chain ID" "Phase"
        printf "  %-4s %-30s %-25s\n" "----" "------------------------------" "-------------------------"
        
        # Process each chain as JSON object
        echo "$chains_json" | jq -c '.chains[]' 2>/dev/null | while IFS= read -r chain; do
            display_consumer_summary "$chain"
        done
    else
        # Detailed view for all chains
        local i=0
        local total chain
        total=$(echo "$chains_json" | jq '.chains | length' 2>/dev/null || echo "0")
        while [ "$i" -lt "$total" ]; do
            chain=$(echo "$chains_json" | jq ".chains[$i]" 2>/dev/null)
            if [ -n "$chain" ] && [ "$chain" != "null" ]; then
                display_consumer_details "$chain" "$pod"
                echo ""
            fi
            i=$((i + 1))
        done
    fi
}

show_consumer_details() {
    local consumer_id="$1"
    local pod="$2"
    
    # Validate consumer ID
    if ! validate_consumer_id "$consumer_id"; then
        exit 1
    fi
    
    # Get consumer info
    local consumer_info
    consumer_info=$(query_consumer_chain "$consumer_id" "$pod")
    
    if [ -z "$consumer_info" ] || [ "$consumer_info" = "null" ]; then
        log_error "Consumer chain $consumer_id not found"
        exit 1
    fi
    
    if [ "${RAW_OUTPUT}" = "true" ]; then
        # In raw mode, use display_consumer_details which will output JSON
        display_consumer_details "$consumer_info" "$pod"
        return
    fi
    
    print_header "Consumer Chain Details"
    display_consumer_details "$consumer_info" "$pod"
}

show_info_summary() {
    local pod="$1"
    
    print_header "Consumer Chain Information"
    
    # Get all chains
    local chains_json chain_count
    chains_json=$(query_consumer_chains "$pod")
    chain_count=$(echo "$chains_json" | jq '.chains | length' 2>/dev/null || echo "0")
    
    log_info "Total consumer chains: $chain_count"
    
    if [ "$chain_count" -gt 0 ]; then
        # Get consumer IDs
        local consumer_ids highest_id
        consumer_ids=$(echo "$chains_json" | jq -r '.chains[].consumer_id' 2>/dev/null | sort -n)
        highest_id=$(echo "$consumer_ids" | tail -1)
        local next_id=$((highest_id + 1))
        
        echo ""
        print_subheader "Consumer Chain Summary"
        
        # Show each chain with basic info
        echo "$chains_json" | jq -c '.chains[]' 2>/dev/null | while IFS= read -r chain; do
            local id chain_id phase
            id=$(echo "$chain" | jq -r '.consumer_id // "N/A"' 2>/dev/null)
            chain_id=$(echo "$chain" | jq -r '.chain_id // "N/A"' 2>/dev/null)
            phase=$(echo "$chain" | jq -r '.phase // "N/A"' 2>/dev/null)
            echo "  ID: $id | Chain: $chain_id | Phase: $phase"
        done
        
        echo ""
        log_info "Highest consumer ID: $highest_id"
        log_info "Next available consumer ID: $next_id"
    else
        log_info "Next available consumer ID: 0"
    fi
}

# ============================================
# Main
# ============================================

main() {
    parse_arguments "$@"
    
    # Get validator pod
    local pod
    pod=$(get_any_validator_pod)
    if [ -z "$pod" ]; then
        exit 1
    fi
    
    if [ -n "$CONSUMER_ID" ]; then
        # Show details for specific consumer
        show_consumer_details "$CONSUMER_ID" "$pod"
    elif [ "$SHOW_INFO" = true ]; then
        # Show info summary
        show_info_summary "$pod"
    else
        # List all consumers
        list_all_consumers "$pod" "$SHOW_DETAILS"
    fi
}

# Run main
main "$@"