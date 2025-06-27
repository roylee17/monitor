#!/bin/bash
# Fix consumer chain peer connections across Kind clusters
# This is necessary because consumer chains in different clusters can't resolve each other's DNS

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/../common/logging.sh"

# Function to get consumer chain node ID
get_consumer_node_id() {
    local cluster=$1
    local namespace=$2
    
    kubectl --context "kind-${cluster}-cluster" exec -n "$namespace" \
        deployment/"$namespace" -c consumer-daemon -- \
        interchain-security-cd tendermint show-node-id --home "/consumer/.${namespace}" 2>/dev/null || echo ""
}

# Function to update consumer peers
update_consumer_peers() {
    local chain_id=$1
    local clusters=("alice" "bob" "charlie")
    local peers=""
    
    log_info "Collecting node IDs for consumer chain: $chain_id"
    
    # Collect node IDs from all clusters
    declare -A node_ids
    declare -A has_deployment
    
    for cluster in "${clusters[@]}"; do
        # Check if namespace exists
        if kubectl --context "kind-${cluster}-cluster" get namespace "$chain_id" &>/dev/null; then
            has_deployment[$cluster]=true
            node_id=$(get_consumer_node_id "$cluster" "$chain_id")
            if [ -n "$node_id" ]; then
                node_ids[$cluster]=$node_id
                log_info "  $cluster: $node_id"
            else
                log_warn "  $cluster: Failed to get node ID"
            fi
        else
            has_deployment[$cluster]=false
            log_info "  $cluster: Not deployed"
        fi
    done
    
    # Build peers list for each deployed instance
    for cluster in "${clusters[@]}"; do
        if [ "${has_deployment[$cluster]}" = "true" ]; then
            local cluster_peers=""
            
            # Add peers from other clusters
            for peer_cluster in "${clusters[@]}"; do
                if [ "$peer_cluster" != "$cluster" ] && [ "${has_deployment[$peer_cluster]}" = "true" ] && [ -n "${node_ids[$peer_cluster]}" ]; then
                    # Map to host port based on cluster
                    local host_port
                    case "$peer_cluster" in
                        alice) host_port=30156 ;;  # Base NodePort for consumer P2P
                        bob) host_port=30256 ;;    # Bob's consumer P2P NodePort
                        charlie) host_port=30356 ;; # Charlie's consumer P2P NodePort
                    esac
                    
                    if [ -n "$cluster_peers" ]; then
                        cluster_peers+=","
                    fi
                    # Use host.docker.internal to reach other Kind clusters
                    cluster_peers+="${node_ids[$peer_cluster]}@host.docker.internal:${host_port}"
                fi
            done
            
            if [ -n "$cluster_peers" ]; then
                log_info "Updating peers for $cluster: $cluster_peers"
                
                # Update config.toml
                kubectl --context "kind-${cluster}-cluster" exec -n "$chain_id" \
                    deployment/"$chain_id" -c consumer-daemon -- \
                    sed -i "s/persistent_peers = \".*\"/persistent_peers = \"$cluster_peers\"/" \
                    "/consumer/.${chain_id}/config/config.toml"
                
                # Restart the pod to apply changes
                kubectl --context "kind-${cluster}-cluster" -n "$chain_id" \
                    delete pod -l "app=consumer-chain,chain-id=$chain_id" --grace-period=5
            fi
        fi
    done
}

# Main function
main() {
    log_info "Fixing consumer chain peer connections"
    
    # Get all consumer namespaces
    local consumer_namespaces=$(kubectl get namespaces -l app.kubernetes.io/part-of=consumer-chains --no-headers -o custom-columns=":metadata.name" 2>/dev/null | sort -u)
    
    if [ -z "$consumer_namespaces" ]; then
        log_warn "No consumer chains found"
        exit 0
    fi
    
    # Fix peers for each consumer chain
    for chain_id in $consumer_namespaces; do
        log_info ""
        log_info "Processing consumer chain: $chain_id"
        update_consumer_peers "$chain_id"
    done
    
    # Wait for pods to restart
    log_info ""
    log_info "Waiting for consumer chain pods to restart..."
    sleep 10
    
    # Check peer counts
    log_info ""
    log_info "Checking peer connections:"
    for chain_id in $consumer_namespaces; do
        log_info "Consumer: $chain_id"
        for cluster in alice bob charlie; do
            if kubectl --context "kind-${cluster}-cluster" get namespace "$chain_id" &>/dev/null; then
                # Try to get peer count
                peer_count=$(kubectl --context "kind-${cluster}-cluster" exec -n "$chain_id" \
                    deployment/"$chain_id" -c consumer-daemon -- \
                    interchain-security-cd status --home "/consumer/.${chain_id}" 2>/dev/null | \
                    jq -r '.node_info.network // "unknown"' || echo "error")
                log_info "  $cluster network: $peer_count"
            fi
        done
    done
}

# Run main
main "$@"