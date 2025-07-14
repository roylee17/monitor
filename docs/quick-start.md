# Quick Start Guide

This guide walks you through setting up a devnet, creating a consumer chain, and verifying it's working correctly.

```bash
make quick-start
```

This automated command will:

1. Deploy a 3-validator devnet with monitors
2. Install MetalLB for Kubernetes LoadBalancer services
3. Register validator endpoints on-chain
4. Create a consumer chain with 15-second spawn time
5. Wait for it to launch and start producing blocks
6. Display the final status

The whole process takes about 2-3 minutes.

## Prerequisites

- Docker installed and running
- Kubernetes CLI (`kubectl`) installed
- Kind (Kubernetes in Docker) installed
- Make installed
- At least 8GB of available RAM

## Manual Step-by-Step Process

If you want to understand what `make quick-start` does, or run the steps manually, here's the breakdown:

### Step 1: Deploy the Devnet

```bash
# Full deployment: creates clusters, builds images, and deploys
make deploy
```

This command will:
1. Create 3 Kind clusters (alice, bob, charlie)
2. Install MetalLB for LoadBalancer services
3. Build the monitor Docker image
4. Generate devnet configuration
5. Deploy validators and monitors using Helm

### Step 2: Register Validator Endpoints

```bash
make register-endpoints
```

This registers each validator's LoadBalancer IP address on-chain, enabling peer discovery for consumer chains.

### Step 3: Create a Consumer Chain

```bash
make create-consumer
```

Creates a consumer chain with a 15-second spawn time (default for quick-start).

### Step 4: Monitor the Launch

The monitors will automatically:
1. Detect the new consumer chain
2. Select validators based on voting power (top 66%)
3. Opt-in selected validators
4. Deploy consumer chains after spawn time

### Step 5: Check Status

```bash
make consumer-info CONSUMER_ID=0
```

## Expected Output

After running `make quick-start`, you should see:

```text
✅ Quick start complete! Checking runtime consumer chain status...

========================================
Consumer Chain Information
========================================
Consumer ID: 0
Chain ID: consumer-0-1234567890-0

📊 Block Heights:
  alice     : not deployed
  bob       : 42 (port 26757)
  charlie   : 42 (port 26757)

👥 Validators:
  From bob instance:
    Validators are active (check logs for details)

🔗 Deployment Status:
  alice     : not deployed
  bob       : Running (restarts: 0)
  charlie   : Running (restarts: 0)
```

The matching block heights indicate the consumer chain is producing blocks successfully!

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
make show-validator-endpoints       # Show registered validator endpoints
make hermes-status                  # Show all Hermes relayers status
make hermes-status CHAIN_ID=consumer-0-xxx  # Specific chain's Hermes status
```

### Clean Up

```bash
make clean-consumers     # Remove all consumer chains
make reset              # Full reset and redeploy
make clean              # Clean everything
```

## Troubleshooting

### Consumer Chain Not Producing Blocks?

1. Check if validators have registered their endpoints:

   ```bash
   make show-validator-endpoints
   ```

2. Verify enough validators opted in:

   ```bash
   make show-consumer CONSUMER_ID=0
   ```

3. Check consumer chain logs for errors:

   ```bash
   make consumer-logs CONSUMER_ID=0
   ```

4. Verify LoadBalancer services are working:

   ```bash
   kubectl --context kind-alice-cluster get svc -n metallb-system
   kubectl --context kind-alice-cluster get svc -A | grep LoadBalancer
   ```
