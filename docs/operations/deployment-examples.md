# Deployment Examples

This guide provides practical examples of deploying ICS validators using the Helm chart.

## Single Validator Deployment

For validators who want to deploy their own infrastructure:

### Quick Start

```bash
# 1. Clone the repository
git clone https://github.com/cosmos/interchain-security-monitor.git
cd interchain-security-monitor

# 2. Build the monitor image (or use pre-built)
make docker-build

# 3. Create your values file based on the example below
vim my-values.yaml
# Add your configuration using the example values file shown below

# 4. Deploy using Helm
helm install myvalidator ./helm/ics-validator \
  --namespace myvalidator \
  --create-namespace \
  -f my-values.yaml

# 5. Check deployment status
kubectl -n myvalidator get pods
kubectl -n myvalidator logs -l app.kubernetes.io/component=validator

# 6. Verify validator is syncing
kubectl -n myvalidator exec deployment/validator -- \
  curl -s localhost:26657/status | jq .result.sync_info
```

### Example Values File

```yaml
# Validator configuration
validator:
  name: myvalidator
  moniker: "My Awesome Validator"
  index: 0  # This will use m/44'/118'/0'/0/0

# Key configuration
keys:
  type: "mnemonic"
  # Replace with your actual mnemonic
  mnemonic: "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"

# Chain configuration
chain:
  id: "cosmoshub-4"  # Replace with actual chain ID
  image: "ghcr.io/cosmos/interchain-security:v7.0.1"
  
  # Genesis configuration - use URL for public chains
  genesis:
    type: "url"
    url: "https://raw.githubusercontent.com/cosmos/mainnet/master/genesis/genesis.cosmoshub-4.json"

# Network configuration
peers:
  persistent:
    - "e1b058e5cfa2b836ddaa496b10911da62dcf182e@23.88.21.35:26656"
    - "c1b40148e2fd7f908d0fb3ef73c2e5b3e9c7fbd6@34.65.212.73:26656"
  seeds:
    - "ade4d8bc8cbe014af6ebdf3cb7b1e9ad36f412c0@seeds.polkachu.com:15956"

# Monitor configuration
monitor:
  enabled: true
  image: "ics-monitor:latest"

# Service exposure
service:
  type: LoadBalancer  # Or NodePort if not on cloud

# Resource allocation
resources:
  validator:
    requests:
      memory: "2Gi"
      cpu: "1"
    limits:
      memory: "4Gi"
      cpu: "2"

# Storage
storage:
  validator:
    size: 100Gi
```

## Testnet Deployment

The testnet uses the same Helm chart but deploys 3 validators:

```bash
# 1. Generate testnet assets
./scripts/testnet-coordinator.sh

# 2. Create Kind clusters
./scripts/clusters/create-clusters.sh

# 3. Deploy using Helm
./scripts/deploy-testnet-helm.sh

# 4. Check status
for cluster in alice bob charlie; do
  echo "=== $cluster ==="
  kubectl --context kind-${cluster}-cluster -n provider get pods
done
```

## Key Differences

### Single Validator
- Provides their own mnemonic
- Connects to existing network via peers/seeds
- Downloads genesis from URL
- Uses cloud LoadBalancer or NodePort

### Testnet
- Uses predefined test mnemonic
- Creates isolated network
- Generates genesis locally
- Uses Kind cluster networking

## Advanced Examples

### Using Existing Keys

If you already have validator keys:

```yaml
keys:
  type: "explicit"
  validatorKey: |
    {
      "address": "...",
      "pub_key": {
        "type": "tendermint/PubKeyEd25519",
        "value": "..."
      },
      "priv_key": {
        "type": "tendermint/PrivKeyEd25519",
        "value": "..."
      }
    }
  nodeKey: |
    {
      "priv_key": {
        "type": "tendermint/PrivKeyEd25519",
        "value": "..."
      }
    }
```

### Using External Secrets

For GitOps workflows:

```yaml
keys:
  type: "existing-secret"
  secretName: "validator-keys"
```

The secret should contain:
- `mnemonic` (for mnemonic type)
- `priv_validator_key.json` and `node_key.json` (for explicit type)

### High Availability Setup

For production validators with sentries:

```yaml
# Don't expose P2P directly
service:
  type: ClusterIP

# Use private sentries as peers
peers:
  persistent:
    - "nodeid1@sentry1.internal:26656"
    - "nodeid2@sentry2.internal:26656"
  
# Increase resources
resources:
  validator:
    requests:
      memory: "8Gi"
      cpu: "4"
```

## Troubleshooting Deployments

### Check Validator Status
```bash
kubectl -n myvalidator exec deployment/validator -- \
  interchain-security-pd status
```

### View Monitor Logs
```bash
kubectl -n myvalidator logs -l app.kubernetes.io/component=monitor -f
```

### Check Peer Connections
```bash
kubectl -n myvalidator exec deployment/validator -- \
  curl -s localhost:26657/net_info | jq .result.n_peers
```

### Common Issues

1. **Validator not syncing**
   - Check genesis file is correct
   - Verify peers are reachable
   - Ensure sufficient resources

2. **Monitor not starting**
   - Verify validator is running first
   - Check gRPC endpoint is accessible
   - Review monitor logs for errors

3. **Key import failures**
   - Ensure mnemonic is valid
   - Check HD path is correct
   - Verify keyring permissions

## Next Steps

- See [Configuration Reference](configuration.md) for all available options
- Check [Troubleshooting Guide](troubleshooting.md) for common issues
- Review [Helm Deployment Guide](../helm-deployment-guide.md) for detailed Helm usage