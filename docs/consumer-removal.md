# Consumer Chain Removal Guide

This guide explains how to remove consumer chains in ICS v7.

## Overview

In ICS v7, consumer chains follow a lifecycle with distinct phases:

1. **REGISTERED** - Chain has been created with a unique consumer ID
2. **INITIALIZED** - Chain has spawn time set and is ready to launch
3. **LAUNCHED** - Chain is running and consuming validator set
4. **STOPPED** - Chain has been removed but state not yet deleted
5. **DELETED** - Chain state has been completely removed

## Removal Process

### Prerequisites

- The consumer chain must be in the **LAUNCHED** phase
- Only the **owner** of the chain can remove it
- The removal is a two-step process:
  1. Immediate transition to **STOPPED** phase
  2. Deletion after the unbonding period expires

### Using the Remove Script

```bash
# Remove a consumer chain
./scripts/remove-consumer.sh -c <consumer-id>

# Examples
./scripts/remove-consumer.sh -c consumer1
./scripts/remove-consumer.sh -c testchain1
```

### What Happens During Removal

1. **Phase Transition**: The chain immediately transitions from LAUNCHED to STOPPED
2. **Stop Validation**: Validators are no longer required to validate the chain
3. **Channel Closure**: IBC channels are closed
4. **State Cleanup**: After the unbonding period (~21 days on mainnet), the chain state is deleted

### Listing Consumer Chains

To see all consumer chains and their current phases:

```bash
# Basic listing
./scripts/list-consumers.sh

# Verbose listing with additional details
./scripts/list-consumers.sh -v
```

Output example:
```
CONSUMER ID     CHAIN ID             PHASE           OWNER
--------------- -------------------- --------------- ---------------------------------------------
consumer1       testchain1           LAUNCHED        cosmos1abc...
consumer2       testchain2           STOPPED         cosmos1def...
consumer3       testchain3           REGISTERED      cosmos1ghi...
```

### Manual Removal via CLI

If you need to remove a chain manually:

```bash
# From inside the validator pod
interchain-security-pd tx provider remove-consumer <consumer-id> \
  --from <owner-key> \
  --chain-id provider \
  --home /chain/.provider \
  --node tcp://localhost:26657 \
  --keyring-backend test \
  --gas auto \
  --gas-adjustment 1.5 \
  --fees 1000000stake \
  -y
```

### Verification

After removal, verify the chain status:

```bash
# Query specific consumer
interchain-security-pd query provider consumer <consumer-id> \
  --node tcp://localhost:26657 \
  -o json

# Check the phase field - should show CONSUMER_PHASE_STOPPED
```

## Important Notes

1. **Ownership**: Only the address that created the chain can remove it
2. **Phase Requirements**: Chains can only be removed when in LAUNCHED phase
3. **Immediate Effect**: Validators stop validating immediately upon removal
4. **Delayed Deletion**: Chain state persists until unbonding period expires
5. **No Reversal**: Once removed, a chain cannot be restarted with the same consumer ID

## Troubleshooting

### Common Errors

1. **"expected owner address X, got Y"**
   - You're trying to remove a chain with the wrong account
   - Check the owner with `list-consumers.sh`

2. **"chain with consumer id: X has to be in its launched phase"**
   - The chain is not in LAUNCHED phase
   - Only launched chains can be removed

3. **"cannot find consumer chain"**
   - The consumer ID doesn't exist
   - Use `list-consumers.sh` to see valid consumer IDs

### Monitor Integration

The monitor will automatically detect and report phase transitions:

- LAUNCHED → STOPPED: Reported immediately
- STOPPED → DELETED: Reported after unbonding period

The monitor continues to track stopped chains until they are fully deleted.

## Security Considerations

1. **Owner Verification**: The system verifies ownership before allowing removal
2. **Unbonding Protection**: The delayed deletion ensures proper unbonding
3. **No Force Removal**: There's no way to bypass the unbonding period

## Related Commands

- `create-consumer.sh` - Create new consumer chains
- `list-consumers.sh` - List all consumer chains
- `update-consumer.sh` - Update consumer chain parameters (coming soon)