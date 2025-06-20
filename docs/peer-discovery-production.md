# Production Peer Discovery for ICS Consumer Chains

## Overview

The ICS Monitor implements dynamic peer discovery for consumer chains, automatically configuring peers based on the validator set selected for each consumer chain. This document explains how peer discovery works and how to adapt it for production deployments.

## How Peer Discovery Works

### 1. Validator Selection
When a consumer chain is created, the monitor:
- Uses deterministic validator selection to choose a subset (67% by default)
- Each validator's monitor independently calculates the same subset
- Only selected validators deploy the consumer chain

### 2. Peer Discovery Process
The `SimplePeerDiscovery` service:
1. Queries the provider chain for all bonded validators
2. Filters to validators opted-in for the specific consumer
3. Retrieves node IDs for each validator
4. Builds peer addresses excluding self
5. Uses deterministic port allocation based on consumer ID

### 3. Peer Address Format
Peers are configured in the format: `nodeID@host:port`
- `nodeID`: The validator's Tendermint node ID (20-byte hex)
- `host`: The network address where the validator's consumer chain is accessible
- `port`: The P2P port for the consumer chain (deterministically calculated)

## Current Implementation (Testnet)

In the Kubernetes testnet environment:
```
# Example peer addresses
9cee33456402ff3370734a61c4f99c612e3c01a5@finaltest-0.alice-finaltest-0.svc.cluster.local:36536
650c9fac5a183cf4de2150a585bf008f21751a6d@finaltest-0.bob-finaltest-0.svc.cluster.local:36536
```

## Production Adaptation

### 1. External Endpoints
In production, validators run in different infrastructures. Update the peer discovery to use external endpoints:

```go
// In SimplePeerDiscovery.DiscoverPeersWithChainID()

// Instead of Kubernetes service names:
peerHost := fmt.Sprintf("%s.%s.svc.cluster.local", chainID, namespace)

// Use external endpoints:
peerHost := getValidatorExternalEndpoint(validator.Description.Moniker, chainID)
```

### 2. Configuration Options

#### Option A: Static Configuration
```yaml
validators:
  alice:
    provider_endpoint: alice.validators.com:26656
    consumer_template: alice-{{chain-id}}.validators.com:{{port}}
  bob:
    provider_endpoint: bob-validator.example.org:26656
    consumer_template: bob-consumer-{{chain-id}}.example.org:{{port}}
  charlie:
    provider_endpoint: 203.0.113.10:26656
    consumer_template: 203.0.113.10:{{port}}
```

#### Option B: DNS-Based Discovery
Use DNS SRV records:
```
_cosmos-p2p._tcp.alice-finaltest.validators.zone. IN SRV 0 0 36536 alice-consumer.validators.com.
```

#### Option C: Registry-Based Discovery
Use a shared registry (database, etcd, consul) where validators register their consumer endpoints.

### 3. Implementation Example

```go
func (spd *SimplePeerDiscovery) getValidatorEndpoint(validator string, chainID string, port int) string {
    // Option 1: Configuration-based
    if endpoint, exists := spd.config.Validators[validator]; exists {
        return strings.ReplaceAll(endpoint.ConsumerTemplate, "{{chain-id}}", chainID)
    }
    
    // Option 2: DNS-based
    if spd.config.UseDNS {
        srv, _ := net.LookupSRV("cosmos-p2p", "tcp", fmt.Sprintf("%s-%s.%s", validator, chainID, spd.config.DNSZone))
        if len(srv) > 0 {
            return fmt.Sprintf("%s:%d", srv[0].Target, srv[0].Port)
        }
    }
    
    // Option 3: Registry-based
    if spd.registry != nil {
        if endpoint, err := spd.registry.GetEndpoint(validator, chainID); err == nil {
            return endpoint
        }
    }
    
    // Fallback
    return fmt.Sprintf("%s-consumer-%s:%d", validator, chainID, port)
}
```

### 4. Port Allocation
The current implementation uses deterministic port calculation:
```go
func getConsumerP2PPort(consumerID string) int {
    hash := sha256.Sum256([]byte(consumerID))
    offset := binary.BigEndian.Uint32(hash[:4]) % 1000
    return 26656 + 100 + int(offset*10)
}
```

In production, you might:
- Use fixed port ranges per validator
- Configure ports explicitly
- Use port 26656 for all consumers with different IPs/domains

## Security Considerations

1. **Node ID Verification**: Ensure node IDs match the validator's actual identity
2. **TLS/mTLS**: Consider adding mutual TLS between validators
3. **Firewall Rules**: Only allow connections from known validator IPs
4. **Private Networks**: Use VPN or private peering between validators

## Monitoring and Troubleshooting

### Check Peer Configuration
```bash
# Inside a consumer chain container
grep persistent_peers /data/.*/config/config.toml
```

### Verify Peer Connections
```bash
# Check if peers are connected
curl http://localhost:26657/net_info | jq '.result.peers[].node_info.moniker'
```

### Common Issues
1. **Peers not connecting**: Check firewall rules and network connectivity
2. **Wrong node IDs**: Verify node IDs match actual validator nodes
3. **Port conflicts**: Ensure ports are not already in use

## Best Practices

1. **High Availability**: Run consumer chains on separate infrastructure from provider
2. **Geographic Distribution**: Spread validators across regions
3. **Monitoring**: Set up alerts for peer disconnections
4. **Regular Updates**: Keep peer lists updated as validators change

## Example Production Setup

### Alice's Infrastructure (AWS)
```
Provider: alice.validators.com:26656
Consumer Template: alice-consumer-*.us-east-1.elb.amazonaws.com:26656
```

### Bob's Infrastructure (GCP)
```
Provider: bob-validator.example.org:26656  
Consumer Template: consumer-*.bob.example.org:26656
```

### Charlie's Infrastructure (Bare Metal)
```
Provider: 203.0.113.10:26656
Consumer Template: 203.0.113.10:{26700-26799}
```

## Summary

The dynamic peer discovery system provides a flexible foundation for production deployments. The key adaptation needed is replacing Kubernetes service names with actual external endpoints accessible across different validator infrastructures. The modular design allows for various discovery mechanisms (static config, DNS, registry) based on operational preferences.