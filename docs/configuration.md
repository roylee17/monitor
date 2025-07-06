# Configuration Reference

This document provides a comprehensive reference for configuring the Interchain Security Monitor system.

## Table of Contents

- [Configuration Overview](#configuration-overview)
- [Command-Line Flags](#command-line-flags)
- [Environment Variables](#environment-variables)
- [Configuration File](#configuration-file)
- [Helm Chart Configuration](#helm-chart-configuration)
- [Key Management](#key-management)
- [Kubernetes-Specific Configuration](#kubernetes-specific-configuration)
- [LoadBalancer Configuration](#loadbalancer-configuration)
- [Example Configurations](#example-configurations)

## Configuration Overview

The monitor uses a simplified configuration model with the following precedence (highest to lowest):

1. Command-line flags
2. Environment variables
3. Configuration file (`$HOME/.monitor.yaml`)
4. Default values

The monitor is designed to work with minimal configuration, automatically discovering most settings from the environment.

## Command-Line Flags

### Core Flags

| Flag | Description | Default | Example |
|------|-------------|---------|---------|
| `--config` | Config file path | `$HOME/.monitor.yaml` | `--config /etc/monitor/config.yaml` |
| `--node` | RPC endpoint for provider chain | `tcp://localhost:26657` | `--node tcp://validator:26657` |
| `--ws-url` | WebSocket URL for events | `ws://localhost:26657/websocket` | `--ws-url ws://validator:26657/websocket` |
| `--chain-id` | Provider chain ID | `provider` | `--chain-id provider-1` |
| `--home` | Directory for config and data | `/data/.provider` | `--home /var/lib/monitor` |

### Key Management Flags

| Flag | Description | Default | Example |
|------|-------------|---------|---------|
| `--from` | Key name or address to sign with | - | `--from alice` |
| `--keyring-backend` | Keyring backend type | `test` | `--keyring-backend test` |

**Note**: Currently only the `test` keyring backend is supported.

### Kubernetes Deployment Flags

| Flag | Description | Default | Example |
|------|-------------|---------|---------|
| `--consumer-namespace` | Namespace for consumer chains | `<validator>-consumer-chains` | `--consumer-namespace alice-consumers` |
| `--consumer-image` | Docker image for consumer chains | `ghcr.io/cosmos/interchain-security:v7.0.1` | `--consumer-image myregistry/ics:latest` |
| `--provider-endpoints` | Provider P2P endpoints | - | `--provider-endpoints alice:26656,bob:26656` |

## Environment Variables

### Core Variables

| Variable | Type | Default | Description |
|----------|------|---------|-------------|
| `CHAIN_ID` | string | - | Chain ID of provider chain |
| `CHAIN_RPC` | string | - | RPC endpoint URL |
| `CHAIN_GRPC` | string | - | gRPC endpoint URL |
| `VALIDATOR_KEY` | string | - | Validator key name |
| `TESTNET_COORDINATOR_KEY` | string | - | Testnet coordinator key |

### Feature Flags

| Variable | Type | Default | Description |
|----------|------|---------|-------------|
| `AUTO_UPDATE_CONSUMERS` | bool | `false` | Enable automatic consumer chain updates when validator endpoints change |
| `HYBRID_PEER_UPDATES` | bool | `false` | Enable zero-downtime peer updates via RPC before falling back to restarts |

### Testnet Mode Variables

| Variable | Description | Used When | Example |
|----------|-------------|-----------|---------|
| `MULTI_CLUSTER_MODE` | Enable multi-cluster mode | Testnet deployments | `MULTI_CLUSTER_MODE=true` |
| `VALIDATOR_NAME` | Name of the validator | Testnet deployments | `VALIDATOR_NAME=alice` |

All command-line flags can also be set via environment variables using Viper's automatic env binding.

## Feature Flags Detailed Reference

### AUTO_UPDATE_CONSUMERS

**Purpose**: Automatically update consumer chains when validator P2P endpoints change.

**Default**: `false` (manual updates required)

**How it works**:

1. ValidatorUpdateHandler detects `edit_validator` events
2. ConsumerRegistry identifies affected consumer chains
3. ConsumerChainUpdater updates peer configurations
4. Updates are applied based on HYBRID_PEER_UPDATES setting

**Enable when**:

- Manual updates are operationally burdensome
- You've tested the feature in staging
- Brief downtime during updates is acceptable

### HYBRID_PEER_UPDATES

**Purpose**: Minimize downtime by attempting RPC-based peer updates before pod restarts.

**Default**: `false` (restart-based updates)

**Requires**: `AUTO_UPDATE_CONSUMERS=true`

**How it works**:

1. Always updates ConfigMap for persistence
2. Attempts to add peers via Tendermint RPC `/dial_peers`
3. Verifies connectivity via `/net_info`
4. Falls back to pod restart if RPC fails

**Enable when**:

- AUTO_UPDATE_CONSUMERS is working reliably
- Zero-downtime is critical
- Running Tendermint/CometBFT v0.34+

## Configuration File

The monitor supports configuration files in YAML format. By default, it looks for `.monitor.yaml` in the home directory.

Example configuration file:

```yaml
# Provider chain connection
node: tcp://validator-alice:26657
ws-url: ws://validator-alice:26657/websocket
chain-id: provider-1

# Key configuration
from: alice
keyring-backend: test

# Kubernetes deployment
consumer-namespace: alice-consumer-chains
consumer-image: ghcr.io/cosmos/interchain-security:v7.0.1
provider-endpoints:
  - validator-alice:26656
  - validator-bob:26656
  - validator-charlie:26656
```

## Helm Chart Configuration

When deployed via Helm, the monitor is configured through the following values:

### Validator Configuration

```yaml
validator:
  name: validator          # Name of the validator (e.g., alice, bob, charlie)
  moniker: "ICS Validator" # Display name for the validator
  index: 0                 # Index for HD key derivation (0 for alice, 1 for bob, etc.)
```

### Key Management Configuration

```yaml
keys:
  # Type of key configuration: "mnemonic", "explicit", or "existing-secret"
  type: "mnemonic"

  # For type: "mnemonic" - BIP39 mnemonic phrase
  mnemonic: "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon actual"

  # HD derivation path (optional, uses default if not specified)
  hdPath: "m/44'/118'/0'/0/0"

  # For type: "explicit" - directly provide keys
  validatorKey: ""  # Validator private key
  nodeKey: ""       # Node P2P key

  # For type: "existing-secret" - reference existing Kubernetes secret
  secretName: ""    # Name of secret containing keys
```

### Chain Configuration

```yaml
chain:
  id: "provider-1"
  binary: "interchain-security-pd"
  image: "ghcr.io/cosmos/interchain-security:v7.0.1"

  # Genesis configuration
  genesis:
    # Type: "url", "inline", or "existing-configmap"
    type: "url"

    # For type: "url"
    url: "https://example.com/genesis.json"

    # For type: "inline"
    inline: |
      {
        "genesis_time": "2024-01-01T00:00:00Z",
        ...
      }

    # For type: "existing-configmap"
    configMapName: "provider-genesis"
```

### Monitor Configuration

```yaml
monitor:
  enabled: true
  image: "ics-monitor:latest"
  imagePullPolicy: "IfNotPresent"

  # Provider endpoints for peer discovery
  providerEndpoints:
    - "validator-alice:26656"
    - "validator-bob:26656"
    - "validator-charlie:26656"

  # Resource limits
  resources:
    requests:
      memory: "256Mi"
      cpu: "100m"
    limits:
      memory: "1Gi"
      cpu: "500m"
```

### Testnet Configuration

```yaml
testnet:
  enabled: false           # Enable testnet mode
  kindCluster: false       # Running in Kind cluster
  useDockerInternal: false # Use host.docker.internal for Docker Desktop

  # External endpoints for multi-cluster setup
  externalEndpoints:
    validator-alice: "10.0.1.10:26656"
    validator-bob: "10.0.1.11:26656"
    validator-charlie: "10.0.1.12:26656"
```

## Key Management

The monitor includes a built-in key management system accessible via the `monitor keys` command:

### Adding Keys

```bash
# Add a new key
monitor keys add <keyname> --keyring-backend test

# Import from mnemonic (interactive)
monitor keys add <keyname> --recover --keyring-backend test

# Import from mnemonic file
monitor keys add <keyname> --recover --source mnemonic.txt --keyring-backend test
```

### Managing Keys

```bash
# List all keys
monitor keys list --keyring-backend test

# Show specific key details
monitor keys show <keyname> --keyring-backend test

# Export a key
monitor keys export <keyname>

# Delete a key
monitor keys delete <keyname> --keyring-backend test
```

## Kubernetes-Specific Configuration

### Service Account and RBAC

The monitor requires specific Kubernetes permissions, configured automatically by the Helm chart:

- Create, update, delete Deployments
- Create, update, delete Services
- Create, update, delete ConfigMaps
- Create, update, delete Jobs
- Create, update, delete PersistentVolumeClaims
- Manage EndpointSlices for LoadBalancer discovery

### Persistent Storage

```yaml
persistence:
  enabled: true
  size: "10Gi"
  storageClass: ""  # Uses cluster default if not specified
  accessMode: "ReadWriteOnce"
```

### Port Allocation

Consumer chains use deterministic port allocation:

- Base ports: P2P (26656), RPC (26657), API (1317), gRPC (9090)
- Consumer offset: 100
- Port calculation: `base + offset + (SHA256(chainID) % 1000) * 10`

## LoadBalancer Configuration

The monitor uses LoadBalancer services for peer discovery in production:

### LoadBalancer Service Configuration

```yaml
loadBalancerAnnotations:
  # AWS NLB example
  service.beta.kubernetes.io/aws-load-balancer-type: "nlb"
  service.beta.kubernetes.io/aws-load-balancer-scheme: "internal"

  # GCP example
  # cloud.google.com/load-balancer-type: "Internal"

  # Azure example
  # service.beta.kubernetes.io/azure-load-balancer-internal: "true"
```

### Peer Discovery Setup

1. LoadBalancer services are created for each validator's P2P port
2. Validators register their LoadBalancer endpoints on-chain
3. Monitors discover peers through on-chain endpoint queries
4. Consumer chains connect using registered endpoints

For local Kind clusters, MetalLB must be installed:

```bash
# Install MetalLB
./scripts/clusters/install-metallb.sh

# Register endpoints after LoadBalancers get IPs
./scripts/testnet/register-validator-endpoints.sh
```

## Example Configurations

### Minimal Local Development

```bash
# Start monitor with minimal configuration
monitor start \
  --node tcp://localhost:26657 \
  --chain-id provider-1 \
  --from alice
```

### Kubernetes Testnet Deployment

```yaml
# values-testnet.yaml
validator:
  name: alice
  index: 0

chain:
  id: provider-1

monitor:
  enabled: true
  providerEndpoints:
    - validator-alice:26656
    - validator-bob:26656
    - validator-charlie:26656

testnet:
  enabled: true
  kindCluster: true
```

```bash
# Deploy with Helm
helm install alice ./helm/ics-validator -f values-testnet.yaml
```

### Production Deployment

```yaml
# values-production.yaml
validator:
  name: my-validator
  moniker: "My Production Validator"

keys:
  type: existing-secret
  secretName: validator-keys

chain:
  id: cosmoshub-4
  genesis:
    type: url
    url: https://github.com/cosmos/mainnet/raw/master/genesis/cosmoshub-4/genesis.json

monitor:
  enabled: true
  image: myregistry/ics-monitor:v1.0.0
  resources:
    requests:
      memory: "512Mi"
      cpu: "250m"
    limits:
      memory: "2Gi"
      cpu: "1000m"

persistence:
  enabled: true
  size: "100Gi"
  storageClass: "fast-ssd"

loadBalancerAnnotations:
  service.beta.kubernetes.io/aws-load-balancer-type: "nlb"
  service.beta.kubernetes.io/aws-load-balancer-scheme: "internal"
```

## Configuration Best Practices

1. **Use Helm values** for Kubernetes deployments rather than command-line flags
2. **Store sensitive data** in Kubernetes Secrets, not in values files
3. **Use LoadBalancer services** for production peer discovery
4. **Configure appropriate resource limits** based on expected load
5. **Enable persistence** for production deployments
6. **Use specific image tags** rather than `latest` in production
7. **Configure cloud-specific LoadBalancer annotations** for optimal networking
8. **Test configuration** in staging environment before production deployment

## Configuration Validation

The monitor validates configuration on startup and will fail fast if:

- Required flags are missing (e.g., `--from` for signing)
- RPC endpoints are unreachable
- Chain ID doesn't match the connected chain
- Keyring doesn't contain the specified key
- Kubernetes permissions are insufficient

Check monitor logs for detailed error messages:

```bash
kubectl logs -n provider monitor-alice
```
