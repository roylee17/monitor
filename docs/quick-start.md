# Quick Start Guide

This guide walks you through setting up a devnet, creating a consumer chain, and verifying it's working correctly.

```bash
make quick-start
```

This will:

1. Deploy a 3-validator devnet
2. Install MetalLB for as a Kubernetes LoadBalancer
3. Register validator endpoints
4. Create a consumer chain
5. Wait for it to start producing blocks
6. Show you the status

The whole process takes about 5 minutes.

## Prerequisites

- Docker installed and running
- Kubernetes CLI (`kubectl`) installed
- Kind (Kubernetes in Docker) installed
- Make installed
- At least 8GB of available RAM

## Step 1: Deploy the Devnet

Start by deploying a fresh 3-validator devnet with monitors:

```bash
# Full deployment: creates clusters, builds images, and deploys
make deploy
```

This command will:

1. Create 3 Kind clusters (alice, bob, charlie)
2. Build the monitor Docker image
3. Generate devnet configuration
4. Deploy validators and monitors using Helm

Wait for all pods to be running (about 30-60 seconds):

```bash
# Check deployment status
make status
```

You should see output like:

```text
📊 Cluster status:

=== ALICE CLUSTER ===
NAME                        READY   STATUS    RESTARTS   AGE
monitor-xxxxxxxxx-xxxxx     1/1     Running   0          45s
validator-xxxxxxxxx-xxxxx   1/1     Running   0          45s

=== BOB CLUSTER ===
NAME                         READY   STATUS    RESTARTS   AGE
monitor-xxxxxxxxx-xxxxx      1/1     Running   0          45s
validator-xxxxxxxxx-xxxxx    1/1     Running   0          45s

=== CHARLIE CLUSTER ===
NAME                         READY   STATUS    RESTARTS   AGE
monitor-xxxxxxxxx-xxxxx      1/1     Running   0          44s
validator-xxxxxxxxx-xxxxx    1/1     Running   0          44s

🔗 Peer connections:
alice     : 2
bob       : 2
charlie   : 2
```

## Step 2: Install MetalLB (Required for LoadBalancer Services)

The monitors use LoadBalancer services for peer discovery. Install MetalLB:

```bash
./scripts/clusters/install-metallb.sh
```

This provides LoadBalancer IPs for each cluster:

- Alice: 192.168.97.100-109
- Bob: 192.168.97.110-119
- Charlie: 192.168.97.120-129

## Step 3: Register Validator Endpoints

Register each validator's P2P endpoint on-chain:

```bash
make register-endpoints
```

This registers the LoadBalancer IPs so validators can discover each other's consumer chains.

## Step 4: Create a Consumer Chain

Create a consumer chain with a 10-second spawn time:

```bash
make create-consumer
```

You'll see output like:

```text
📝 Creating consumer chain...
[INFO] Next consumer ID will be: 0
[INFO] Generated chain ID: consumer-0-1234567890-0
[INFO] Current time: 2025-01-01T12:00:00Z
[INFO] Spawn time: 2025-01-01T12:00:10Z (10 seconds from now)

✅ Consumer chain created with ID: 0

Consumer Chain 0
----------------------------------------
  Chain ID             : consumer-0-1234567890-0
  Phase                : CONSUMER_PHASE_INITIALIZED
  Spawn Time           : 2025-01-01T12:00:10Z
  Time until spawn     : 8 seconds
```

## Step 5: Wait for Chain Launch

The monitors will automatically:

1. Detect the new consumer chain
2. Decide which validators should opt-in (based on voting power)
3. Opt-in selected validators
4. Deploy consumer chains after spawn time

Wait about 15-20 seconds, then check the consumer status:

```bash
make show-consumer CONSUMER_ID=0
```

You should see the chain in LAUNCHED phase:

```text
Consumer Chain 0
----------------------------------------
  Chain ID             : consumer-0-1234567890-0
  Phase                : CONSUMER_PHASE_LAUNCHED

Opted-in validators:
  • cosmosvalcons1xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
  • cosmosvalcons1yyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyy
```

## Step 6: Verify Consumer Chain is Running

Check comprehensive status with:

```bash
make consumer-info CONSUMER_ID=0
```

This shows:

```text
========================================
Consumer Chain Information
========================================
Consumer ID: 0
Chain ID: consumer-0-1234567890-0

📊 Block Heights:
  alice     : not deployed
  bob       : 125 (port 26657)
  charlie   : 125 (port 26657)

👥 Validators:
  From bob instance:
    Validators are active (check logs for details)

🔗 Deployment Status:
  alice     : not deployed
  bob       : Running (restarts: 0)
  charlie   : Running (restarts: 0)
```

The block heights increasing indicate the consumer chain is producing blocks successfully!

## Step 7: View Consumer Chain Logs (Optional)

To see the consumer chain in action:

```bash
# View logs from bob's consumer chain
make consumer-logs CONSUMER_ID=0 CLUSTER=bob
```

## Common Commands

### Status Checks

```bash
make status              # Basic cluster status
make status-verbose      # Detailed status with block heights
make list-consumers      # List all consumer chains
make consumer-info CONSUMER_ID=0  # Detailed consumer chain status
```

### Consumer Chain Management

```bash
make create-consumer     # Create with 10s spawn time
make show-consumer CONSUMER_ID=0    # Show consumer details
make consumer-info CONSUMER_ID=0    # Comprehensive status
make remove-consumer CONSUMER_ID=0  # Remove consumer chain
```

### Debugging

```bash
make logs TARGET=monitor-alice      # Monitor logs
make logs TARGET=validator-bob      # Validator logs
make consumer-logs CONSUMER_ID=0    # Consumer chain logs
make shell TARGET=alice             # Shell into validator
```

### Clean Up

```bash
make clean-consumers     # Remove all consumer chains
make reset              # Full reset and redeploy
make clean              # Clean everything
```

## Troubleshooting

### Consumer Chain Not Producing Blocks?

1. Check if enough validators opted in:

   ```bash
   make show-consumer CONSUMER_ID=0
   ```

2. Verify MetalLB is installed:

   ```bash
   kubectl --context kind-alice-cluster get svc -n metallb-system
   ```
