# Validator Endpoint Updates

This document describes how validator P2P endpoint updates are handled and how they affect consumer chains.

## Current Implementation

### Validator Endpoint Registration
1. Validators register their P2P endpoints on-chain using the `update_validator_endpoint` transaction
2. The endpoint typically contains the LoadBalancer's external IP address
3. The `ValidatorUpdateHandler` listens for `edit_validator` events and updates the peer discovery registry

### Event Flow
```
Validator Updates Endpoint -> edit_validator TX -> ValidatorUpdateHandler -> PeerDiscovery Registry Update
```

### Current Behavior
- When a validator updates their P2P endpoint, the `ValidatorUpdateHandler`:
  - Detects the `edit_validator` event
  - Fetches the latest validator endpoints from the chain
  - Updates the local peer discovery registry
  - Logs a warning that consumer chains may need manual updates

## Gap 8: Automatic Consumer Chain Updates

### Problem
When a validator's P2P endpoint changes, existing consumer chains continue using the old peer addresses in their configuration. This can lead to connectivity issues if the old endpoint becomes unavailable.

### Solution Design

#### Required Components
1. **ConsumerRegistry Interface**: To query active consumer chains and their opted-in validators
2. **K8sManager Enhancement**: Add methods to update consumer chain configurations

#### Implementation Steps

1. **Extend ValidatorUpdateHandler**:
```go
type ValidatorUpdateHandler struct {
    logger            *slog.Logger
    peerDiscovery     *subnet.PeerDiscovery
    validatorRegistry *ValidatorRegistry
    stakingClient     stakingtypes.QueryClient
    consumerRegistry  ConsumerRegistry      // NEW
    k8sManager        K8sManagerInterface   // NEW
}
```

2. **Add ConsumerRegistry Interface**:
```go
type ConsumerRegistry interface {
    GetActiveConsumers(ctx context.Context) ([]ConsumerInfo, error)
    GetOptedInValidators(ctx context.Context, consumerID string) ([]string, error)
}
```

3. **Add K8sManager Methods**:
```go
// UpdateConsumerPeers updates the peer configuration for a consumer chain
func (m *K8sManager) UpdateConsumerPeers(ctx context.Context, chainID, consumerID string, peers []string) error {
    // 1. Update ConfigMap with new peer list
    // 2. Trigger rolling restart of consumer pods
}
```

4. **Update Flow**:
```
Validator Updates Endpoint 
    -> edit_validator TX 
    -> ValidatorUpdateHandler 
    -> Find affected consumer chains
    -> Update ConfigMaps
    -> Rolling restart consumer pods
```

### Implementation Considerations

1. **Performance**: Updating consumer chains should be done asynchronously to avoid blocking event processing
2. **Reliability**: Failed updates should be retried with exponential backoff
3. **Coordination**: In multi-cluster deployments, each monitor only updates its own consumer chains
4. **Validation**: Verify new endpoints are reachable before updating consumer configurations

### Alternative Approaches

1. **Webhook-based Updates**: Consumer chains could watch for ConfigMap changes and reload peers dynamically
2. **Sidecar Pattern**: A sidecar container could manage peer updates without pod restarts
3. **Service Mesh**: Use a service mesh to handle endpoint changes transparently

### Current Status

The foundation for this feature is in place:
- `ValidatorUpdateHandler` detects endpoint changes
- Peer discovery registry is updated
- A warning is logged for manual intervention

The automatic update functionality is marked as TODO and would require the additional components described above.

## Manual Update Process

Until automatic updates are implemented, operators must manually update consumer chains when validator endpoints change:

1. Identify affected consumer chains (those with the updated validator in their opted-in set)
2. Update the peer list in the consumer chain's ConfigMap
3. Restart the consumer chain pods to pick up the new configuration

Example:
```bash
# Update ConfigMap
kubectl edit configmap consumer-1-config -n consumer-1-namespace

# Restart pods
kubectl rollout restart deployment consumer-1 -n consumer-1-namespace
```