package subnet

import (
	"context"
	"time"
)

// Example of how monitors would use production peer discovery:

// DeployConsumerWithProductionPeers shows how to deploy with production peer discovery
func (m *K8sManager) DeployConsumerWithProductionPeers(
	ctx context.Context,
	chainID string,
	consumerID string,
	ports ConsumerPorts,
	ccvPatch map[string]interface{},
	peerManager *ProductionPeerManager,
) error {
	m.logger.Info("Deploying consumer with production peer discovery",
		"chain_id", chainID,
		"consumer_id", consumerID)
	
	// Step 1: Register ourselves in the peer registry
	externalP2P := peerManager.config.GetConsumerP2PAddress(consumerID, int(ports.P2P))
	externalRPC := peerManager.config.GetConsumerP2PAddress(consumerID, int(ports.RPC))
	
	// Update discovery service with actual addresses
	peerManager.discoveryService.externalP2PAddr = externalP2P
	peerManager.discoveryService.externalRPCAddr = externalRPC
	
	// Register self
	if err := peerManager.discoveryService.RegisterSelf(ctx, consumerID, peerManager.config.ProviderExternalAddress); err != nil {
		m.logger.Error("Failed to register self in peer registry", "error", err)
		// Continue anyway - other validators might have our info
	}
	
	// Step 2: Wait a bit for other validators to register (in production, this could be event-driven)
	time.Sleep(5 * time.Second)
	
	// Step 3: Discover peers
	peers, err := peerManager.discoveryService.DiscoverPeers(ctx, consumerID)
	if err != nil {
		m.logger.Warn("Failed to discover peers", "error", err)
		peers = []string{} // Start without peers, will connect later
	}
	
	m.logger.Info("Discovered peers for consumer chain",
		"chain_id", chainID,
		"peer_count", len(peers),
		"peers", peers)
	
	// Step 4: Deploy with discovered peers
	return m.DeployConsumerWithPortsAndGenesis(ctx, chainID, consumerID, ports, peers, ccvPatch)
}

// Example deployment flow in production:

// 1. Alice's monitor (AWS us-east-1):
//    - Deploys consumer at alice-consumer-1.us-east-1.validators.com:27656
//    - Registers: {validator: "alice", node_id: "abc123...", p2p_address: "alice-consumer-1.us-east-1.validators.com:27656"}
//
// 2. Bob's monitor (GCP europe-west1):
//    - Deploys consumer at bob-consumer.example.org:27656
//    - Registers: {validator: "bob", node_id: "def456...", p2p_address: "bob-consumer.example.org:27656"}
//    - Discovers Alice's peer: "abc123...@alice-consumer-1.us-east-1.validators.com:27656"
//
// 3. Charlie's monitor (Bare metal):
//    - Deploys consumer at 203.0.113.10:27656
//    - Registers: {validator: "charlie", node_id: "ghi789...", p2p_address: "203.0.113.10:27656"}
//    - Discovers both Alice and Bob's peers

// ProductionDeploymentExample shows a complete example
func ProductionDeploymentExample() {
	// Each validator would have their own configuration
	aliceConfig := &ProductionConfig{
		ValidatorName:            "alice",
		ProviderExternalAddress:  "alice.validators.com:26656",
		ConsumerExternalTemplate: "alice-{{consumer-id}}.validators.com:{{port}}",
		DiscoveryMethod:          "registry",
		RegistryType:             "database",
		RegistryEndpoint:         "postgres://shared-registry.validators.network:5432/peers",
		Infrastructure: InfrastructureInfo{
			Provider: "aws",
			Region:   "us-east-1",
		},
	}
	
	bobConfig := &ProductionConfig{
		ValidatorName:            "bob",
		ProviderExternalAddress:  "bob-validator.example.org:26656",
		ConsumerExternalTemplate: "bob-consumer-{{consumer-id}}.example.org:{{port}}",
		DiscoveryMethod:          "registry",
		RegistryType:             "database",
		RegistryEndpoint:         "postgres://shared-registry.validators.network:5432/peers",
		Infrastructure: InfrastructureInfo{
			Provider: "gcp",
			Region:   "europe-west1",
		},
	}
	
	// Alternative: DNS-based discovery
	dnsBasedConfig := &ProductionConfig{
		ValidatorName:           "charlie",
		ProviderExternalAddress: "203.0.113.10:26656",
		ConsumerExternalTemplate: "203.0.113.10:{{port}}",
		DiscoveryMethod:         "dns",
		DNSTemplate:             "_cosmos-p2p._tcp.{{consumer-id}}.validators.zone",
		Infrastructure: InfrastructureInfo{
			Provider:   "bare-metal",
			Datacenter: "dc1",
		},
	}
	
	_ = aliceConfig
	_ = bobConfig
	_ = dnsBasedConfig
}

// For validators that prefer simpler setups, they could use:
// 1. Static peer lists maintained in configuration
// 2. DNS SRV records for peer discovery
// 3. Shared configuration in a git repository
// 4. On-chain peer registry (store peer info in blockchain state)