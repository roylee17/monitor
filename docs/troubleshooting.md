# Troubleshooting Guide

This guide consolidates solutions to common issues encountered when operating the Interchain Security Monitor system.

## Table of Contents

- [Quick Diagnostics](#quick-diagnostics)
- [Deployment Issues](#deployment-issues)
- [LoadBalancer and Networking Issues](#loadbalancer-and-networking-issues)
- [Consumer Chain Issues](#consumer-chain-issues)
- [Key Management Issues](#key-management-issues)
- [Hermes Relayer Issues](#hermes-relayer-issues)
- [Monitor Issues](#monitor-issues)
- [Port Allocation Issues](#port-allocation-issues)
- [Recovery Procedures](#recovery-procedures)

## Quick Diagnostics

### Status Check Commands

```bash
# Overall system status
make status

# Consumer chain information
make consumer-info CONSUMER_ID=0

# Show all consumer chains
make show-consumer CONSUMER_ID=0

# Check monitor logs
kubectl -n provider logs monitor -f

# Check validator logs
kubectl -n provider logs validator -f
```

### Common Error Patterns

| Error Pattern | Likely Cause | Quick Fix |
|--------------|--------------|-----------|
| `consumer chain is not in its launched phase` | Trying to remove non-LAUNCHED chain | Wait for LAUNCHED phase |
| `validator key not found` | Monitor key import failed | Re-import keys with `monitor keys add` |
| `no such host` | DNS/networking issue | Check service names and endpoints |
| `LoadBalancer IP pending` | MetalLB not installed (Kind) | Run `./scripts/clusters/install-metallb.sh` |
| `peer connection failed` | Wrong LoadBalancer endpoint | Register validator endpoints |
| `CCV patch update failed` | Consumer key assignment issue | Check monitor logs for key format |
| `port already in use` | Port allocation conflict | Check port calculation logic |

## Deployment Issues

### Monitor Won't Start

**Symptoms**: Monitor pod in CrashLoopBackOff or Error state

**Diagnosis**:

```bash
# Check pod status
kubectl -n provider get pods -l app.kubernetes.io/component=monitor

# View logs
kubectl -n provider logs monitor --previous

# Describe pod for events
kubectl -n provider describe pod monitor-xxxxx
```

**Common Causes**:

1. **Key import failure**: Monitor can't import validator key

   ```bash
   # Check if key was imported
   kubectl -n provider exec monitor -- monitor keys list

   # Re-import key manually
   kubectl -n provider exec -it monitor -- monitor keys add alice \
     --recover --keyring-backend test
   ```

2. **RPC connection failure**: Can't connect to validator

   ```bash
   # Check validator is running
   kubectl -n provider get pods -l app.kubernetes.io/component=validator

   # Test connection
   kubectl -n provider exec monitor -- \
     curl -s http://validator-alice:26657/status
   ```

3. **Permission issues**: Can't create Kubernetes resources

   ```bash
   # Check RBAC permissions
   kubectl auth can-i create namespace \
     --as=system:serviceaccount:provider:ics-operator-monitor
   ```

### Validator Won't Start

**Symptoms**: Validator pod not running or syncing

**Common Causes**:

1. **Genesis file issues**:

   ```bash
   # Check genesis hash
   kubectl -n provider exec validator -- \
     sha256sum /data/.provider/config/genesis.json
   ```

2. **Persistent peers not configured**:

   ```bash
   # Check config.toml
   kubectl -n provider exec validator -- \
     grep persistent_peers /data/.provider/config/config.toml
   ```

## LoadBalancer and Networking Issues

### LoadBalancer IP Pending

**Symptoms**: LoadBalancer service shows `<pending>` for EXTERNAL-IP

**Diagnosis**:

```bash
# Check LoadBalancer status
kubectl -n provider get svc p2p-loadbalancer

# Check MetalLB (for Kind clusters)
kubectl -n metallb-system get pods
```

**Solutions**:

1. **For Kind clusters**: Install MetalLB

   ```bash
   ./scripts/clusters/install-metallb.sh
   ```

2. **For cloud providers**: Check LoadBalancer annotations

   ```bash
   # Verify annotations in Helm values
   grep -A5 loadBalancerAnnotations values.yaml
   ```

### Validator Endpoints Not Registered

**Symptoms**: Peer discovery fails, validators can't find each other

**Diagnosis**:

```bash
# Check registered endpoints
./scripts/devnet/register-validator-endpoints.sh status

# Query on-chain endpoints
kubectl -n provider exec validator -- \
  interchain-security-pd query provider validator-provider-keys
```

**Solution**:

```bash
# Wait for LoadBalancers to get IPs
kubectl -n provider wait --for=jsonpath='{.status.loadBalancer.ingress[0].ip}' \
  svc/p2p-loadbalancer --timeout=300s

# Register endpoints
./scripts/devnet/register-validator-endpoints.sh
```

### Port Forward Manager Issues

**Symptoms**: Can't access services across clusters in multi-cluster setup

**Diagnosis**:

```bash
# Check port forward logs
kubectl -n provider logs monitor | grep "port forward"

# List active port forwards
ps aux | grep "kubectl port-forward"
```

**Solution**:

```bash
# Restart monitor to re-establish port forwards
kubectl -n provider rollout restart deployment/monitor

# Manually create port forward if needed
kubectl port-forward -n provider svc/validator-bob 26657:26657 &
```

## Consumer Chain Issues

### Consumer Chain Won't Deploy

**Symptoms**: No consumer chain namespace created after LAUNCHED phase

**Diagnosis**:

```bash
# Check if monitor detected LAUNCHED event
kubectl -n provider logs monitor | grep -i "consumer.*launched"

# List consumer namespaces
kubectl get namespaces -l consumer-chain-id

# Check consumer chain status
make show-consumer CONSUMER_ID=0
```

**Solutions**:

1. **Monitor didn't detect event**:

   ```bash
   # Restart monitor to trigger reconciliation
   kubectl -n provider rollout restart deployment/monitor
   ```

2. **Wrong validator selection**:

   ```bash
   # Check if validator is in the selected set
   kubectl -n provider logs monitor | grep "SelectValidatorSubset"
   ```

### Consumer Chain Not Producing Blocks

**Symptoms**: Chain deployed but height stays at 0

**Diagnosis**:

```bash
# Check block height
kubectl -n alice-testchain-0 exec validator-testchain-0 -- \
  interchain-security-cd status | jq .SyncInfo.latest_block_height

# View consumer logs
kubectl -n alice-testchain-0 logs validator-testchain-0

# Check peer connections
kubectl -n alice-testchain-0 exec validator-testchain-0 -- \
  curl -s localhost:26657/net_info | jq .result.n_peers
```

**Common Causes**:

1. **Consumer key mismatch**: CCV genesis doesn't have correct assigned keys

   ```bash
   # Check monitor logs for CCV patch update
   kubectl -n provider logs monitor | grep "CCV patch update"

   # Verify assigned keys in genesis
   kubectl -n alice-testchain-0 get configmap ccv-genesis -o yaml
   ```

2. **Port mismatch**: Consumer chains using wrong ports

   ```bash
   # Check calculated ports
   kubectl -n provider logs monitor | grep "Consumer ports"

   # Verify service ports match
   kubectl -n alice-testchain-0 get svc
   ```

3. **Peer discovery failure**: Wrong persistent_peers

   ```bash
   # Check persistent_peers in config
   kubectl -n alice-testchain-0 exec validator-testchain-0 -- \
     grep persistent_peers /chain/.testchain/config/config.toml
   ```

## Key Management Issues

### Key Import Failures

**Symptoms**: Monitor can't sign transactions, "key not found" errors

**Diagnosis**:

```bash
# List keys in monitor
kubectl -n provider exec monitor -- monitor keys list

# Check keyring directory
kubectl -n provider exec monitor -- ls -la /root/.provider
```

**Solutions**:

1. **Import from mnemonic**:

   ```bash
   # Interactive import
   kubectl -n provider exec -it monitor -- \
     monitor keys add alice --recover --keyring-backend test
   ```

2. **Import from file**:

   ```bash
   # Copy mnemonic to pod
   kubectl -n provider cp mnemonic.txt monitor:/tmp/

   # Import from file
   kubectl -n provider exec monitor -- \
     monitor keys add alice --recover --source /tmp/mnemonic.txt
   ```

### HD Path Issues

**Symptoms**: Wrong validator address derived from mnemonic

**Diagnosis**:

```bash
# Check derived address
kubectl -n provider exec monitor -- \
  monitor keys show alice -a

# Compare with expected address
kubectl -n provider exec validator -- \
  interchain-security-pd keys show alice -a
```

**Solution**:

```bash
# Use explicit HD path
kubectl -n provider exec -it monitor -- \
  monitor keys add alice --recover --hd-path "m/44'/118'/0'/0/0"
```

## Hermes Relayer Issues

### Monitoring Hermes Status

Use the `hermes-status` command to quickly diagnose relayer issues:

```bash
# Check all Hermes relayers
make hermes-status

# Check specific consumer chain
make hermes-status CHAIN_ID=consumer-0-xxx

# Verbose output with full details
make hermes-status CHAIN_ID=consumer-0-xxx VERBOSE=1
```

The status output shows:
- Hermes deployment health and restart counts
- CCV channel status (UNINITIALIZED, INIT, TRYOPEN, OPEN)
- Client and connection IDs
- Any configuration or runtime errors

Common issues revealed by status:
- Missing CCV channels (channel not created yet)
- Wrong client IDs (using client-1 instead of client-0)
- Connection failures (provider unreachable)
- Key import problems (relayer key missing)

### Hermes Won't Start

**Symptoms**: Hermes pod in error state or CrashLoopBackOff

**Diagnosis**:

```bash
# Check Hermes logs
kubectl -n alice-testchain-0 logs hermes

# Check init container logs
kubectl -n alice-testchain-0 logs hermes -c init-keys
```

**Common Causes**:

1. **Permission denied on .hermes directory**:

   ```bash
   # Fixed in deployment - init container sets permissions
   # If still issues, check:
   kubectl -n alice-testchain-0 exec hermes -- ls -la /home/hermes/.hermes
   ```

2. **Key import failure**:

   ```bash
   # Check if keys were imported
   kubectl -n alice-testchain-0 exec hermes -- \
     hermes keys list --chain provider-1
   ```

### CCV Channel Creation Issues

**Symptoms**: Previously saw errors like "channel must be built on top of client: 07-tendermint-0"

**Status**: ✅ FIXED - Automated CCV channel creation now works with dynamic client discovery

**How it works**:
1. Monitor automatically discovers the correct IBC clients and connections
2. Uses `queryProviderClientForConsumer` to find provider's client for the consumer
3. Uses `queryConsumerClientID` to find consumer's client for the provider
4. Creates CCV channel using discovered client/connection IDs
5. No longer assumes hardcoded client IDs (07-tendermint-0)

**Verification**:

```bash
# Check if CCV channel exists
make hermes-status CHAIN_ID=consumer-0-xxx

# Or manually check channels
kubectl -n alice-testchain-0 exec hermes -- \
  hermes query channels --chain testchain-0
```

**If channel creation fails**:
- Check monitor logs for "Creating CCV channel" messages
- Verify monitor has `pods/exec` permission in RBAC
- Ensure both consumer and provider chains are running
- Wait 30 seconds after consumer deployment before checking

## Monitor Issues

### Monitor Not Processing Events

**Symptoms**: Monitor doesn't react to consumer chain events

**Diagnosis**:

```bash
# Check WebSocket connection
kubectl -n provider logs monitor | grep -i websocket

# Check event processing
kubectl -n provider logs monitor | grep "Processing event"
```

**Solution**:

```bash
# Restart monitor to reconnect
kubectl -n provider rollout restart deployment/monitor

# Check if events are being received
kubectl -n provider logs monitor -f | grep -E "create_consumer|update_consumer"
```

### Monitor Creates Wrong Namespace

**Symptoms**: Consumer resources created in unexpected namespace

**Diagnosis**:

```bash
# Check namespace calculation
kubectl -n provider logs monitor | grep "namespace for consumer"

# List created namespaces
kubectl get namespaces -l created-by=monitor
```

**Root Cause**: Namespace uses validator name + chain ID pattern

## Port Allocation Issues

### Port Conflicts

**Symptoms**: "bind: address already in use" errors

**Diagnosis**:

```bash
# Check port allocation
kubectl -n provider logs monitor | grep "Consumer ports"

# List all services and their ports
kubectl get svc --all-namespaces -o wide | grep -E "26656|26657"
```

**Solution**:

Port calculation is deterministic:

```text
P2P: 26656 + 100 + (SHA256(chainID) % 1000) * 10
RPC: 26657 + 100 + (SHA256(chainID) % 1000) * 10
```

Ensure no manual port assignments conflict with this scheme.

## Recovery Procedures

### Complete System Reset

```bash
# Clean everything and redeploy
make clean
make setup
```

### Reset Monitors Only

```bash
# Restart all monitors
for validator in alice bob charlie; do
  kubectl -n provider rollout restart deployment/monitor-$validator
done
```

### Remove Stuck Consumer Chain

```bash
# Clean up consumer namespaces
for validator in alice bob charlie; do
  kubectl delete namespace $validator-testchain-0 --ignore-not-found
done

# Remove consumer chain
make remove-consumer CONSUMER_ID=0
```

### Recover from Failed Key Import

```bash
# Delete monitor pod to trigger fresh start
kubectl -n provider delete pod -l app.kubernetes.io/component=monitor

# Monitor will restart and re-import keys
```

## Prevention Tips

1. **Always use LoadBalancer services** for production deployments
2. **Register validator endpoints** after LoadBalancer IPs are assigned
3. **Use Makefile commands** instead of raw scripts
4. **Check consumer chain phase** before attempting operations
5. **Monitor logs continuously** during deployments
6. **Clean up test chains** after experiments
7. **Use deterministic port allocation** to avoid conflicts
8. **Verify key imports** before starting operations

## Debug Commands Reference

```bash
# Get all pods across namespaces
kubectl get pods --all-namespaces | grep -E "alice|bob|charlie"

# Check all services
kubectl get svc --all-namespaces | grep -E "alice|bob|charlie"

# View recent events
kubectl get events --all-namespaces --sort-by='.lastTimestamp' | tail -20

# Check ConfigMaps
kubectl -n provider get configmaps

# Describe RBAC permissions
kubectl describe clusterrole ics-operator-monitor

# Port forward for local debugging
kubectl -n provider port-forward svc/validator 26657:26657
```

## Getting Help

If these solutions don't resolve your issue:

1. Collect diagnostics:

   ```bash
   # Create diagnostic bundle
   mkdir diagnostics
   kubectl -n provider logs monitor --tail=1000 > diagnostics/monitor.log
   kubectl -n provider describe pod -l app.kubernetes.io/component=monitor > diagnostics/monitor-pods.txt
   kubectl get events --all-namespaces --sort-by='.lastTimestamp' > diagnostics/events.txt
   make status > diagnostics/status.txt
   ```

2. Check versions:

   ```bash
   # Monitor version
   kubectl -n provider exec monitor -- monitor version

   # ICS version
   kubectl -n provider exec validator -- interchain-security-pd version
   ```

3. Report issue with diagnostics at the project repository
