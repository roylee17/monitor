# Helm Deployment Guide

This guide explains how to deploy ICS validators using the new Helm-based system.

## Overview

The project now uses a unified Helm chart (`helm/ics-operator`) for all deployments:

- **Single validators** use it to deploy their infrastructure
- **Devnet** uses it 3 times with different configurations

## Prerequisites

1. **Kubernetes cluster** (or Kind for local testing)
2. **Helm 3** installed
3. **Docker** (for building the monitor image)
4. **kubectl** configured

## Devnet Deployment (3 Validators)

### Quick Start

```bash
# 1. Clean any existing setup
make clean

# 2. Deploy the complete devnet
make deploy

# This will:
# - Build the monitor Docker image
# - Generate devnet configuration (genesis, keys)
# - Create 3 Kind clusters (alice, bob, charlie)
# - Deploy validators using Helm
```

### Manual Steps (Understanding the Process)

```bash
# 1. Build the monitor image
make docker-build

# 2. Generate devnet assets (genesis, keys)
./scripts/devnet-coordinator.sh

# 3. Create Kind clusters
./scripts/clusters/create-clusters.sh

# 4. Deploy using Helm
./scripts/deploy-devnet-helm.sh

# Or deploy each validator manually:
helm install alice ./helm/ics-operator \
  --namespace alice \
  --create-namespace \
  --values ./helm/ics-operator/devnet-values.yaml \
  --values ./helm/ics-operator/values/devnet-alice.yaml \
  --set-string chain.genesis.inline="$(cat devnet/assets/alice/config/genesis.json)" \
  --set peers.persistent="{...}"
```

### Check Status

```bash
# View all deployments
make status

# Detailed status
make status-verbose

# View logs
make logs TARGET=alice COMPONENT=validator
make logs TARGET=bob COMPONENT=monitor

# Get shell access
make shell TARGET=alice

# Check Helm releases
helm list -A
```

## Single Validator Deployment

### For Production Validators

```bash
# 1. Clone the repository
git clone https://github.com/sourcenetwork/ics-operator.git
cd interchain-security-monitor

# 2. Build the monitor image
make docker-build

# 3. Create your values file
cat > my-validator.yaml <<EOF
validator:
  name: myvalidator
  moniker: "My Production Validator"

keys:
  type: "mnemonic"
  mnemonic: "your twenty four word mnemonic phrase here..."

chain:
  id: "cosmoshub-4"
  genesis:
    type: "url"
    url: "https://raw.githubusercontent.com/cosmos/mainnet/master/genesis.cosmoshub-4.json"

peers:
  persistent:
    - "e1b058e5cfa2b836ddaa496b10911da62dcf182e@23.88.21.35:26656"
    - "c1b40148e2fd7f908d0fb3ef73c2e5b3e9c7fbd6@34.65.212.73:26656"

monitor:
  enabled: true
  providerEndpoints:
    - "rpc1.cosmos.network:26656"
    - "rpc2.cosmos.network:26656"

service:
  type: LoadBalancer

storage:
  validator:
    size: 100Gi
  monitor:
    size: 10Gi
EOF

# 4. Deploy
helm install myvalidator ./helm/ics-operator \
  --namespace myvalidator \
  --create-namespace \
  -f my-validator.yaml

# 5. Check status
kubectl -n myvalidator get pods
kubectl -n myvalidator logs -l app.kubernetes.io/component=validator
```

## Key Management Options

### Option 1: Mnemonic (Recommended for New Validators)

```yaml
keys:
  type: "mnemonic"
  mnemonic: "guard cream sadness..."
  hdPath: "m/44'/118'/0'/0/0"  # Optional
```

### Option 2: Explicit Keys (For Existing Validators)

```yaml
keys:
  type: "explicit"
  validatorKey: |
    {
      "address": "...",
      "pub_key": {...},
      "priv_key": {...}
    }
  nodeKey: |
    {
      "priv_key": {...}
    }
```

### Option 3: External Secret (For GitOps)

```yaml
keys:
  type: "existing-secret"
  secretName: "my-validator-keys"
```

## Common Operations

### Create Consumer Chain

```bash
# After deployment, create a consumer chain
make create-consumer

# With specific spawn time
./scripts/lifecycle/create-consumer.sh -s 30
```

### Upgrade Validator

```bash
helm upgrade myvalidator ./helm/ics-operator \
  --namespace myvalidator \
  --reuse-values \
  --set chain.image="ghcr.io/cosmos/interchain-security:v7.0.2"
```

### Uninstall

```bash
# Devnet
make clean

# Single validator
helm uninstall myvalidator --namespace myvalidator
kubectl delete namespace myvalidator
```

## Troubleshooting

### Check Pod Status

```bash
kubectl -n <namespace> get pods
kubectl -n <namespace> describe pod <pod-name>
```

### View Logs

```bash
# Validator logs
kubectl -n alice logs -l app.kubernetes.io/component=validator

# Monitor logs
kubectl -n alice logs -l app.kubernetes.io/component=monitor
```

### Check Sync Status

```bash
kubectl -n alice exec deploy/alice-ics-operator-validator -- \
  curl -s localhost:26657/status | jq .result.sync_info
```

### Common Issues

1. **Pods not starting**: Check events with `kubectl describe pod`
2. **Keys not found**: Check init container logs
3. **Not syncing**: Verify peers are configured correctly
4. **Monitor errors**: Ensure validator is fully synced first

## Architecture Notes

- Each validator runs in its own namespace
- The Helm chart creates all necessary resources (RBAC, ConfigMaps, Secrets, etc.)
- Devnet validators use Kind's `host.docker.internal` for cross-cluster communication
- Production validators should use proper DNS/IPs for peers

## Next Steps

- See `helm/ics-operator/README.md` for detailed chart documentation
- Check [Deployment Examples](operations/deployment-examples.md) for more scenarios
- Review `helm/ics-operator/values.yaml` for all configuration options
