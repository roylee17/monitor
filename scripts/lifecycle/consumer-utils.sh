#!/bin/bash
# Consumer Chain Utility Functions and Common Configuration
# These functions can be sourced by other scripts to work with consumer chains

# ============================================
# Common Configuration
# ============================================

# Default values
DEFAULT_NAMESPACE="${NAMESPACE:-provider}"
DEFAULT_VALIDATOR="${VALIDATOR:-alice}"
DEFAULT_SPAWN_DELAY=30

# ============================================
# Utility Functions
# ============================================

# Get validator pod by name
get_validator_pod() {
    local validator="${1:-$DEFAULT_VALIDATOR}"
    local namespace="${2:-$DEFAULT_NAMESPACE}"
    
    local pod
    pod=$(kubectl get pods -n "${namespace}" -l "app.kubernetes.io/component=validator" -o name 2>/dev/null | head -1)
    if [ -z "$pod" ]; then
        log_error "No validator pod found for ${validator}"
        return 1
    fi
    echo "${pod#pod/}"
}

# Get any validator pod (useful when we just need one)
get_any_validator_pod() {
    local namespace="${1:-$DEFAULT_NAMESPACE}"
    
    local pod
    pod=$(kubectl get pods -n "${namespace}" -l app.kubernetes.io/component=validator -o name 2>/dev/null | head -1)
    if [ -z "$pod" ]; then
        log_error "No validator pods found"
        return 1
    fi
    echo "${pod#pod/}"
}

# Execute command on validator pod
exec_on_validator() {
    local pod="$1"
    local namespace="${2:-$DEFAULT_NAMESPACE}"
    shift 2
    
    kubectl exec -n "${namespace}" "pod/${pod}" -c validator -- "$@"
}

# ============================================
# Consumer Chain Query Functions
# ============================================

# List all consumer chains
query_consumer_chains() {
    local pod="$1"
    local namespace="${2:-$DEFAULT_NAMESPACE}"
    
    exec_on_validator "$pod" "$namespace" \
        interchain-security-pd query provider list-consumer-chains \
        --home /chain/.provider \
        --output json 2>/dev/null
}

# Get specific consumer chain details
query_consumer_chain() {
    local consumer_id="$1"
    local pod="$2"
    local namespace="${3:-$DEFAULT_NAMESPACE}"
    
    exec_on_validator "$pod" "$namespace" \
        interchain-security-pd query provider consumer-chain "${consumer_id}" \
        --home /chain/.provider \
        --output json 2>/dev/null
}

# Get opted-in validators for a consumer chain
query_opted_in_validators() {
    local consumer_id="$1"
    local pod="$2"
    local namespace="${3:-$DEFAULT_NAMESPACE}"
    
    exec_on_validator "$pod" "$namespace" \
        interchain-security-pd query provider consumer-opted-in-validators "${consumer_id}" \
        --home /chain/.provider 2>/dev/null
}

# ============================================
# Transaction Functions
# ============================================

# Send a transaction and return the output
send_transaction() {
    local tx_command="$1"
    local from_account="$2"
    local pod="$3"
    local namespace="${4:-$DEFAULT_NAMESPACE}"
    
    exec_on_validator "$pod" "$namespace" \
        interchain-security-pd tx "${tx_command}" \
        --from "${from_account}" \
        --home /chain/.provider \
        --chain-id provider-1 \
        --keyring-backend test \
        --yes \
        --output json 2>&1
}

# Wait for transaction to be included in block
wait_for_tx() {
    local tx_hash="$1"
    local pod="$2"
    local namespace="${3:-$DEFAULT_NAMESPACE}"
    local max_attempts="${4:-10}"
    
    log_info "Waiting for transaction to be included in block..."
    
    for _ in $(seq 1 "$max_attempts"); do
        sleep 2
        local result
        result=$(exec_on_validator "$pod" "$namespace" \
            interchain-security-pd query tx "${tx_hash}" \
            --home /chain/.provider \
            --output json 2>/dev/null)
        
        if [ -n "$result" ] && [ "$result" != "null" ]; then
            echo "$result"
            return 0
        fi
    done
    
    log_error "Transaction not found after ${max_attempts} attempts"
    return 1
}

# Extract transaction hash from output
extract_tx_hash() {
    local tx_output="$1"
    echo "$tx_output" | jq -r '.txhash // empty' 2>/dev/null
}

# Check if transaction was successful
check_tx_success() {
    local tx_result="$1"
    
    # First check if we have valid JSON
    if ! echo "$tx_result" | jq empty 2>/dev/null; then
        log_error "Transaction result is not valid JSON"
        return 1
    fi
    
    local code
    code=$(echo "$tx_result" | jq -r '.code // empty' 2>/dev/null)
    
    # Handle empty code - assume success if we have a valid tx result with no code field
    if [ -z "$code" ]; then
        # Check if this looks like a valid transaction result
        if echo "$tx_result" | jq -e '.height' >/dev/null 2>&1; then
            # Has height field, likely a valid tx result with code 0
            return 0
        else
            log_error "Transaction failed: Invalid or empty response code"
            return 1
        fi
    fi
    
    # Check numeric code
    if ! [[ "$code" =~ ^[0-9]+$ ]]; then
        log_error "Transaction failed: Non-numeric response code: $code"
        return 1
    fi
    
    if [ "$code" -eq 0 ]; then
        return 0
    else
        local raw_log
        raw_log=$(echo "$tx_result" | jq -r '.raw_log // .log // "Unknown error"' 2>/dev/null)
        log_error "Transaction failed: $raw_log"
        return 1
    fi
}

# ============================================
# Time and Date Functions
# ============================================

# Calculate spawn time from delay in seconds
calculate_spawn_time() {
    local delay_seconds="${1:-$DEFAULT_SPAWN_DELAY}"
    
    # Handle both Linux and macOS date commands
    date -u -v+"${delay_seconds}"S '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || \
    date -u -d "+${delay_seconds} seconds" '+%Y-%m-%dT%H:%M:%SZ'
}

# Get current time in UTC
get_current_time() {
    date -u '+%Y-%m-%dT%H:%M:%SZ'
}

# Calculate time difference in seconds
time_diff_seconds() {
    local time1="$1"
    local time2="$2"
    
    local epoch1 epoch2
    epoch1=$(date -d "$time1" +%s 2>/dev/null || date -j -f "%Y-%m-%dT%H:%M:%SZ" "$time1" +%s 2>/dev/null || echo "0")
    epoch2=$(date -d "$time2" +%s 2>/dev/null || date -j -f "%Y-%m-%dT%H:%M:%SZ" "$time2" +%s 2>/dev/null || echo "0")
    
    echo $((epoch2 - epoch1))
}

# ============================================
# Display Functions
# ============================================

# Get phase color for display
get_phase_color() {
    local phase="$1"
    case "$phase" in
        "CONSUMER_PHASE_REGISTERED")
            echo "${COLOR_YELLOW}"
            ;;
        "CONSUMER_PHASE_INITIALIZED")
            echo "${COLOR_CYAN}"
            ;;
        "CONSUMER_PHASE_LAUNCHED")
            echo "${COLOR_GREEN}"
            ;;
        "CONSUMER_PHASE_STOPPED")
            echo "${COLOR_RED}"
            ;;
        "CONSUMER_PHASE_DELETED")
            echo "${COLOR_GRAY}"
            ;;
        *)
            echo "${COLOR_RESET}"
            ;;
    esac
}

# Display consumer chain summary
display_consumer_summary() {
    local consumer_info="$1"
    
    if [ "${RAW_OUTPUT}" = "true" ]; then
        # In raw mode, just output the JSON
        echo "$consumer_info"
        return
    fi
    
    local consumer_id chain_id phase phase_color
    consumer_id=$(echo "$consumer_info" | jq -r '.consumer_id // "N/A"')
    chain_id=$(echo "$consumer_info" | jq -r '.chain_id // "N/A"')
    phase=$(echo "$consumer_info" | jq -r '.phase // "N/A"')
    phase_color=$(get_phase_color "$phase")
    
    printf "  %-4s %-30s ${phase_color}%-25s${COLOR_RESET}\n" \
        "$consumer_id" "$chain_id" "$phase"
}

# Display detailed consumer chain info
display_consumer_details() {
    local consumer_info="$1"
    local pod="$2"
    local namespace="${3:-$DEFAULT_NAMESPACE}"
    
    if [ "${RAW_OUTPUT}" = "true" ]; then
        # In raw mode, output enriched JSON with opted-in validators
        local consumer_id validators
        consumer_id=$(echo "$consumer_info" | jq -r '.consumer_id // "N/A"')
        validators=$(RAW_OUTPUT=true query_opted_in_validators "$consumer_id" "$pod" "$namespace" 2>/dev/null | grep -v "^validators:" | grep -E "^\s*-" | sed 's/^- //' | jq -R -s -c 'split("\n") | map(select(length > 0))')
        
        # Add validators to the consumer info
        echo "$consumer_info" | jq --argjson validators "$validators" '. + {opted_in_validators: $validators}'
        return
    fi
    
    local consumer_id chain_id phase owner spawn_time metadata_name metadata_desc
    consumer_id=$(echo "$consumer_info" | jq -r '.consumer_id // "N/A"')
    chain_id=$(echo "$consumer_info" | jq -r '.chain_id // "N/A"')
    phase=$(echo "$consumer_info" | jq -r '.phase // "N/A"')
    owner=$(echo "$consumer_info" | jq -r '.owner_address // ""')
    spawn_time=$(echo "$consumer_info" | jq -r '.init_params.spawn_time // ""')
    metadata_name=$(echo "$consumer_info" | jq -r '.metadata.name // ""')
    metadata_desc=$(echo "$consumer_info" | jq -r '.metadata.description // ""')
    
    # Provide defaults for empty fields
    [ -z "$owner" ] && owner="N/A"
    [ -z "$spawn_time" ] && spawn_time="N/A"
    [ -z "$metadata_name" ] && metadata_name="N/A"
    [ -z "$metadata_desc" ] && metadata_desc="N/A"
    
    local phase_color
    phase_color=$(get_phase_color "$phase")
    
    print_subheader "Consumer Chain ${consumer_id}"
    print_kv "Chain ID" "$chain_id"
    echo -e "  Phase                : ${phase_color}${phase}${COLOR_RESET}"
    print_kv "Owner" "$owner"
    print_kv "Metadata Name" "$metadata_name"
    print_kv "Description" "$metadata_desc"
    print_kv "Spawn Time" "$spawn_time"
    
    # Show time until spawn if applicable
    if [ "$phase" = "CONSUMER_PHASE_INITIALIZED" ] && [ "$spawn_time" != "N/A" ] && [ "$spawn_time" != "0001-01-01T00:00:00Z" ]; then
        local current_time seconds_left
        current_time=$(get_current_time)
        seconds_left=$(time_diff_seconds "$current_time" "$spawn_time")
        
        if [ "$seconds_left" -gt 0 ]; then
            print_kv "Time until spawn" "$seconds_left seconds"
        else
            print_kv "Time until spawn" "Spawn time passed"
        fi
    fi
    
    # Show opted-in validators
    echo ""
    echo "Opted-in validators:"
    local validators
    validators=$(query_opted_in_validators "$consumer_id" "$pod" "$namespace" | grep -E "^\s*-" | sed 's/^- //')
    if [ -n "$validators" ]; then
        echo "$validators" | while read -r val; do
            print_item "$val"
        done
    else
        print_item "None"
    fi
}

# ============================================
# Validation Functions
# ============================================

# Validate consumer ID is a number
validate_consumer_id() {
    local consumer_id="$1"
    
    if ! [[ "$consumer_id" =~ ^[0-9]+$ ]]; then
        log_error "Invalid consumer ID: $consumer_id (must be a number)"
        return 1
    fi
    return 0
}

# Check if consumer chain exists
consumer_exists() {
    local consumer_id="$1"
    local pod="$2"
    local namespace="${3:-$DEFAULT_NAMESPACE}"
    
    local info
    info=$(query_consumer_chain "$consumer_id" "$pod" "$namespace")
    if [ -z "$info" ] || [ "$info" = "null" ]; then
        return 1
    fi
    return 0
}

# ============================================
# Export common variables
# ============================================

export DEFAULT_NAMESPACE DEFAULT_VALIDATOR DEFAULT_SPAWN_DELAY