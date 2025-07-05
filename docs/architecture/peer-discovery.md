# LoadBalancer-Based Peer Discovery for ICS Consumer Chains

## Overview

This document describes the implementation of peer discovery for ICS consumer chains using LoadBalancer services. The system enables validators in different Kubernetes clusters to discover and connect to each other's consumer chains while strictly following ICS (Interchain Security) specifications.

## Core Principles

### 1. ICS Compliance

- **Only validators in the initial validator set deploy consumer chains**
- Initial validator set is determined at spawn time by the provider chain
- Initial validator set always contains provider consensus keys for consistent identification
- Consumer key assignments are handled separately during genesis construction
- No random subset selection or pre-determination of validators
- Monitors must honor the provider chain's CCV genesis

### 2. Direct TCP Connectivity

- Tendermint P2P protocol requires direct TCP connections (no proxies)
- Uses SecretConnection encryption that is incompatible with TCP proxies
- LoadBalancer services provide direct TCP exposure
- TCP proxies (Traefik, HAProxy, nginx) cause protocol errors: `auth failure: secret conn failed: proto: BytesValue: wiretype end group for non-group`

### 3. Deterministic Configuration

- All validators calculate the same ports using chain ID hash
- All monitors construct identical genesis files (including sorting fields)
- Peer discovery uses on-chain validator registry
- All monitors run the same opt-in algorithm based on voting power
- No hardcoded validator lists or fallback mechanisms

## Architecture

### System Components

```text
┌─────────────────────────────────────────────────────────────────┐
│                        Provider Chain                            │
│  ┌─────────────────────────────────────────────────────────┐   │
│  │  On-chain Validator Registry (Description Field)         │   │
│  │  - Alice: alice-lb.example.com                          │   │
│  │  - Bob: bob-lb.example.com                              │   │
│  │  - Charlie: charlie-lb.example.com                      │   │
│  └─────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────┘
                                    │
                 ┌──────────────────┴──────────────────┐
                 ▼                                     ▼
        ┌─────────────────┐                   ┌─────────────────┐
        │  Alice Cluster  │                   │   Bob Cluster   │
        │                 │                   │                 │
        │ LoadBalancer:   │                   │ LoadBalancer:   │
        │ alice-lb        │◄──────────────────│ bob-lb          │
        │                 │    Direct TCP     │                 │
        │ Dynamic Ports:  │                   │ Dynamic Ports:  │
        │ - 30416: cons-1 │                   │ - 30416: cons-1 │
        │ - 30523: cons-2 │                   │ - 30523: cons-2 │
        └─────────────────┘                   └─────────────────┘
```

### Namespace Organization

- **Provider Namespace** (`provider`): Contains Provider chain and Monitor deployments
- **Consumer Namespaces** (`{validator}-{chain-id}`): Each consumer chain gets its own namespace
  - Example: `alice-testchain1-0`, `bob-testchain1-0`
  - Contains: Consumer chain deployment + Hermes relayer deployment

## Implementation Flow

### 1. Consumer Chain Creation

```text
User submits create-consumer transaction
    │
    ▼
Provider chain creates consumer (INITIALIZED phase)
    │
    ▼
Monitors determine opt-in using deterministic algorithm
    │
    ▼
Selected validators opt-in before spawn time
    │
    ▼
Validators optionally assign consumer keys
    │
    ▼
At spawn time: Provider determines initial validator set
    │
    ▼
Consumer transitions to LAUNCHED phase
    │
    ▼
CCV genesis generated with initial validator set
```

### 2. Deterministic Opt-in Algorithm

```go
// When consumer is created, determine if validator should opt-in
func shouldOptIn(consumerID string) bool {
    // Query all bonded validators using Cosmos SDK staking module
    // GET /cosmos/staking/v1beta1/validators?status=BOND_STATUS_BONDED
    validators := queryBondedValidators()
    
    // Sort by tokens (voting power) descending
    sort.Slice(validators, func(i, j int) bool {
        return validators[i].Tokens.GT(validators[j].Tokens)
    })
    
    // Calculate total voting power
    totalPower := sdk.ZeroInt()
    for _, val := range validators {
        totalPower = totalPower.Add(val.Tokens)
    }
    
    // Select validators until we reach 67% voting power
    targetPower := totalPower.MulRaw(2).QuoRaw(3)  // 67%
    selectedPower := sdk.ZeroInt()
    selectedValidators := []string{}
    
    for _, validator := range validators {
        selectedValidators = append(selectedValidators, validator.Description.Moniker)
        selectedPower = selectedPower.Add(validator.Tokens)
        
        if selectedPower.GTE(targetPower) {
            break
        }
    }
    
    // Check if local validator is in selected set
    localValidator := getLocalValidatorMoniker()
    for _, selected := range selectedValidators {
        if selected == localValidator {
            return true
        }
    }
    
    return false
}
```

### 3. Monitor Deployment Decision

```go
// When consumer reaches LAUNCHED phase:
func HandlePhaseTransition(consumerID, chainID string, newPhase string) {
    if newPhase != "CONSUMER_PHASE_LAUNCHED" {
        return
    }
    
    // 1. Fetch CCV genesis from provider using ICS provider module
    // GET /interchain_security/ccv/provider/consumer_genesis/{consumer_id}
    ccvGenesis := queryCCVGenesis(consumerID)
    
    // 2. Check if local validator is in initial set
    if !isLocalValidatorInInitialSet(ccvGenesis) {
        log("Not in initial set, skipping deployment")
        return
    }
    
    // 3. Deploy consumer chain
    deployConsumerChain(chainID, ccvGenesis)
    
    // 4. Configure LoadBalancer
    port := calculatePort(chainID)
    addLoadBalancerPort(chainID, port)
    
    // 5. Discover peers from on-chain registry
    peers := discoverPeers(chainID, ccvGenesis.InitialValSet)
    configureConsumerPeers(chainID, peers)
}
```

### 4. Initial Validator Set Checking

The CCV genesis initial validator set always contains the provider's consensus public key, regardless of whether a validator has assigned a consumer key. This ensures consistent validator identification across all monitors.

```go
func isLocalValidatorInInitialSet(ccvGenesis CCVGenesis) bool {
    localValidator := getLocalValidatorInfo()
    
    // Initial validator set always uses provider consensus keys
    providerKey := localValidator.ProviderConsensusKey
    
    // Check against initial validator set
    for _, validator := range ccvGenesis.InitialValSet {
        if validator.PubKey == providerKey {
            return true
        }
    }
    
    return false
}
```

### 5. Deterministic Genesis Construction

When building the consumer genesis, monitors must construct it identically to ensure all validators have the same genesis hash:

```go
func buildConsumerGenesis(ccvPatch CCVGenesis, consumerID string) Genesis {
    // CCV patch already contains initial validator set with correct keys
    genesis := ccvPatch
    
    // Query all key assignments to update validator keys in genesis
    // GET /interchain_security/ccv/provider/address_pairs/{consumer_id}
    keyAssignments := queryAddressPairs(consumerID)
    
    // Update genesis validators with assigned keys
    for i, validator := range genesis.Provider.InitialValSet {
        for _, pair := range keyAssignments.PairValConAddr {
            if validator.Address == pair.ProviderAddress && pair.ConsumerKey != "" {
                // Update to use assigned consumer key
                genesis.Provider.InitialValSet[i].PubKey = pair.ConsumerKey
                break
            }
        }
    }
    
    // Sort all fields to ensure deterministic ordering
    sortGenesisFields(&genesis)
    
    return genesis
}
```

### 6. Peer Discovery

```go
func discoverPeers(chainID string, initialValSet []Validator) []string {
    // 1. Query on-chain validator registry for external endpoints
    registry := queryValidatorRegistry()
    
    // 2. Calculate consumer port
    port := calculatePort(chainID)
    
    // 3. Build peer list ONLY from initial validator set
    var peers []string
    for _, validator := range initialValSet {
        // Get external endpoint from registry
        endpoint := registry[validator.Moniker].P2PEndpoint
        
        // Get node ID from GetNodeInfo API or derive from node key
        nodeInfo := queryNodeInfo(validator.RPCEndpoint)
        nodeID := nodeInfo.DefaultNodeInfo.ID
        
        peer := fmt.Sprintf("%s@%s:%d", nodeID, endpoint, port)
        peers = append(peers, peer)
    }
    
    return peers
}
```

## Technical Details

### Node ID

The Tendermint/CometBFT node ID is derived from the node's public key:
- Stored in `node_key.json` file
- Can be obtained via `GetNodeInfo` API
- Can be shown with `interchain-security-pd tendermint show-node-id`
- Format: 40-character hex string (e.g., `3b4c06c2c0e8f6d7a5b9c1a2f3e4d5c6b7a8e9f0`)

### Port Calculation

```go
func calculatePort(chainID string) int {
    hash := sha256.Sum256([]byte(chainID))
    offset := int(binary.BigEndian.Uint64(hash[:8]) % 1000)
    return 30100 + offset // Base port + deterministic offset
}
```

### LoadBalancer Management

```go
type LoadBalancerManager struct {
    clientset kubernetes.Interface
    logger    *slog.Logger
}

func (m *LoadBalancerManager) AddConsumerPort(chainID string, port int32) error {
    // 1. Get LoadBalancer service
    svc, err := m.clientset.CoreV1().Services("provider").
        Get(ctx, "p2p-loadbalancer", metav1.GetOptions{})
    
    // 2. Add port to service
    svc.Spec.Ports = append(svc.Spec.Ports, v1.ServicePort{
        Name:       fmt.Sprintf("consumer-%s", chainID),
        Port:       port,
        TargetPort: intstr.FromInt(int(port)),
        Protocol:   v1.ProtocolTCP,
    })
    
    // 3. Update service
    _, err = m.clientset.CoreV1().Services("provider").
        Update(ctx, svc, metav1.UpdateOptions{})
    
    // 4. Create EndpointSlice for routing
    return m.createEndpointSlice(chainID, port)
}
```

### Event-Based Endpoint Updates

```go
type ValidatorUpdateHandler struct {
    validatorRegistry *ValidatorRegistry
    peerDiscovery     *PeerDiscovery
}

func (h *ValidatorUpdateHandler) HandleEvent(event Event) error {
    if event.Type != "edit_validator" {
        return nil
    }
    
    // Refresh validator endpoints from chain
    endpoints := h.validatorRegistry.RefreshEndpoints()
    
    // Update peer discovery
    h.peerDiscovery.SetValidatorEndpoints(endpoints)
    
    // Consumer chains will use updated endpoints automatically
    return nil
}
```

## Deployment Setup

### For Testnet (Kind + MetalLB)

1. **Install MetalLB**:
```bash
helm repo add metallb https://metallb.github.io/metallb
helm install metallb metallb/metallb -n metallb-system --create-namespace
```

2. **Configure IP Pool**:
```yaml
apiVersion: metallb.io/v1beta1
kind: IPAddressPool
metadata:
  name: kind-pool
  namespace: metallb-system
spec:
  addresses:
  - 172.18.255.1-172.18.255.250  # Adjust based on your Kind network
---
apiVersion: metallb.io/v1beta1  
kind: L2Advertisement
metadata:
  name: kind-advertisement
  namespace: metallb-system
```

### For Production

Use cloud provider LoadBalancer with appropriate annotations:

**AWS (NLB)**:
```yaml
metadata:
  annotations:
    service.beta.kubernetes.io/aws-load-balancer-type: "nlb"
```

**GCP**:
```yaml
metadata:
  annotations:
    cloud.google.com/load-balancer-type: "External"
```

### Kubernetes Resources

```yaml
# LoadBalancer Service (created by monitor)
apiVersion: v1
kind: Service
metadata:
  name: p2p-loadbalancer
  namespace: provider
spec:
  type: LoadBalancer
  ports: [] # Ports added dynamically by monitor
  
---
# RBAC for monitor
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: ics-monitor
rules:
- apiGroups: [""]
  resources: ["services", "endpoints"]
  verbs: ["get", "list", "watch", "create", "update", "patch"]
- apiGroups: ["discovery.k8s.io"]
  resources: ["endpointslices"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
```

### Validator Registration

```bash
# Get LoadBalancer endpoint
ENDPOINT=$(kubectl get svc p2p-loadbalancer -n provider \
  -o jsonpath='{.status.loadBalancer.ingress[0].hostname}')

# Register on-chain
interchain-security-pd tx staking edit-validator \
  --details "p2p=$ENDPOINT" \
  --from validator-key \
  --chain-id provider \
  --keyring-backend test
```

## CLI Examples

```bash
# List all launched consumer chains
interchain-security-pd query provider list-consumer-chains 3

# Get specific consumer chain details
interchain-security-pd query provider consumer-chain 0

# Get consumer genesis (CCV patch)
interchain-security-pd query provider consumer-genesis 0

# Get opted-in validators
interchain-security-pd query provider consumer-opted-in-validators 0

# Get assigned consumer key for a validator
interchain-security-pd query provider validator-consumer-key 0 cosmosvalcons1...

# Get initial validator set
interchain-security-pd query provider consumer-validators 0
```

## API Reference

### ICS Provider Module APIs

1. **Query Consumer Chain Details**
   - `GET /interchain_security/ccv/provider/consumer_chain/{consumer_id}`
   - Returns phase, spawn time, chain ID, metadata

2. **Query Consumer Genesis (CCV Patch)**
   - `GET /interchain_security/ccv/provider/consumer_genesis/{consumer_id}`
   - Returns initial validator set, parameters, provider info

3. **Query Opted-in Validators**
   - `GET /interchain_security/ccv/provider/opted_in_validators/{consumer_id}`
   - Returns list of validator addresses that opted in

4. **Query Validator Consumer Key**
   - `GET /interchain_security/ccv/provider/validator_consumer_addr/{consumer_id}/{provider_address}`
   - Returns assigned consumer address if key assigned

5. **Query All Key Assignments**
   - `GET /interchain_security/ccv/provider/address_pairs/{consumer_id}`
   - Returns all provider-consumer key mappings

### Cosmos SDK Staking Module APIs

1. **Query Bonded Validators**
   - `GET /cosmos/staking/v1beta1/validators?status=BOND_STATUS_BONDED`
   - Returns all active validators with voting power

2. **Query Validator Details**
   - `GET /cosmos/staking/v1beta1/validators/{validator_addr}`
   - Returns specific validator info including description

### Tendermint/CometBFT APIs

1. **Query Node Info**
   - `GET /cosmos/base/tendermint/v1beta1/node_info`
   - Returns node information including:
     - `default_node_info.id` - Node ID
     - `default_node_info.listen_addr` - P2P listening address
     - `default_node_info.moniker` - Node moniker
     - `default_node_info.network` - Chain ID
     - `application_version` - Application version info

### Note on P2P Endpoints

P2P endpoints can be discovered through:

1. **GetNodeInfo API** - Returns the node's P2P listen address
   - `GET /cosmos/base/tendermint/v1beta1/node_info`
   - Returns `default_node_info.listen_addr` (e.g., `tcp://0.0.0.0:26656`)
   - This is the internal listening address, not necessarily externally accessible

2. **External Registry** - For cross-cluster connectivity
   - Validators register their external LoadBalancer endpoints
   - Can use validator description field: `interchain-security-pd tx staking edit-validator --details "p2p=alice-lb.example.com"`
   - Or maintain a separate service registry

For cross-cluster ICS deployments, an external registry is required since the node's `listen_addr` is typically not routable between clusters.

## Summary

This LoadBalancer-based approach provides:

- ✅ **ICS Compliance**: Only initial validator set deploys
- ✅ **Direct TCP**: Compatible with Tendermint P2P
- ✅ **Dynamic Discovery**: Uses on-chain registry
- ✅ **Uniform Architecture**: Same for testnet and production
- ✅ **Automatic Management**: Monitor handles all complexity
- ✅ **Deterministic Opt-in**: All monitors agree on which validators should opt-in

The key is strict adherence to ICS specifications: monitors use a deterministic algorithm to select validators for opt-in (top validators by voting power until 67% threshold), then check the provider chain's CCV genesis to determine deployment, with no fallback mechanisms or random selection.