#!/bin/bash
# Wait for blockchain nodes to be ready for transactions

set -e

# Source common functions
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/../utils/common.sh"
source "${SCRIPT_DIR}/../utils/logging.sh"

# ============================================
# Configuration
# ============================================

# Command line options
DEFAULT_NAMESPACE="${NAMESPACE:-provider}"
DEFAULT_RPC_PORT="${RPC_PORT:-26657}"
DEFAULT_MAX_WAIT="${MAX_WAIT:-60}"
DEFAULT_CHECK_MODE="${CHECK_MODE:-single}"  # single or all
DEFAULT_VALIDATOR="${VALIDATOR:-alice}"
DEFAULT_VALIDATORS=(alice bob charlie)

# Global variables
CHECK_MODE=""
VALIDATORS_TO_CHECK=()
MAX_WAIT=""
NAMESPACE=""
RPC_PORT=""
USE_RPC=true
USE_STATUS_CMD=false

# ============================================
# Functions
# ============================================

# Check if chain is ready via RPC endpoint
# Arguments:
#   $1 - Kubernetes context to use
#   $2 - Namespace (optional, defaults to DEFAULT_NAMESPACE)
#   $3 - RPC port (optional, defaults to DEFAULT_RPC_PORT)
# Returns:
#   0 if chain is ready, 1 otherwise
check_chain_ready_rpc() {
    local context="$1"
    local namespace="${2:-$DEFAULT_NAMESPACE}"
    local rpc_port="${3:-$DEFAULT_RPC_PORT}"

    # Query status via RPC
    local status_json
    status_json=$(kubectl --context "$context" -n "$namespace" exec deployment/validator -c validator -- \
        curl -s "http://localhost:${rpc_port}/status" 2>/dev/null) || return 1

    # Extract result
    local result
    result=$(echo "$status_json" | jq -r '.result // empty' 2>/dev/null) || return 1

    if [[ -z "$result" ]] || [[ "$result" == "empty" ]]; then
        return 1
    fi

    # Extract sync info
    local catching_up
    local latest_height
    catching_up=$(echo "$result" | jq -r 'if has("sync_info") and (.sync_info | has("catching_up")) then .sync_info.catching_up else true end')
    latest_height=$(echo "$result" | jq -r '.sync_info.latest_block_height // "0"')

    # Chain is ready when:
    # 1. Not catching up
    # 2. Has produced blocks (height > 1)
    if [[ "$catching_up" == "false" ]] && [[ "${latest_height}" -gt "1" ]]; then
        return 0
    fi

    return 1
}

# Check if chain is ready via status command
# Arguments:
#   $1 - Kubernetes context to use
#   $2 - Namespace (optional, defaults to DEFAULT_NAMESPACE)
#   $3 - Binary name (optional, defaults to interchain-security-pd)
# Returns:
#   0 if chain is ready, 1 otherwise
check_chain_ready_status() {
    local context="$1"
    local namespace="${2:-$DEFAULT_NAMESPACE}"
    local binary="${3:-interchain-security-pd}"

    # Get status - redirect stderr to capture all output
    local status_output
    status_output=$(kubectl --context "$context" -n "$namespace" exec deployment/validator -c validator -- \
        "$binary" status --home /chain/.provider 2>/dev/null) || return 1

    # Must be valid JSON
    if [[ -z "$status_output" ]]; then
        return 1
    fi

    # Validate JSON
    if ! echo "$status_output" | jq empty >/dev/null 2>&1; then
        return 1
    fi

    # Check key indicators
    local catching_up
    local latest_height
    local voting_power
    catching_up=$(echo "$status_output" | jq -r 'if has("sync_info") and (.sync_info | has("catching_up")) then .sync_info.catching_up else true end')
    latest_height=$(echo "$status_output" | jq -r '.sync_info.latest_block_height // "0"')
    voting_power=$(echo "$status_output" | jq -r '.validator_info.voting_power // "0"')

    # Convert to numbers for comparison
    latest_height=$(echo "$latest_height" | sed 's/[^0-9]//g')
    voting_power=$(echo "$voting_power" | sed 's/[^0-9]//g')

    # All conditions must be met
    if [[ "$catching_up" == "false" ]] && [[ -n "$latest_height" ]] && [[ "$latest_height" -gt 0 ]] && [[ -n "$voting_power" ]] && [[ "$voting_power" -gt 0 ]]; then
        return 0
    fi

    return 1
}

# Check if a validator is ready
# Arguments:
#   $1 - Validator name
# Returns:
#   0 if ready, 1 otherwise
check_validator_ready() {
    local validator="$1"
    local context="kind-${validator}-cluster"

    if [[ "$USE_RPC" == "true" ]]; then
        check_chain_ready_rpc "$context" "$NAMESPACE" "$RPC_PORT"
    else
        check_chain_ready_status "$context" "$NAMESPACE"
    fi
}

# Wait for single validator
wait_for_single() {
    local validator="${VALIDATORS_TO_CHECK[0]}"
    local context="kind-${validator}-cluster"
    local start_time
    start_time=$(date +%s)

    log_info "Waiting for $validator node to be ready for transactions..."

    while true; do
        if check_validator_ready "$validator"; then
            echo ""
            log_success "Chain is ready!"

            # Show final status
            if [[ "$USE_RPC" == "true" ]]; then
                local status_json
                local height
                local catching_up
                status_json=$(kubectl --context "$context" -n "$NAMESPACE" exec deployment/validator -c validator -- \
                    curl -s "http://localhost:${RPC_PORT}/status" 2>/dev/null)

                height=$(echo "$status_json" | jq -r '.result.sync_info.latest_block_height // "unknown"')
                catching_up=$(echo "$status_json" | jq -r 'if .result.sync_info.catching_up == false then "false" elif .result.sync_info.catching_up == true then "true" else "unknown" end')

                log_info "Status: height=$height, catching_up=$catching_up"
            else
                local status_json
                local height
                local vp
                status_json=$(kubectl --context "$context" -n "$NAMESPACE" exec deployment/validator -c validator -- \
                    interchain-security-pd status --home /chain/.provider 2>/dev/null)
                height=$(echo "$status_json" | jq -r '.sync_info.latest_block_height // "unknown"')
                vp=$(echo "$status_json" | jq -r '.validator_info.voting_power // "unknown"')

                log_info "Status: height=$height, voting_power=$vp"
            fi
            return 0
        fi

        # Check timeout
        local current_time
        local elapsed
        current_time=$(date +%s)
        elapsed=$((current_time - start_time))

        if [[ $elapsed -ge $MAX_WAIT ]]; then
            echo ""
            log_error "Timeout: Chain not ready after ${MAX_WAIT} seconds"

            # Show debug info
            if [[ "$USE_RPC" == "true" ]]; then
                local status_json
                status_json=$(kubectl --context "$context" -n "$NAMESPACE" exec deployment/validator -c validator -- \
                    curl -s "http://localhost:${RPC_PORT}/status" 2>/dev/null || echo '{}')

                log_info "Last status:"
                echo "$status_json" | jq '.result.sync_info // {}' 2>/dev/null || echo "$status_json"
            fi
            return 1
        fi

        # Show progress
        echo -ne "\rWaiting... (${elapsed}s elapsed)"
        sleep 2
    done
}

# Wait for all validators
wait_for_all() {
    local start_time
    local all_ready
    start_time=$(date +%s)
    all_ready=false

    log_info "Waiting for all validator nodes to be ready..."

    while [[ "$all_ready" == "false" ]]; do
        all_ready=true
        local ready_count=0

        for validator in "${VALIDATORS_TO_CHECK[@]}"; do
            if check_validator_ready "$validator"; then
                ready_count=$((ready_count + 1))
            else
                all_ready=false
            fi
        done

        # Calculate elapsed time
        local current_time
        local elapsed
        current_time=$(date +%s)
        elapsed=$((current_time - start_time))

        # Update status
        echo -ne "\rReady: $ready_count/${#VALIDATORS_TO_CHECK[@]} validators (${elapsed}s elapsed)"

        # Check timeout
        if [[ $elapsed -ge $MAX_WAIT ]]; then
            echo ""
            log_error "Timeout waiting for validators to be ready after ${MAX_WAIT}s"
            return 1
        fi

        # If not all ready, wait and retry
        if [[ "$all_ready" == "false" ]]; then
            sleep 2
        fi
    done

    echo ""
    log_success "All validators are ready!"

    # Show final status
    for validator in "${VALIDATORS_TO_CHECK[@]}"; do
        local context="kind-${validator}-cluster"
        if [[ "$USE_RPC" == "true" ]]; then
            local status_json
            local height
            local catching_up
            status_json=$(kubectl --context "$context" -n "$NAMESPACE" exec deployment/validator -c validator -- \
                curl -s "http://localhost:${RPC_PORT}/status" 2>/dev/null)
            height=$(echo "$status_json" | jq -r '.result.sync_info.latest_block_height // "unknown"')
            catching_up=$(echo "$status_json" | jq -r 'if .result.sync_info.catching_up == false then "false" elif .result.sync_info.catching_up == true then "true" else "unknown" end')
            log_info "$validator: height=$height, catching_up=$catching_up"
        else
            local status_json
            local height
            local vp
            status_json=$(kubectl --context "$context" -n "$NAMESPACE" exec deployment/validator -c validator -- \
                interchain-security-pd status --home /chain/.provider 2>/dev/null)
            height=$(echo "$status_json" | jq -r '.sync_info.latest_block_height // "unknown"')
            vp=$(echo "$status_json" | jq -r '.validator_info.voting_power // "unknown"')
            log_info "$validator: height=$height, voting_power=$vp"
        fi
    done

    return 0
}

# ============================================
# Main
# ============================================

# Usage function
usage() {
    cat << EOF
Usage: $0 [OPTIONS]

Wait for blockchain nodes to be ready for transactions.
Supports checking single validator or all validators.

Options:
  -a, --all            Check all validators (default: single validator)
  -v, --validator VAL  Validator to check (default: alice)
  -m, --method METHOD  Check method: rpc or status (default: rpc)
  -t, --timeout SECS   Maximum seconds to wait (default: 60)
  -n, --namespace NS   Kubernetes namespace (default: provider)
  -p, --port PORT      RPC port (default: 26657)
  -h, --help           Show this help message

Environment variables:
  NAMESPACE            Kubernetes namespace (default: provider)
  RPC_PORT             RPC port to check (default: 26657)
  MAX_WAIT             Maximum seconds to wait (default: 60)
  CHECK_MODE           Check mode: single or all (default: single)
  VALIDATOR            Validator to check (default: alice)

Examples:
  $0                   # Check alice validator via RPC
  $0 --all             # Check all validators
  $0 -v bob            # Check bob validator
  $0 -m status         # Use status command instead of RPC
  $0 -a -t 120         # Check all validators, wait up to 2 minutes
EOF
}

# Parse arguments
parse_arguments() {
    # Set defaults
    CHECK_MODE="$DEFAULT_CHECK_MODE"
    MAX_WAIT="$DEFAULT_MAX_WAIT"
    NAMESPACE="$DEFAULT_NAMESPACE"
    RPC_PORT="$DEFAULT_RPC_PORT"
    local validator="$DEFAULT_VALIDATOR"

    while [[ $# -gt 0 ]]; do
        case $1 in
            -a|--all)
                CHECK_MODE="all"
                shift
                ;;
            -v|--validator)
                validator="$2"
                shift 2
                ;;
            -m|--method)
                case "$2" in
                    rpc)
                        USE_RPC=true
                        USE_STATUS_CMD=false
                        ;;
                    status)
                        USE_RPC=false
                        USE_STATUS_CMD=true
                        ;;
                    *)
                        log_error "Invalid method: $2. Use 'rpc' or 'status'"
                        usage
                        exit 1
                        ;;
                esac
                shift 2
                ;;
            -t|--timeout)
                MAX_WAIT="$2"
                shift 2
                ;;
            -n|--namespace)
                NAMESPACE="$2"
                shift 2
                ;;
            -p|--port)
                RPC_PORT="$2"
                shift 2
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

    # Set validators to check based on mode
    if [[ "$CHECK_MODE" == "all" ]]; then
        VALIDATORS_TO_CHECK=("${DEFAULT_VALIDATORS[@]}")
    else
        VALIDATORS_TO_CHECK=("$validator")
    fi
}

main() {
    parse_arguments "$@"

    # Validate timeout is a number
    if ! [[ "$MAX_WAIT" =~ ^[0-9]+$ ]]; then
        log_error "Timeout must be a number"
        exit 1
    fi

    # Execute based on mode
    if [[ "$CHECK_MODE" == "all" ]]; then
        wait_for_all
    else
        wait_for_single
    fi
}

# Run main
main "$@"
