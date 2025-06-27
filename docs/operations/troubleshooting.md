# Troubleshooting Guide

This guide consolidates solutions to common issues encountered when operating the Interchain Security Monitor system.

## Table of Contents

- [Quick Diagnostics](#quick-diagnostics)
- [Deployment Issues](#deployment-issues)
- [Consumer Chain Issues](#consumer-chain-issues)
- [Networking Problems](#networking-problems)
- [Consensus and Block Production](#consensus-and-block-production)
- [Hermes Relayer Issues](#hermes-relayer-issues)
- [Monitor Issues](#monitor-issues)
- [Recovery Procedures](#recovery-procedures)

## Quick Diagnostics

### Status Check Commands

```bash
# Overall system status
make status

# Detailed deployment status
make status-verbose

# Consumer chain status
make consumer-status CONSUMER_ID=0

# Check logs for errors
make logs TARGET=monitor | grep ERROR

# Monitor CCV events
make monitor-events
```

### Common Error Patterns

| Error Pattern | Likely Cause | Quick Fix |
|--------------|--------------|-----------|
| `consumer chain is not in its launched phase` | Trying to remove non-LAUNCHED chain | Force launch first |
| `validator key not found` | Monitor key mismatch | Check keyring import |
| `no such host` | DNS/networking issue | Verify service names |
| `genesis time already passed` | Stale genesis | Update spawn time |
| `invalid client: 07-tendermint-1` | Wrong IBC client | Use client-0 |

## Deployment Issues

### Monitor Won't Start

**Symptoms**: Monitor pod in CrashLoopBackOff or Error state

**Diagnosis**:

```bash
# Check pod status
kubectl -n provider get pods -l component=monitor

# View logs
kubectl -n provider logs deployment/monitor

# Describe pod for events
kubectl -n provider describe pod -l component=monitor
```

**Common Causes**:

1. **Missing keyring**: Keyring ConfigMap not created

   ```bash
   # Fix: Ensure keyring exists
   kubectl -n provider get configmap alice-keyring
   ```

2. **RPC connection failure**: Can't connect to validator

   ```bash
   # Fix: Check validator is running
   kubectl -n provider get pods -l role=validator
   ```

3. **Permission issues**: Can't create consumer namespaces

   ```bash
   # Fix: Check RBAC permissions
   kubectl auth can-i create namespace --as=system:serviceaccount:provider:monitor
   ```

### Consumer Chain Won't Deploy

**Symptoms**: No consumer chain resources created after LAUNCHED phase

**Diagnosis**:

```bash
# Check if monitor detected LAUNCHED
kubectl -n provider logs deployment/monitor | grep "LAUNCHED.*consumer-0"

# List consumer namespaces
kubectl get namespaces -l app.kubernetes.io/part-of=consumer-chains
```

**Solutions**:

1. **Phase detection failure**:

   ```bash
   # Force phase check
   make show-consumer CONSUMER_ID=0
   ```

2. **Kubernetes permissions**:

   ```bash
   # Verify monitor can create resources
   kubectl -n provider logs deployment/monitor | grep "permission denied"
   ```

## Consumer Chain Issues

### Consumer Stuck in REGISTERED Phase

**Symptoms**: Consumer won't transition to INITIALIZED

**Diagnosis**:

```bash
# Check consumer details
./scripts/lifecycle/list-consumers.sh -d 0

# Look for missing parameters (especially spawn_time)
```

**Solution**:

```bash
# Update with all required parameters
./scripts/lifecycle/update-consumer.sh 0 -d 120

# Verify transition
make show-consumer CONSUMER_ID=0
```

### Consumer Reverts from INITIALIZED to REGISTERED

**Symptoms**: Consumer was INITIALIZED but reverted after spawn time

**Root Cause**: No active validators opted in before spawn time

**Solution**:

```bash
# Option 1: Force launch with all validators
./scripts/lifecycle/force-launch-consumer.sh 0 -s 30

# Option 2: Manual recovery
# Set new spawn time
./scripts/lifecycle/update-consumer.sh 0 -d 60

# Opt in validators
for val in alice bob charlie; do
    kubectl -n provider exec deployment/$val -- \
        interchain-security-pd tx provider opt-in 0 \
        --from $val --yes
done
```

### Consumer Chain Not Producing Blocks

**Symptoms**: Chain deployed but height stays at 0

**Diagnosis**:

```bash
# Check block height
make consumer-height CHAIN_ID=testchain1-0

# View consumer logs
make consumer-logs CHAIN_ID=testchain1-0

# Check validator connections
kubectl -n bob-testchain1-0 logs deployment/testchain1-0 | grep "peer"
```

**Common Causes**:

1. **Missing CCV genesis patch**:

   ```bash
   # Check if ccvconsumer section exists
   kubectl -n alice-testchain1-0 exec deployment/testchain1-0 -- \
       interchain-security-cd query ccvconsumer params
   ```

2. **Peer connectivity issues**:

   ```bash
   # Fix: Update persistent_peers manually
   ./scripts/helpers/update-consumer-peers.sh testchain1-0
   ```

3. **Validator key mismatch**:

   ```bash
   # Verify assigned keys match genesis
   make show-consumer-keys CONSUMER_ID=0
   ```

## Networking Problems

### Pods Can't Resolve Service Names

**Symptoms**: Errors like "no such host" in logs

**Diagnosis**:

```bash
# Test DNS resolution
kubectl -n provider exec deployment/alice -- nslookup bob

# Check service exists
kubectl -n provider get svc
```

**Solution**:

```bash
# Restart CoreDNS if needed
kubectl -n kube-system rollout restart deployment/coredns

# Verify services have endpoints
kubectl -n provider get endpoints
```

### Consumer Chains Can't Connect to Peers

**Symptoms**: "Failed to connect to peer" errors

**Root Cause**: Incorrect persistent_peers configuration

**Solution**:

```bash
# Generate correct peer list
./scripts/peer-discovery.sh testchain1-0

# Update consumer configs
for validator in alice bob charlie; do
    kubectl -n $validator-testchain1-0 exec deployment/testchain1-0 -- \
        sed -i 's/persistent_peers = .*/persistent_peers = "PEER_LIST"/' \
        /chain/.consumer/config/config.toml
done

# Restart consumers
for validator in alice bob charlie; do
    kubectl -n $validator-testchain1-0 rollout restart deployment/testchain1-0
done
```

## Consensus and Block Production

### Genesis Time Issues

**Symptoms**: "Genesis time already passed" errors

**Solution**:

```bash
# Use spawn time as authoritative source
# Genesis time is set to spawn_time in consumer deployment

# For stuck chains, update spawn time
./scripts/lifecycle/update-consumer.sh 0 -d 60
```

### Validator Key Mismatches

**Symptoms**: Validators not recognized in consumer chain

**Diagnosis**:

```bash
# Compare assigned keys with genesis
make show-consumer-keys CONSUMER_ID=0

# Check genesis validators
kubectl -n alice-testchain1-0 exec deployment/testchain1-0 -- \
    interchain-security-cd query ccvconsumer initial-validators
```

**Solution**: Ensure CCV patch is properly applied with assigned keys

## Hermes Relayer Issues

### Hermes Can't Create Channel

**Symptoms**: "channel must be built on top of client: 07-tendermint-0"

**Root Cause**: Hermes creates new client instead of using genesis client

**Solution**:

```bash
# Hermes should detect and use existing client-0
# Ensure Hermes starts after consumer chain is running

# Verify client exists
kubectl -n alice-testchain1-0 exec deployment/hermes -- \
    hermes query clients --host-chain provider-1
```

### Hermes Account Not Funded

**Symptoms**: "insufficient funds" errors

**Diagnosis**:

```bash
# Check provider account
kubectl -n provider exec deployment/alice -- \
    interchain-security-pd query bank balances [hermes-address]

# Check consumer account
kubectl -n alice-testchain1-0 exec deployment/testchain1-0 -- \
    interchain-security-cd query bank balances [hermes-address]
```

**Solution**:

```bash
# Fund on provider (if needed)
kubectl -n provider exec deployment/bob -- \
    interchain-security-pd tx bank send bob [hermes-address] 100000000stake \
    --from bob --yes

# Consumer should be pre-funded via genesis
```

## Monitor Issues

### Monitor Not Detecting Events

**Symptoms**: Monitor doesn't react to consumer chain creation

**Diagnosis**:

```bash
# Check WebSocket connection
kubectl -n provider logs deployment/monitor | grep "websocket"

# Verify event subscription
kubectl -n provider logs deployment/monitor | grep "subscribe"
```

**Solution**:

```bash
# Restart monitor to reconnect
kubectl -n provider rollout restart deployment/monitor

# Check validator RPC endpoints
for val in alice bob charlie; do
    kubectl -n provider exec deployment/monitor -- \
        curl -s http://$val:26657/status | jq .result.sync_info
done
```

### Monitor Creates Resources in Wrong Namespace

**Symptoms**: Resources created in unexpected namespaces

**Root Cause**: Namespace calculation mismatch

**Solution**: Ensure consistent namespace pattern:

```go
namespace := fmt.Sprintf("%s-%s", validatorName, chainID)
```

## Recovery Procedures

### Complete System Reset

```bash
# Clean everything
make clean

# Redeploy
make deploy
```

### Reset Monitors Only

```bash
# Full reset and redeploy
make reset
```

### Remove Stuck Consumer

```bash
# Force to LAUNCHED then remove
./scripts/lifecycle/force-launch-consumer.sh 0 -s 10
sleep 15
./scripts/lifecycle/remove-consumer.sh 0
```

### Recover from Failed Deployment

```bash
# Delete consumer namespace
kubectl delete namespace alice-testchain1-0 --force --grace-period=0

# Remove finalizers if stuck
kubectl patch namespace alice-testchain1-0 -p '{"metadata":{"finalizers":null}}'

# Clean monitor state
kubectl -n provider rollout restart deployment/monitor
```

## Prevention Tips

1. **Always use Makefile commands** instead of raw kubectl/scripts
2. **Check phase before operations** that require specific phases
3. **Allow adequate spawn time** (60-120 seconds) for opt-ins
4. **Monitor logs continuously** during operations
5. **Keep one deployment** - avoid multiple conflicting deployments
6. **Clean up test chains** after experiments

## Getting Help

If these solutions don't resolve your issue:

1. Collect diagnostics:

   ```bash
   make status-verbose > diagnostics.txt
   kubectl -n provider logs deployment/monitor --tail=1000 >> diagnostics.txt
   ```

2. Check monitor version:

   ```bash
   kubectl -n provider exec deployment/monitor -- monitor version
   ```

3. Report issue with diagnostics at: <https://github.com/your-org/monitor/issues>
