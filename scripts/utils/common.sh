#!/usr/bin/env bash
# Common utilities for scripts

# Get the repository root directory
get_repo_root() {
    git rev-parse --show-toplevel 2>/dev/null || {
        # Fallback: go up from script location
        local script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
        cd "$script_dir/../.." && pwd
    }
}

# Export common variables
export REPO_ROOT=$(get_repo_root)
export SCRIPTS_DIR="$REPO_ROOT/scripts"
export ASSETS_DIR=".devnet/assets"

# Default values
export DEFAULT_CHAIN_ID="provider"
export DEFAULT_NODE_URL="http://localhost:26657"
export DEFAULT_KEYRING_BACKEND="test"

# Check if a command exists
command_exists() {
    command -v "$1" >/dev/null 2>&1
}

# Get current Kubernetes context
get_current_context() {
    kubectl config current-context 2>/dev/null || echo "no-context"
}

# Switch to a specific Kind cluster context
switch_to_cluster() {
    local cluster_name="$1"
    kubectl config use-context "kind-${cluster_name}-cluster" >/dev/null 2>&1
}

# Check if a Kind cluster exists
cluster_exists() {
    local cluster_name="$1"
    kind get clusters 2>/dev/null | grep -q "^${cluster_name}-cluster$"
}

# Get validator addresses from a specific cluster
get_validator_address() {
    local validator_name="$1"
    local cluster_name="${2:-$validator_name}"

    switch_to_cluster "$cluster_name"
    kubectl exec -n provider deployment/validator -- \
        interchain-security-pd keys show "$validator_name" \
        --keyring-backend test -a 2>/dev/null
}

# Execute command in validator container
exec_in_validator() {
    local cluster_name="$1"
    shift

    switch_to_cluster "$cluster_name"
    kubectl exec -n provider deployment/validator -- "$@"
}
