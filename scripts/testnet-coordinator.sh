#!/bin/bash

# Testnet Coordinator Script
# 
# This script performs a complete genesis ceremony for a 3-validator testnet.
# It handles three types of keys:
#
# 1. Validator Account Keys (secp256k1):
#    - Derived deterministically from HD paths (m/44'/118'/X'/0/0)
#    - Used for signing transactions (e.g., creating validators, voting)
#    - Stored in the keyring (keyring-test directory)
#
# 2. P2P Node Keys (Ed25519):
#    - Used for P2P networking between validators
#    - Determines the node ID used in peer connections
#    - Stored in config/node_key.json
#    - Has nothing to do with consensus or validation
#
# 3. Consensus Keys (Ed25519):
#    - Used for signing blocks as part of consensus
#    - Stored in config/priv_validator_key.json
#    - This is what makes a node a "validator" in the consensus
#
# Environment Variables:
# - DEBUG_TIMESTAMPS=true  Enable timestamps in log output (default: false)

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# Configuration
CHAIN_ID="provider-1"
DENOM="stake"
BINARY="interchain-security-pd"
ASSETS_DIR="$PROJECT_ROOT/testnet/assets"
KEYS_BACKUP_DIR="$PROJECT_ROOT/testnet/keys-backup"

# Validator names
VALIDATORS=("alice" "bob" "charlie")

# Options for idempotency
FIXED_GENESIS_TIME=""  # Set via command line flag
SAVE_KEYS=false        # Set via command line flag (-s to save keys)

# Source common functions
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common/logging.sh"

# Suppress sonic warnings from the Go binary
export GODEBUG=asyncpreemptoff=1

# Override log_info to include timestamp (using variables from logging.sh)
# Use DEBUG_TIMESTAMPS=true to enable timestamps for debugging
log_info() {
    if [ "${DEBUG_TIMESTAMPS:-false}" = "true" ]; then
        echo -e "${COLOR_GREEN}[$(date +'%Y-%m-%d %H:%M:%S')]${COLOR_RESET} $*"
    else
        echo -e "${COLOR_GREEN}[INFO]${COLOR_RESET} $*"
    fi
}

# error function that exits
error() {
    log_error "$@"
    exit 1
}

# Clean up assets directory
cleanup() {
    log_info "Cleaning up assets directory..."
    rm -rf "$ASSETS_DIR"
    mkdir -p "$ASSETS_DIR"
}

# Generate deterministic mnemonic for each validator
# Using HD paths: we use a single root mnemonic and derive different accounts
get_mnemonic() {
    # Using a single root mnemonic for all validators
    # This is a valid 24-word BIP39 mnemonic for testing
    echo "guard cream sadness conduct invite crumble clock pudding hole grit liar hotel maid produce squeeze return argue turtle know drive eight casino maze host"
}

# Get HD path for validator
get_hd_path() {
    local validator=$1
    case "$validator" in
        alice)
            echo "m/44'/118'/0'/0/0"  # First account
            ;;
        bob)
            echo "m/44'/118'/1'/0/0"  # Second account
            ;;
        charlie)
            echo "m/44'/118'/2'/0/0"  # Third account
            ;;
        *)
            error "Unknown validator: $validator"
            ;;
    esac
}

# Save P2P and consensus Ed25519 keys for future runs
# These are different from validator account keys (which use secp256k1)
# - node_key.json: For P2P networking
# - priv_validator_key.json: For consensus/block signing
# Also saves keyring for idempotency
save_keys() {
    local validator=$1
    local home_dir="$ASSETS_DIR/$validator"

    if [ "$SAVE_KEYS" = true ]; then
        mkdir -p "$KEYS_BACKUP_DIR/$validator"
        cp "$home_dir/config/node_key.json" "$KEYS_BACKUP_DIR/$validator/"
        cp "$home_dir/config/priv_validator_key.json" "$KEYS_BACKUP_DIR/$validator/"
        cp "$home_dir/data/priv_validator_state.json" "$KEYS_BACKUP_DIR/$validator/" 2>/dev/null || true
        
        # Also save keyring for idempotency
        if [ -d "$home_dir/keyring-test" ]; then
            mkdir -p "$KEYS_BACKUP_DIR/$validator/keyring-test"
            cp -r "$home_dir/keyring-test/"* "$KEYS_BACKUP_DIR/$validator/keyring-test/" 2>/dev/null || true
        fi
        
        log_info "  Saved P2P, consensus keys, and keyring for $validator to backup directory"
    fi
}

# Always try to restore P2P and consensus Ed25519 keys from backup (if exists)
# node_key.json: P2P networking key (determines node ID for peer connections)
# priv_validator_key.json: Consensus key for block signing (validator identity)
# Also restores keyring for idempotency
restore_keys() {
    local validator=$1
    local home_dir="$ASSETS_DIR/$validator"

    # Always check if backup exists, regardless of flags
    if [ -d "$KEYS_BACKUP_DIR/$validator" ] && [ -f "$KEYS_BACKUP_DIR/$validator/node_key.json" ]; then
        cp "$KEYS_BACKUP_DIR/$validator/node_key.json" "$home_dir/config/"
        cp "$KEYS_BACKUP_DIR/$validator/priv_validator_key.json" "$home_dir/config/"
        cp "$KEYS_BACKUP_DIR/$validator/priv_validator_state.json" "$home_dir/data/" 2>/dev/null || true
        
        # Also restore keyring if exists
        if [ -d "$KEYS_BACKUP_DIR/$validator/keyring-test" ]; then
            mkdir -p "$home_dir/keyring-test"
            cp -r "$KEYS_BACKUP_DIR/$validator/keyring-test/"* "$home_dir/keyring-test/" 2>/dev/null || true
        fi
        
        log_info "  Restored P2P, consensus keys, and keyring for $validator from backup"
        return 0
    fi
    return 1
}

# Initialize validator home directory
init_validator() {
    local validator=$1
    local home_dir="$ASSETS_DIR/$validator"

    log_info "Initializing $validator..."

    # Initialize chain
    "$BINARY" init "$validator" --chain-id "$CHAIN_ID" --home "$home_dir" 2>/dev/null

    # Try to restore keys from backup, otherwise they'll be generated
    if ! restore_keys "$validator"; then
        log_info "  Using newly generated P2P and consensus keys"
    fi

    # Check if keyring already exists
    if [ -d "$home_dir/keyring-test" ] && [ -n "$(ls -A "$home_dir/keyring-test" 2>/dev/null)" ]; then
        log_info "  Using existing keyring for $validator"
    else
        # Generate deterministic keys using mnemonic and HD path
        local mnemonic hd_path
        mnemonic=$(get_mnemonic)
        hd_path=$(get_hd_path "$validator")

        # Add validator account key from mnemonic with specific HD path
        # This is the account key used for transactions, different from consensus keys
        echo "$mnemonic" | "$BINARY" keys add "$validator" \
            --home "$home_dir" \
            --keyring-backend test \
            --recover \
            --hd-path "$hd_path" \
            --output json > "$home_dir/key_info.json" 2>&1 || {
            error "Failed to add validator account key for $validator. Check mnemonic format."
        }
    fi

    # Get validator address
    local address
    address=$("$BINARY" keys show "$validator" --home "$home_dir" --keyring-backend test -a)
    echo "$address" > "$home_dir/address"

    log_info "  Validator account address: $address"

    # Store validator info
    echo "$validator:$address" >> "$ASSETS_DIR/validators.txt"

    # Set fixed genesis time if specified
    if [ -n "$FIXED_GENESIS_TIME" ]; then
        jq --arg time "$FIXED_GENESIS_TIME" '.genesis_time = $time' "$home_dir/config/genesis.json" > "$home_dir/config/genesis.json.tmp"
        mv "$home_dir/config/genesis.json.tmp" "$home_dir/config/genesis.json"
    fi

    # Save keys for future runs
    save_keys "$validator"
}

# Create provisional genesis
create_provisional_genesis() {
    log_info "Creating provisional genesis..."

    local genesis_home="$ASSETS_DIR/alice"
    local genesis_file="$genesis_home/config/genesis.json"

    # Add all validator accounts to genesis
    while IFS=: read -r validator address; do
        log_info "  Adding genesis account for $validator ($address)..."
        "$BINARY" genesis add-genesis-account "$address" "300000000000000000000$DENOM" \
            --home "$genesis_home" \
            --keyring-backend test
    done < "$ASSETS_DIR/validators.txt"

    # Add Hermes relayer account to genesis with funds for IBC transactions
    local HERMES_ADDRESS="cosmos1r5v5srda7xfth3hn2s26txvrcrntldjumt8mhl"
    log_info "  Adding genesis account for Hermes relayer ($HERMES_ADDRESS)..."
    "$BINARY" genesis add-genesis-account "$HERMES_ADDRESS" "100000000000000000000$DENOM" \
        --home "$genesis_home" \
        --keyring-backend test

    # Copy provisional genesis to all validators
    for validator in "${VALIDATORS[@]}"; do
        if [ "$validator" != "alice" ]; then
            cp "$genesis_file" "$ASSETS_DIR/$validator/config/genesis.json"
        fi
    done

    log_info "Provisional genesis created"
}

# Create gentx for each validator
create_gentx() {
    local validator=$1
    local home_dir="$ASSETS_DIR/$validator"

    log_info "Creating gentx for $validator..."

    # Create validator transaction
    "$BINARY" genesis gentx "$validator" "30000000000000000000$DENOM" \
        --chain-id "$CHAIN_ID" \
        --home "$home_dir" \
        --keyring-backend test \
        --moniker "$validator"

    # Copy gentx to coordinator (alice)
    if [ "$validator" != "alice" ]; then
        cp "$home_dir/config/gentx/"*.json "$ASSETS_DIR/alice/config/gentx/"
    fi
}

# Collect all gentxs and create final genesis
collect_gentxs() {
    log_info "Collecting gentxs..."

    local genesis_home="$ASSETS_DIR/alice"

    # Collect all gentx files
    "$BINARY" genesis collect-gentxs --home "$genesis_home"

    # Validate genesis
    "$BINARY" genesis validate --home "$genesis_home"

    log_info "Final genesis created"
}

# Distribute final genesis to all validators
distribute_genesis() {
    log_info "Distributing final genesis..."

    local final_genesis="$ASSETS_DIR/alice/config/genesis.json"

    for validator in "${VALIDATORS[@]}"; do
        if [ "$validator" != "alice" ]; then
            cp "$final_genesis" "$ASSETS_DIR/$validator/config/genesis.json"
        fi
    done

    log_info "Genesis distributed to all validators"
}

# Configure P2P peers
configure_peers() {
    log_info "Configuring P2P peers..."

    # Get node IDs and save to file
    for validator in "${VALIDATORS[@]}"; do
        local home_dir="$ASSETS_DIR/$validator"
        local node_id
        node_id=$("$BINARY" tendermint show-node-id --home "$home_dir")
        echo "$validator:$node_id" >> "$ASSETS_DIR/node_ids.txt"
    done

    # Configure persistent peers for each validator
    for validator in "${VALIDATORS[@]}"; do
        local home_dir="$ASSETS_DIR/$validator"
        local config_file="$home_dir/config/config.toml"
        local peers=""

        # Build peer list (exclude self)
        while IFS=: read -r peer_name peer_id; do
            if [ "$peer_name" != "$validator" ]; then
                if [ -n "$peers" ]; then
                    peers+=","
                fi
                peers+="${peer_id}@${peer_name}:26656"
            fi
        done < "$ASSETS_DIR/node_ids.txt"

        # Update config
        sed -i.bak "s/persistent_peers = \"\"/persistent_peers = \"$peers\"/" "$config_file"
        rm -f "$config_file.bak"

        log_info "  $validator peers: $peers"
    done
}

# Create summary file
create_summary() {
    log_info "Creating summary..."

    cat > "$ASSETS_DIR/summary.txt" << EOF
=== Testnet Configuration Summary ===

Chain ID: $CHAIN_ID
Denom: $DENOM
Genesis Time: $(jq -r '.genesis_time' "$ASSETS_DIR/alice/config/genesis.json")
Keys Saved: $SAVE_KEYS

Validators:
EOF

    while IFS=: read -r validator address; do
        echo "  $validator: $address" >> "$ASSETS_DIR/summary.txt"
    done < "$ASSETS_DIR/validators.txt"

    echo "" >> "$ASSETS_DIR/summary.txt"
    echo "Node IDs:" >> "$ASSETS_DIR/summary.txt"

    while IFS=: read -r validator node_id; do
        echo "  $validator: $node_id" >> "$ASSETS_DIR/summary.txt"
    done < "$ASSETS_DIR/node_ids.txt"

    echo "" >> "$ASSETS_DIR/summary.txt"
    echo "Home directories created in: $ASSETS_DIR" >> "$ASSETS_DIR/summary.txt"

    log_info "Summary written to $ASSETS_DIR/summary.txt"
}

# Show usage
usage() {
    echo "Usage: $0 [OPTIONS]"
    echo ""
    echo "Options:"
    echo "  -t, --time TIME         Use fixed genesis time (e.g., '2025-01-01T00:00:00Z')"
    echo "  -s, --save-keys         Save node/consensus keys to backup for future runs"
    echo "  -h, --help              Show this help message"
    echo ""
    echo "Examples:"
    echo "  $0                      # Use current time, reuse existing keys if found"
    echo "  $0 -s                   # Always use -s for consistent keys across runs"
    echo "  $0 -t '2025-01-01T00:00:00Z' -s  # Fixed time and consistent keys (fully idempotent)"
    echo ""
    echo "Note: Node/consensus keys are always restored from backup if available. Using -s repeatedly is safe."
    echo ""
    echo "Key Types:"
    echo "  - Validator account keys (secp256k1): Derived from HD paths, used for transactions"
    echo "  - P2P Node keys (Ed25519): Used for P2P networking, determines node ID"
    echo "  - Consensus keys (Ed25519): Used for block signing as a validator"
    exit 0
}

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        -t|--time)
            FIXED_GENESIS_TIME="$2"
            shift 2
            ;;
        -s|--save-keys)
            SAVE_KEYS=true
            shift
            ;;
        -h|--help)
            usage
            ;;
        *)
            error "Unknown option: $1"
            ;;
    esac
done

# Main execution
main() {
    log_info "Starting 3-validator testnet genesis ceremony..."

    if [ -n "$FIXED_GENESIS_TIME" ]; then
        log_info "Using fixed genesis time: $FIXED_GENESIS_TIME"
    fi

    if [ "$SAVE_KEYS" = true ]; then
        log_info "Node/consensus key saving enabled - will be saved to backup"
    else
        log_info "Node/consensus key saving disabled - using existing backup if available"
    fi

    # Step 1: Clean up
    cleanup

    # Step 2: Initialize all validators
    for validator in "${VALIDATORS[@]}"; do
        init_validator "$validator"
    done

    # Step 3: Create provisional genesis with all accounts
    create_provisional_genesis

    # Step 4: Create gentx for each validator
    for validator in "${VALIDATORS[@]}"; do
        create_gentx "$validator"
    done

    # Step 5: Collect gentxs to create final genesis
    collect_gentxs

    # Step 6: Distribute final genesis to all validators
    distribute_genesis

    # Step 7: Configure P2P peers
    configure_peers

    # Step 8: Create summary
    create_summary

    log_info "Genesis ceremony complete!"
    log_info ""
    log_info "Validator home directories created in: $ASSETS_DIR"
    log_info "Each validator directory contains:"
    log_info "  - Initialized chain configuration"
    log_info "  - Validator account keys (deterministic from HD paths)"
    log_info "  - Node/consensus keys (Ed25519 for P2P and block signing)"
    log_info "  - Final genesis.json with all validators"
    log_info "  - Configured persistent peers"
    log_info ""
    log_info "You can now use these directories to start the validators."

    if [ "$SAVE_KEYS" = true ]; then
        log_info ""
        log_info "Node/consensus keys have been saved to: $KEYS_BACKUP_DIR"
        log_info "These keys will be automatically reused in future runs for consistent node IDs"
    fi
}

# Run main function
main
