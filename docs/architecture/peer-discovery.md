# Production Peer Discovery for ICS Consumer Chains

## Overview

The ICS Monitor implements dynamic peer discovery for consumer chains, automatically configuring peers based on the validator set selected for each consumer chain. This document explains how peer discovery works, including our learnings from the Traefik TCP proxy incompatibility and the production-ready solution using direct NodePort/LoadBalancer exposure.

## Key Learning: Tendermint P2P Protocol Incompatibility with TCP Proxies

### The Protobuf Error

During development, we discovered that Tendermint's P2P protocol is incompatible with standard TCP proxies (Traefik, HAProxy, nginx, etc.). The error manifests as:

```text
auth failure: secret conn failed: proto: BytesValue: wiretype end group for non-group
```

### Root Cause

Tendermint uses a custom encrypted protocol called **SecretConnection** for P2P communication. This protocol:

- Performs its own encryption and authentication
- Uses a specific handshake sequence
- Is sensitive to any modification of the TCP stream
- Cannot tolerate the buffering or stream manipulation that TCP proxies perform

### Failed Attempts

We attempted several solutions that didn't work:

1. **Traefik TCP Router**: Even with raw TCP mode, the protobuf errors persisted
2. **ServersTransportTCP**: Custom transport configuration didn't resolve the issue
3. **Disabled timeouts**: Setting all timeouts to 0 didn't help
4. **TCP entrypoint tuning**: No configuration resolved the protocol incompatibility

## Production-Ready Solution: Direct TCP Exposure via LoadBalancer

Given the incompatibility with TCP proxies, the solution is **direct TCP exposure** without any intermediate proxy. We use a **shared LoadBalancer** approach that works uniformly across testnet and production environments.

### Unified Architecture Overview

```text
┌──────────────────────────────┐     ┌─────────────────────────────┐
│   Validator A Cluster        │     │   Validator B Cluster       │
│                              │     │                             │
│  ┌───────────────────────┐   │     │  ┌───────────────────────┐  │
│  │ Shared LoadBalancer   │   │     │  │ Shared LoadBalancer   │  │
│  │ Public IP: 1.2.3.4    │   │     │  │ Public IP: 5.6.7.8    │  │
│  │                       │   │     │  │                       │  │
│  │ Direct TCP Ports:     │   │     │  │ Direct TCP Ports:     │  │
│  │ - 30100 → Consumer-1  │   │     │  │ - 30100 → Consumer-1  │  │
│  │ - 30127 → Consumer-2  │   │     │  │ - 30127 → Consumer-2  │  │
│  │ - 30145 → Consumer-3  │   │     │  │ - 30145 → Consumer-3  │  │
│  └─────────┬─────────────┘   │     │  └─────────┬─────────────┘  │
│             │ No Proxy!      │     │             │ No Proxy!      │
│             │ Direct TCP     │     │             │ Direct TCP     │
│             ▼                │     │             ▼                │
│  ┌─────────────────────────┐ │     │  ┌─────────────────────────┐ │
│  │ Consumer Services       │ │     │  │ Consumer Services       │ │
│  │ (NodePort/LoadBalancer) │ │     │  │ (NodePort/LoadBalancer) │ │
│  └─────────────────────────┘ │     │  └─────────────────────────┘ │
│            │                 │     │            │                 │
│            ▼                 │     │            ▼                 │
│  ┌─────────┐ ┌─────────┐     │     │  ┌─────────┐ ┌─────────┐    │
│  │Consumer │ │Consumer │     │     │  │Consumer │ │Consumer │    │
│  │Chain 1  │ │Chain 2  │     │     │  │Chain 1  │ │Chain 2  │    │
│  └─────────┘ └─────────┘     │     │  └─────────┘ └─────────┘    │
└──────────────────────────────┘     └─────────────────────────────┘
                │                                     │
                └──────────── Internet ───────────────┘
                         Direct P2P Connections
```

### Standard Implementation: Shared LoadBalancer with Dynamic Ports

This approach works identically in both testnet (using MetalLB) and production (using cloud LoadBalancers).

#### How It Works

1. **Validator Setup**:
   - Each validator deploys ONE LoadBalancer service in their cluster
   - Registers their LoadBalancer's external IP/hostname on-chain
   - Example: Alice registers `alice.validators.com`

2. **Consumer Chain Creation**:
   - Monitor deploys consumer chain pods in a dedicated namespace
   - Creates a Service that routes through the shared LoadBalancer
   - Adds a new port to the LoadBalancer configuration
   - Port is deterministically calculated from chain ID

3. **Peer Discovery**:
   - Other validators query on-chain registry for endpoints
   - Calculate the same deterministic port for the consumer
   - Connect using: `<nodeID>@<hostname>:<port>`

#### LoadBalancer Service Configuration

```yaml
apiVersion: v1
kind: Service
metadata:
  name: p2p-loadbalancer
  namespace: provider
  labels:
    app: ics-monitor
    component: p2p-gateway
spec:
  type: LoadBalancer
  # No selector here - we use EndpointSlices for dynamic routing
  ports:
    # Ports are added dynamically by the monitor when consumer chains are created
    - name: consumer-testchain1-0
      port: 30143  # Deterministic port based on chain ID
      targetPort: 30143
      protocol: TCP
    - name: consumer-testchain2-0  
      port: 30187  # Different consumer, different port
      targetPort: 30187
      protocol: TCP

---
# EndpointSlice created by monitor for each consumer
apiVersion: discovery.k8s.io/v1
kind: EndpointSlice
metadata:
  name: consumer-testchain1-0-endpoints
  namespace: provider
  labels:
    kubernetes.io/service-name: p2p-loadbalancer
addressType: IPv4
endpoints:
  - addresses:
      - "10.244.0.5"  # Pod IP of consumer chain
    conditions:
      ready: true
    targetRef:
      kind: Pod
      name: testchain1-0
      namespace: alice-testchain1-0
ports:
  - port: 26656  # Consumer's P2P port
    name: p2p
    protocol: TCP
```

#### Dynamic Service Management

When the monitor creates a consumer chain:

1. **Deploy Consumer Chain**:
   ```go
   // Deploy consumer chain in dedicated namespace
   namespace := fmt.Sprintf("%s-%s", validatorName, chainID)
   deployConsumerChain(namespace, chainID)
   ```

2. **Update LoadBalancer Service**:
   ```go
   // Calculate deterministic port
   port := CalculatePorts(chainID).P2P
   
   // Add port to LoadBalancer service
   addPortToLoadBalancer("p2p-loadbalancer", port, chainID)
   ```

3. **Create EndpointSlice**:
   ```go
   // Route traffic to consumer pod
   createEndpointSlice(chainID, consumerPodIP, port)
   ```

#### Advantages of This Approach

- ✅ **Uniform**: Same setup for testnet (MetalLB) and production (cloud LB)
- ✅ **Cost-effective**: One LoadBalancer for all consumer chains
- ✅ **Dynamic**: Ports added/removed as consumers are created/deleted  
- ✅ **Direct TCP**: No proxies, compatible with Tendermint P2P
- ✅ **Deterministic**: All validators calculate the same ports
- ✅ **Scalable**: Supports hundreds of consumer chains per validator

## LoadBalancer Setup

### For Testnet (Kind Clusters)

We use **MetalLB** to provide LoadBalancer services in Kind clusters:

```yaml
# metallb-config.yaml
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

Deploy MetalLB using Helm:
```bash
helm repo add metallb https://metallb.github.io/metallb
helm install metallb metallb/metallb -n metallb-system --create-namespace
kubectl apply -f metallb-config.yaml
```

### For Production

Use your cloud provider's LoadBalancer:

**AWS (NLB)**:
```yaml
metadata:
  annotations:
    service.beta.kubernetes.io/aws-load-balancer-type: "nlb"
    service.beta.kubernetes.io/aws-load-balancer-scheme: "internet-facing"
```

**GCP**:
```yaml
metadata:
  annotations:
    cloud.google.com/load-balancer-type: "External"
```

**Azure**:
```yaml
metadata:
  annotations:
    service.beta.kubernetes.io/azure-load-balancer-resource-group: "myResourceGroup"

## End-to-End Flow

### 1. Initial Validator Setup

Each validator:

1. **Deploys the ICS Monitor** with Helm
2. **Creates a shared LoadBalancer service** (automatically created by monitor)
3. **Registers their LoadBalancer endpoint on-chain**:

```bash
# Get LoadBalancer external IP/hostname
kubectl get svc p2p-loadbalancer -n provider -o jsonpath='{.status.loadBalancer.ingress[0].hostname}'
# Returns: alice-lb.us-east-1.elb.amazonaws.com (or IP address)

# Register on-chain
interchain-security-pd tx staking edit-validator \
    --details "Validator alice
p2p=alice-lb.us-east-1.elb.amazonaws.com" \
    --from alice \
    --chain-id provider \
    --keyring-backend test
```

### 2. Consumer Chain Creation

When a consumer chain is proposed and launched:

1. **Monitors detect the CONSUMER_PHASE_LAUNCHED event**
2. **Each opted-in validator's monitor**:
   - Deploys consumer chain in namespace `<validator>-<chain-id>`
   - Calculates deterministic port: `port = 30100 + (hash(chainID) % 1000)`
   - Updates LoadBalancer to route `port` → consumer pod
   - Creates EndpointSlice for traffic routing

### 3. Peer Discovery

Validators discover peers for consumer chains:

1. **Query on-chain registry** for validator endpoints
2. **Calculate deterministic port** for the consumer chain
3. **Generate deterministic node ID** for each validator/chain combination
4. **Build peer list**:

```text
Alice registered: alice-lb.us-east-1.elb.amazonaws.com
Bob registered: bob-lb.eu-west-1.elb.amazonaws.com  
Charlie registered: charlie-lb.ap-south-1.elb.amazonaws.com

For consumer "testchain1-0":
Port = 30100 + (hash("testchain1-0") % 1000) = 30143

Peers:
- <alice-node-id>@alice-lb.us-east-1.elb.amazonaws.com:30143
- <bob-node-id>@bob-lb.eu-west-1.elb.amazonaws.com:30143
- <charlie-node-id>@charlie-lb.ap-south-1.elb.amazonaws.com:30143
```

### 4. Connection Flow

```text
Bob's Consumer → Bob's LB:30143 → Alice's LB:30143 → Alice's Consumer
     (TCP)          (Internet)         (Internal)         (Pod)
```

## Deterministic Calculations

### Port Calculation

```go
func CalculatePorts(chainID string) (*Ports, error) {
    hash := sha256.Sum256([]byte(chainID))
    offset := int(binary.BigEndian.Uint64(hash[:8]) % 1000)
    
    return &Ports{
        P2P: 30100 + offset,
        RPC: 30200 + offset,
        GRPC: 30300 + offset,
    }, nil
}
```

### Node ID Generation

```go
func GetNodeID(validatorName, chainID string) (string, error) {
    seed := validatorName + "-" + chainID
    // Generate deterministic ed25519 key from seed
    // Return hex-encoded node ID
}
```

## Uniform Deployment Approach

### Key Principles

1. **Same architecture everywhere**: LoadBalancer-based approach works identically in testnet and production
2. **No special cases**: MetalLB in Kind provides the same LoadBalancer semantics as cloud providers
3. **Automatic service management**: Monitor handles all LoadBalancer configuration dynamically
4. **Deterministic addressing**: All validators calculate the same ports and node IDs

### Testnet Setup (Kind + MetalLB)

```bash
# 1. Create Kind cluster
kind create cluster --config kind-cluster.yaml

# 2. Install MetalLB
helm install metallb metallb/metallb -n metallb-system --create-namespace

# 3. Configure IP pool (use Kind network range)
kubectl apply -f metallb-config.yaml

# 4. Deploy ICS validator
helm install alice ./helm/ics-validator \
  --set validator.name=alice \
  --set validator.mnemonic="$ALICE_MNEMONIC"

# 5. LoadBalancer automatically gets external IP
kubectl get svc p2p-loadbalancer -n provider
# NAME               TYPE           CLUSTER-IP      EXTERNAL-IP     PORT(S)
# p2p-loadbalancer   LoadBalancer   10.96.1.2      172.18.255.1    30143:31245/TCP
```

### Production Setup

```bash
# Same Helm deployment!
helm install alice ./helm/ics-validator \
  --set validator.name=alice \
  --set validator.mnemonic="$ALICE_MNEMONIC" \
  --set loadBalancer.annotations."service\.beta\.kubernetes\.io/aws-load-balancer-type"="nlb"

# LoadBalancer gets real external endpoint
kubectl get svc p2p-loadbalancer -n provider  
# NAME               TYPE           CLUSTER-IP      EXTERNAL-IP                          PORT(S)
# p2p-loadbalancer   LoadBalancer   10.96.1.2      alice-lb.us-east-1.elb.amazonaws.com  30143:31245/TCP
```

## Implementation Details

### Monitor Responsibilities

The ICS Monitor automatically:

1. **Creates/manages the shared LoadBalancer** on startup
2. **Adds ports dynamically** when consumer chains are created
3. **Routes traffic** using EndpointSlices to correct consumer pods
4. **Removes ports** when consumer chains are deleted
5. **Handles all networking complexity** transparently

### Helm Configuration

```yaml
# values.yaml
loadBalancer:
  enabled: true
  annotations:
    # AWS NLB
    service.beta.kubernetes.io/aws-load-balancer-type: "nlb"
    # GCP
    # cloud.google.com/load-balancer-type: "External"
    # Azure  
    # service.beta.kubernetes.io/azure-load-balancer-resource-group: "myRG"
  
# For testnet with MetalLB - no annotations needed
# MetalLB automatically assigns IPs from configured pool
```

### Security Considerations

1. **Firewall Rules**: Only open required P2P ports (30100-31100 range)
2. **DDoS Protection**: Use cloud provider's DDoS protection on LoadBalancer
3. **Monitoring**: Track connection counts and bandwidth per consumer
4. **Rate Limiting**: Consider rate limiting at LoadBalancer level

## Troubleshooting

### Common Issues

1. **LoadBalancer pending in Kind**:
   - Check MetalLB is installed: `kubectl get pods -n metallb-system`
   - Verify IP pool configuration matches Kind network

2. **Peers can't connect**:
   - Verify LoadBalancer has external IP/hostname
   - Check validator registered correct endpoint on-chain
   - Ensure firewall allows traffic on calculated ports

3. **Port conflicts**:
   - Rare due to deterministic calculation
   - If occurs, check for hash collisions in port calculation

### Debugging Commands

```bash
# Check LoadBalancer status
kubectl get svc p2p-loadbalancer -n provider -o yaml

# List all EndpointSlices
kubectl get endpointslices -n provider -l kubernetes.io/service-name=p2p-loadbalancer

# Verify port routing
kubectl describe svc p2p-loadbalancer -n provider

# Test connectivity
telnet <loadbalancer-ip> <consumer-port>
```

## Summary

This unified approach provides:

- ✅ **Identical setup** for testnet (MetalLB) and production (cloud LB)
- ✅ **Automatic management** by the ICS Monitor
- ✅ **Direct TCP connections** compatible with Tendermint
- ✅ **Cost-effective** with one LoadBalancer per validator
- ✅ **Scalable** to hundreds of consumer chains
- ✅ **Simple** for validators to deploy and operate

The key insight is using a shared LoadBalancer with dynamic port management, making the deployment uniform across all environments while maintaining the direct TCP connectivity required by Tendermint's P2P protocol.
