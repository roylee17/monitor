#!/bin/bash
# Test the complete consumer chain lifecycle

set -e

# Source common functions
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/../common/logging.sh"
source "${SCRIPT_DIR}/consumer-utils.sh"

# Configuration
CONSUMER_NAMESPACE="${CONSUMER_NAMESPACE:-consumer-chains}"
TEST_CHAIN_ID="autotest"
SPAWN_DELAY=30

# ============================================
# Test Functions
# ============================================

clean_environment() {
    log_step "Cleaning environment..."
    kubectl delete namespace "$DEFAULT_NAMESPACE" "$CONSUMER_NAMESPACE" --ignore-not-found
}

deploy_testnet() {
    log_step "Deploying testnet infrastructure..."
    kubectl apply -f k8s/testnet/
}

wait_for_validators() {
    log_step "Waiting for validators to be ready..."
    kubectl -n "$DEFAULT_NAMESPACE" wait --for=condition=ready pod -l role=validator --timeout=120s
}

verify_monitors() {
    log_step "Verifying monitors are healthy..."
    sleep 10
    
    local monitor_count
    monitor_count=$(kubectl -n "$DEFAULT_NAMESPACE" get pods -l role=monitor --no-headers | wc -l)
    if [ "$monitor_count" -eq 0 ]; then
        log_error "No monitors found"
        return 1
    fi
    
    log_info "Found $monitor_count monitors"
    kubectl -n "$DEFAULT_NAMESPACE" get pods -l role=monitor
}

create_test_consumer() {
    log_step "Creating test consumer chain..."
    ./scripts/lifecycle/create-consumer.sh -c "$TEST_CHAIN_ID" -s "$SPAWN_DELAY" -o
}

get_test_consumer_id() {
    local pod="$1"
    local chain_id="${TEST_CHAIN_ID}-0"
    
    local chains
    chains=$(query_consumer_chains "$pod")
    echo "$chains" | jq -r --arg cid "$chain_id" '.chains[] | select(.chain_id==$cid) | .consumer_id' | head -1
}

check_validator_selection() {
    local consumer_id="$1"
    
    log_info "Checking validator selection and opt-in status..."
    
    local validators=("alice" "bob" "charlie")
    local selected_count=0
    local opted_in_count=0
    
    for validator in "${validators[@]}"; do
        # Check monitor logs
        local logs
        logs=$(kubectl -n "$DEFAULT_NAMESPACE" logs "deploy/monitor-$validator" --tail=200 2>/dev/null || echo "")
        
        if echo "$logs" | grep -q "Local validator selected for subset"; then
            log_info "  ✓ $validator was selected by deterministic algorithm"
            ((selected_count++))
            
            # Check opt-in status
            if echo "$logs" | grep -q "Opt-in transaction sent successfully"; then
                log_info "  ✓ $validator opt-in succeeded"
                ((opted_in_count++))
            elif echo "$logs" | grep -q "Failed to send opt-in transaction"; then
                log_warn "  ✗ $validator opt-in failed"
            fi
        else
            log_info "  - $validator was not selected"
        fi
    done
    
    log_info "Summary: $selected_count validators selected, $opted_in_count successfully opted in"
    return 0
}

monitor_phase_transitions() {
    local consumer_id="$1"
    local pod="$2"
    local max_attempts=20
    
    log_info "Monitoring phase transitions..."
    
    local final_phase=""
    for i in $(seq 1 "$max_attempts"); do
        local consumer_info phase phase_color
        consumer_info=$(query_consumer_chain "$consumer_id" "$pod")
        phase=$(echo "$consumer_info" | jq -r '.phase // "UNKNOWN"')
        phase_color=$(get_phase_color "$phase")
        
        log_info "  Attempt $i/$max_attempts: ${phase_color}${phase}${COLOR_RESET}"
        
        case $phase in
            "CONSUMER_PHASE_LAUNCHED")
                log_success "Consumer successfully reached LAUNCHED phase!"
                final_phase="LAUNCHED"
                break
                ;;
            "CONSUMER_PHASE_REGISTERED")
                if [ "$i" -gt 10 ]; then
                    log_warn "Consumer reverted to REGISTERED - insufficient validators opted in"
                    final_phase="REGISTERED"
                    break
                fi
                ;;
        esac
        
        sleep 5
    done
    
    echo "$final_phase"
}

check_consumer_deployment() {
    local chain_id="${TEST_CHAIN_ID}-0"
    
    log_info "Checking consumer chain deployment..."
    sleep 5
    
    if kubectl -n "$CONSUMER_NAMESPACE" get pods | grep -q "$chain_id"; then
        log_success "Consumer chain pod deployed!"
        kubectl -n "$CONSUMER_NAMESPACE" get pods | grep "$chain_id"
        return 0
    else
        log_warn "Consumer chain pod not found"
        return 1
    fi
}

print_test_summary() {
    local consumer_id="$1"
    local pod="$2"
    local final_phase="$3"
    
    echo ""
    print_header "Test Summary"
    
    # Consumer details
    if [ -n "$consumer_id" ]; then
        local consumer_info
        consumer_info=$(query_consumer_chain "$consumer_id" "$pod" 2>/dev/null || echo "{}")
        if [ "$consumer_info" != "{}" ]; then
            display_consumer_details "$consumer_info" "$pod"
        fi
    fi
    
    # Monitor status
    echo ""
    log_info "Monitor pods:"
    kubectl -n "$DEFAULT_NAMESPACE" get pods -l role=monitor
    
    # Consumer pods
    echo ""
    log_info "Consumer pods:"
    kubectl -n "$CONSUMER_NAMESPACE" get pods 2>/dev/null || echo "  No consumer pods"
    
    # Test result
    echo ""
    if [ "$final_phase" = "LAUNCHED" ]; then
        log_success "TEST PASSED: Consumer chain successfully launched"
    else
        log_error "TEST FAILED: Consumer chain did not reach LAUNCHED phase"
    fi
}

# ============================================
# Main Test Flow
# ============================================

main() {
    local start_time
    start_time=$(date +%s)
    
    print_header "Consumer Chain Lifecycle Test"
    
    # Clean and deploy
    clean_environment
    deploy_testnet
    wait_for_validators
    verify_monitors || exit 1
    
    # Create consumer
    create_test_consumer
    
    # Get consumer ID
    local pod
    pod=$(get_validator_pod)
    if [ -z "$pod" ]; then
        log_error "Failed to get validator pod"
        exit 1
    fi
    
    local consumer_id
    consumer_id=$(get_test_consumer_id "$pod")
    if [ -z "$consumer_id" ]; then
        log_error "Failed to get consumer ID for $TEST_CHAIN_ID"
        exit 1
    fi
    
    log_info "Consumer ID: $consumer_id"
    
    # Check validator selection
    check_validator_selection "$consumer_id"
    
    # Monitor lifecycle
    local final_phase
    final_phase=$(monitor_phase_transitions "$consumer_id" "$pod")
    
    # Check deployment if launched
    if [ "$final_phase" = "LAUNCHED" ]; then
        check_consumer_deployment
    fi
    
    # Summary
    print_test_summary "$consumer_id" "$pod" "$final_phase"
    
    # Test duration
    local end_time duration
    end_time=$(date +%s)
    duration=$((end_time - start_time))
    echo ""
    log_info "Test completed in $duration seconds"
    
    # Exit code based on result
    if [ "$final_phase" = "LAUNCHED" ]; then
        exit 0
    else
        exit 1
    fi
}

# Run main
main "$@"
