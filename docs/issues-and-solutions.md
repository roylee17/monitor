# Issues and Solutions Summary

## Overview
This document summarizes the key issues encountered while implementing ICS consumer chain deployment and monitoring, along with their solutions.

## Issue 1: Consumer Chains Not Producing Blocks - Peer Connectivity

### Problem
- Consumer chains couldn't connect to each other
- Static peer lists were invalid because consumer chains generate unique node IDs at startup
- Provider chain node IDs didn't match consumer chain node IDs

### Root Cause
Each consumer chain generates its own node ID at startup, making pre-configured static peer lists useless.

### Solution
Implemented deterministic node key generation:
- Generate predictable node IDs using `sha256(validator_name + chain_id)`
- Each validator gets the same node ID for a given consumer chain across restarts
- Peers can be calculated ahead of time

**Files Changed**:
- `internal/subnet/node_key_generator.go` - Generates deterministic node keys
- `internal/subnet/deterministic_peer_discovery.go` - Calculates peer addresses
- `internal/deployment/k8s_deployer.go` - Uses deterministic node keys in deployment

## Issue 2: Genesis Time Synchronization

### Problem
- Consumer chains had different genesis.json files across validators
- Different SHA256 hashes prevented consensus
- Genesis files were fetched at slightly different times

### Root Cause
Each validator was fetching the CCV genesis patch independently, and the provider chain was including the current timestamp, resulting in different genesis times.

### Solution
Use the consumer chain's spawn time as the authoritative genesis time:
```go
// Get the spawn time from the consumer chain data to use as genesis time
consumerInfo, err := h.blockchainState.GetConsumerInfo(ctx, consumerID)
if err == nil && !consumerInfo.SpawnTime.IsZero() {
    genesisTime := consumerInfo.SpawnTime.Format(time.RFC3339)
    wrappedGenesis["genesis_time"] = genesisTime
}
```

**Key Insight**: "Why don't we use the on chain spawntime?" - This ensures all validators have identical genesis files.

## Issue 3: Python Script Variable Scope Error

### Problem
- Consumer chain startup failed with: `NameError: name 'ccv_data' is not defined`
- Python merge script had a variable scope issue

### Root Cause
The `ccv_data` variable was defined inside an if block but used outside of it.

### Solution
Initialize the variable before the if block:
```python
# Initialize ccv_data
ccv_data = None

# Then use it in the if block
if 'ccvconsumer' in patch['app_state']:
    ccv_data = patch['app_state']['ccvconsumer']
```

**Files Changed**:
- `internal/deployment/k8s_deployer.go` - Fixed Python script in startup script

## Issue 4: Consumer Chain Namespace Confusion

### Problem
- Initially looked for consumer chain pods in `consumer-chains` namespace
- Couldn't find any deployed consumer chains

### Root Cause
Misunderstanding of the deployment architecture.

### Solution
Consumer chains are deployed in their own namespaces following the pattern: `<validator>-<chain-id>`
- Example: `bob-testchain1-0`, `charlie-testchain1-0`
- Each namespace contains the consumer chain pod and Hermes relayer

**Key Learning**: Consumer chains have isolated namespaces per validator for better security and resource management.

## Issue 5: Consumer Chains Stuck at Height 1 (Current Issue)

### Problem
- Consumer chains start but don't produce blocks
- Stuck at height 1 with "node is not a validator" messages
- Validator addresses in genesis don't match node addresses

### Root Cause
Consumer chains are generating their own validator keys instead of using:
1. The provider chain's validator keys, OR
2. Assigned consumer keys via `MsgAssignConsumerKey`

The validator public keys in the CCV genesis (from provider chain) don't match the private keys being used by the consumer chain nodes.

### Proposed Solution
Implement consumer key assignment workflow:
1. Generate dedicated keys for consumer chains
2. Validators assign these keys using `assign-consensus-key` transaction
3. Monitors detect assignments and inject keys into deployments
4. Consumer chains use assigned keys to sign blocks

**Files Created**:
- `internal/deployment/consumer_key_manager.go` - Manages consumer key generation
- `scripts/assign-consumer-key.sh` - Script to assign consumer keys
- `docs/consumer-key-management.md` - Documentation

## Key Architectural Insights

1. **Stateless Peer Discovery**: Consumer chains can't use static peer lists due to dynamic node ID generation
2. **Deterministic Configuration**: Use deterministic methods (hashing, spawn time) to ensure consistency across validators
3. **Namespace Isolation**: Each validator's consumer chain runs in its own namespace
4. **Key Separation**: Consumer chains should use dedicated keys, not provider validator keys
5. **Event-Driven Monitoring**: Monitors react to blockchain events for deployment decisions

## Common Patterns

1. **Deterministic Generation**: When something needs to be consistent across validators, generate it deterministically from chain data
2. **On-Chain Source of Truth**: Use on-chain data (like spawn time) as the authoritative source
3. **Proper Error Handling**: Always initialize variables and handle edge cases
4. **Security First**: Never copy main validator keys; use dedicated consumer keys

## Debugging Tips

1. Check the right namespace: `kubectl get namespaces | grep <chain-id>`
2. Look for validator selection: Not all validators are selected for every chain
3. Verify genesis consistency: All validators must have identical genesis files
4. Check peer connectivity: Consumer chains need to connect to each other
5. Validate key matching: Consumer chain keys must match genesis validator set