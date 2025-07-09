# ICS Validator Helm Chart

This Helm chart deploys an Interchain Security validator and monitor to Kubernetes.

## Installation

### Single Validator Deployment

For a production validator with your own mnemonic:

```bash
helm install myvalidator ./helm/ics-validator \
  --namespace myvalidator \
  --create-namespace \
  --set validator.name=myvalidator \
  --set validator.moniker="My Validator Node" \
  --set keys.mnemonic="your twenty four word mnemonic phrase..." \
  --set chain.genesis.url="https://example.com/genesis.json" \
  --set peers.persistent="{nodeID1@peer1.example.com:26656,nodeID2@peer2.example.com:26656}"
```

### Devnet Deployment

For the 3-validator devnet:

```bash
# Use the provided script
./scripts/deploy-devnet-helm.sh

# Or deploy manually
helm install alice ./helm/ics-validator \
  --namespace alice \
  --values ./helm/ics-validator/devnet-values.yaml \
  --values ./helm/ics-validator/values/devnet-alice.yaml \
  --set-string chain.genesis.inline="$(cat genesis.json)" \
  --set peers.persistent="{...}"
```

## Configuration

### Key Management

The chart supports three ways to provide validator keys:

1. **Mnemonic** (recommended for new validators):
   ```yaml
   keys:
     type: "mnemonic"
     mnemonic: "your mnemonic phrase..."
     hdPath: "m/44'/118'/0'/0/0"  # Optional, auto-calculated if not set
   ```

2. **Explicit Keys** (for existing validators):
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

3. **Existing Secret** (for GitOps workflows):
   ```yaml
   keys:
     type: "existing-secret"
     secretName: "my-validator-keys"
   ```

### Genesis Configuration

Three ways to provide the genesis file:

1. **URL** (for public chains):
   ```yaml
   chain:
     genesis:
       type: "url"
       url: "https://example.com/genesis.json"
   ```

2. **Inline** (for private chains):
   ```yaml
   chain:
     genesis:
       type: "inline"
       inline: |
         {
           "genesis_time": "...",
           "chain_id": "..."
         }
   ```

3. **Existing ConfigMap**:
   ```yaml
   chain:
     genesis:
       type: "existing-configmap"
       configMapName: "genesis-config"
   ```

### Service Types

Configure how to expose your validator:

- **ClusterIP** (default): Internal access only
- **NodePort**: Expose on specific node ports
- **LoadBalancer**: Cloud provider load balancer

```yaml
service:
  type: NodePort
  nodePorts:
    p2p: 30656
    rpc: 30657
    api: 31317
    grpc: 32090
```

### Storage

Enable persistent storage for blockchain data:

```yaml
storage:
  validator:
    size: 100Gi
    storageClassName: "fast-ssd"
  monitor:
    size: 10Gi
```

## Values Reference

| Parameter | Description | Default |
|-----------|-------------|---------|
| `validator.name` | Validator name | `validator` |
| `validator.moniker` | Validator display name | `ICS Validator` |
| `validator.index` | HD derivation index | `0` |
| `keys.type` | Key provision method | `mnemonic` |
| `keys.mnemonic` | BIP39 mnemonic phrase | `""` |
| `chain.id` | Chain ID | `provider-1` |
| `chain.image` | Validator container image | `ghcr.io/cosmos/interchain-security:v7.0.1` |
| `monitor.enabled` | Deploy monitor | `true` |
| `monitor.image` | Monitor container image | `ics-monitor:latest` |
| `service.type` | Kubernetes service type | `ClusterIP` |
| `resources.*` | Resource limits/requests | See values.yaml |
| `storage.*` | Persistent storage config | See values.yaml |

## Examples

### Production Validator with Persistent Storage

```yaml
validator:
  name: prod-validator
  moniker: "My Production Validator"

keys:
  type: "mnemonic"
  mnemonic: "..."

chain:
  genesis:
    type: "url"
    url: "https://mainnet.example.com/genesis.json"

peers:
  persistent:
    - "e5c22...@sentry1.example.com:26656"
    - "a3b4c...@sentry2.example.com:26656"

service:
  type: LoadBalancer

storage:
  validator:
    size: 500Gi
    storageClassName: "fast-ssd"

resources:
  validator:
    requests:
      memory: "4Gi"
      cpu: "2"
    limits:
      memory: "8Gi"
      cpu: "4"
```

### Devnet Validator in Kind

```yaml
validator:
  name: alice
  index: 0

devnet:
  enabled: true
  kindCluster: true
  useDockerInternal: true

service:
  type: NodePort
  nodePorts:
    rpc: 30657

storage:
  validator:
    size: ""  # Use emptyDir for devnet
```

## Upgrading

To upgrade a deployed validator:

```bash
helm upgrade myvalidator ./helm/ics-validator \
  --namespace myvalidator \
  --reuse-values \
  --set chain.image="ghcr.io/cosmos/interchain-security:v7.0.2"
```

## Uninstalling

```bash
helm uninstall myvalidator --namespace myvalidator
```

Note: This will delete the validator deployment but preserve PVCs if persistent storage was used.