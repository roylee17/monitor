# Testing Consumer Chains with Key Assignment

## Prerequisites

1. Fresh cluster with provider chain running
2. Monitors deployed and running
3. All scripts executable

## Quick Test

Run the automated test script:

```bash
./scripts/test-consumer-with-keys.sh
```

This script will:
1. Create a consumer chain
2. Assign consumer keys for all validators
3. Wait for spawn time
4. Check if chain is producing blocks

## Manual Testing Steps

### 1. Create Consumer Chain

```bash
./scripts/lifecycle/create-consumer.sh -c testchain3 -s 60
```

Note the consumer ID from the output (e.g., `Consumer chain created with ID: 0`)

### 2. Assign Consumer Keys

Before the spawn time, assign consumer keys for each validator:

```bash
# For all validators
./scripts/assign-consumer-key.sh -c 0 -v alice
./scripts/assign-consumer-key.sh -c 0 -v bob  
./scripts/assign-consumer-key.sh -c 0 -v charlie

# Or just for selected validators
./scripts/assign-consumer-key.sh -c 0 -v bob
./scripts/assign-consumer-key.sh -c 0 -v charlie
```

### 3. Verify Key Assignments

Check that keys were stored:

```bash
# Check ConfigMaps
kubectl get configmaps -n provider -l app.kubernetes.io/component=consumer-keys

# View specific key assignment
kubectl get configmap -n provider consumer-keys-0 -o yaml
```

### 4. Monitor Chain Launch

Watch for phase transition:

```bash
# Check chain status
./scripts/lifecycle/list-consumers.sh

# Watch monitor logs
kubectl logs -n provider -l app.kubernetes.io/name=monitor -f | grep -E "(testchain3|consensus_key)"
```

### 5. Verify Deployment

After spawn time, check deployments:

```bash
# Check namespaces
kubectl get namespaces | grep testchain3

# Check pods
kubectl get pods -A | grep testchain3
```

### 6. Verify Block Production

Check if the chain is producing blocks:

```bash
# Get pod name
POD=$(kubectl get pods -n bob-testchain3-0 -o name | head -1)

# Check logs for consumer key usage
kubectl logs -n bob-testchain3-0 $POD | grep -E "(consumer validator key|validator address)"

# Check current height
kubectl logs -n bob-testchain3-0 $POD | grep "height=" | tail -10

# Watch for new blocks
kubectl logs -n bob-testchain3-0 $POD -f | grep "executing block"
```

## Expected Results

### With Consumer Keys Assigned

1. Logs show: "Using assigned consumer validator key"
2. Validator address matches the assigned key
3. Chain produces blocks (height > 1)
4. Consensus works between validators

### Without Consumer Keys

1. Logs show: "WARNING: No consumer validator key provided"
2. Chain stuck at height 1
3. Logs show: "This node is not a validator"

## Troubleshooting

### Keys Not Being Used

1. Check ConfigMap has the key:
   ```bash
   kubectl get configmap -n <namespace> <chain>-config -o yaml | grep priv_validator_key
   ```

2. Check deployment has latest ConfigMap:
   ```bash
   kubectl rollout restart deployment -n <namespace> <chain>
   ```

### Chain Not Producing Blocks

1. Verify all validators have assigned keys:
   ```bash
   kubectl get configmap -n provider consumer-keys-<id> -o yaml
   ```

2. Check validator set matches assigned keys:
   ```bash
   kubectl logs -n <namespace> <pod> | grep "initial_val_set"
   ```

3. Ensure peers are connected:
   ```bash
   kubectl logs -n <namespace> <pod> | grep "numInPeers"
   ```

## Clean Up

Remove test chains:

```bash
# Remove consumer chain
./scripts/lifecycle/remove-consumer.sh -c <consumer-id>

# Delete namespaces
kubectl delete namespace bob-testchain3-0 charlie-testchain3-0

# Clean up ConfigMaps
kubectl delete configmap -n provider consumer-keys-<id>
```