# Consumer Key Management

## Overview

In Interchain Security (ICS), consumer chains need validator keys to produce blocks. There are two approaches:

1. **Use Provider Keys**: Consumer chains use the same validator keys as the provider chain (security risk)
2. **Assign Consumer Keys**: Validators assign dedicated keys for each consumer chain (recommended)

## Current Implementation Status

### What's Working
- Consumer chain creation and deployment
- Phase detection and monitoring
- Deterministic peer discovery
- Genesis time synchronization

### What's Missing
- Consumer chains cannot produce blocks because they don't have valid validator keys
- The validator keys in the CCV genesis don't match the keys being used by the nodes

## Recommended Solution: Consumer Key Assignment

### Why Consumer Keys?

1. **Security**: Provider chain validator keys remain secure
2. **Isolation**: Compromise of a consumer chain doesn't affect the provider
3. **Flexibility**: Different keys can be used for different consumer chains
4. **Standards**: This is how ICS is designed to work

### Implementation Steps

1. **During Consumer Chain Creation**:
   ```bash
   # Create consumer chain
   ./scripts/lifecycle/create-consumer.sh -c testchain1 -s 30
   ```

2. **Before Chain Launch** (within spawn time):
   ```bash
   # Validators assign consumer keys
   ./scripts/assign-consumer-key.sh -c 0 -v alice
   ./scripts/assign-consumer-key.sh -c 0 -v bob
   ./scripts/assign-consumer-key.sh -c 0 -v charlie
   ```

3. **Monitor Detects Assignment**:
   - Monitors watch for `assign_consensus_key` events
   - Store the assigned keys for deployment

4. **During Deployment**:
   - Monitors inject the assigned consumer keys into deployments
   - Consumer chains use these keys instead of generating new ones

### Technical Details

The consumer key assignment creates a mapping:
- Provider Validator → Consumer Chain → Consumer Key

When a consumer chain starts:
1. It receives the assigned validator key via Kubernetes secret
2. Uses this key as its `priv_validator_key.json`
3. Can now sign blocks that match the validator set in genesis

## Alternative: Test Environment Only

For testing, you could:
1. Copy provider validator keys to consumer chains (NOT for production)
2. Use pre-generated test keys
3. Run in single-validator mode

## Next Steps

To make consumer chains produce blocks:

1. **Short term**: Implement consumer key assignment workflow
2. **Medium term**: Automate key assignment during opt-in
3. **Long term**: Implement key rotation and management UI

## Security Considerations

- Never copy provider validator keys to consumer chains in production
- Use Kubernetes RBAC to protect consumer keys
- Implement key rotation capabilities
- Monitor for key compromise
- Have incident response procedures