# Current State Summary

## What's Working ✅

1. **Consumer Chain Creation**
   - Script: `./scripts/lifecycle/create-consumer.sh -c testchain1 -s 30`
   - Creates consumer chain with proper spawn time
   - Validators can opt-in

2. **Phase Detection and Monitoring**
   - Monitors detect phase transitions (REGISTERED → INITIALIZED → LAUNCHED)
   - Event handling for create_consumer and opt_in events
   - Polling for phase changes

3. **Deployment Automation**
   - Consumer chains deploy automatically when entering LAUNCHED phase
   - Proper namespace creation: `<validator>-<chain-id>`
   - Hermes relayer deployment alongside consumer chain

4. **Peer Discovery**
   - Deterministic node key generation
   - Predictable peer addresses
   - Consumer chains can connect to each other

5. **Genesis Synchronization**
   - Using spawn time as genesis time
   - All validators get identical genesis files
   - Python merge script properly handles CCV patches

## What's Not Working ❌

1. **Block Production**
   - Consumer chains stuck at height 1
   - Validators keys don't match genesis validator set
   - Nodes report "This node is not a validator"

## Root Cause

Consumer chains are using self-generated validator keys instead of:
- Provider chain validator keys (not recommended for security)
- Assigned consumer keys (recommended approach)

## Next Steps

1. **Implement Consumer Key Assignment**
   ```bash
   # Assign consumer keys before chain launches
   ./scripts/assign-consumer-key.sh -c 0 -v alice
   ./scripts/assign-consumer-key.sh -c 0 -v bob
   ./scripts/assign-consumer-key.sh -c 0 -v charlie
   ```

2. **Update Monitor to Handle Key Assignments**
   - Watch for assign_consensus_key events
   - Store assigned keys in Kubernetes secrets
   - Inject keys into consumer chain deployments

3. **Modify Deployment to Use Assigned Keys**
   - Mount validator keys from secrets
   - Use assigned keys as priv_validator_key.json
   - Ensure keys match genesis validator set

## Quick Debugging Commands

```bash
# Check consumer chain namespaces
kubectl get namespaces | grep testchain

# View consumer chain logs
kubectl logs -n bob-testchain1-0 -l app=testchain1-0 --tail=50

# Check validator set in logs
kubectl logs -n bob-testchain1-0 -l app=testchain1-0 | grep -i validator

# See current height
kubectl logs -n bob-testchain1-0 -l app=testchain1-0 | grep "height="

# Check peer connections
kubectl logs -n bob-testchain1-0 -l app=testchain1-0 | grep "numInPeers"
```

## Architecture Reminders

1. **Validator Selection**: Not all validators deploy every consumer chain (deterministic selection)
2. **Namespace Pattern**: `<validator>-<chain-id>` for each deployment
3. **Key Requirement**: Consumer chains MUST have validator keys that match genesis
4. **Genesis Source**: Spawn time from chain ensures consistency