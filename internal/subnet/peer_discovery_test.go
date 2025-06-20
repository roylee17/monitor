package subnet

import (
	"log/slog"
	"os"
	"testing"
)

func TestPeerDiscovery(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	
	// Test with sample provider endpoints
	providerEndpoints := []string{
		"validator-1.testnet.svc.cluster.local:26656",
		"validator-2.testnet.svc.cluster.local:26656",
		"validator-3.testnet.svc.cluster.local:26656",
	}
	
	pd := NewPeerDiscovery(logger, providerEndpoints)
	
	// Test deterministic port calculation
	consumerID := "consumer-test-1"
	p2pPort := pd.GetConsumerP2PPort(consumerID)
	rpcPort := pd.GetConsumerRPCPort(consumerID)
	apiPort := pd.GetConsumerAPIPort(consumerID)
	grpcPort := pd.GetConsumerGRPCPort(consumerID)
	
	t.Logf("Consumer ID: %s", consumerID)
	t.Logf("P2P Port: %d", p2pPort)
	t.Logf("RPC Port: %d", rpcPort)
	t.Logf("API Port: %d", apiPort)
	t.Logf("gRPC Port: %d", grpcPort)
	
	// Verify deterministic calculation
	p2pPort2 := pd.GetConsumerP2PPort(consumerID)
	if p2pPort != p2pPort2 {
		t.Errorf("Port calculation not deterministic: %d != %d", p2pPort, p2pPort2)
	}
	
	// Test peer discovery
	validatorSet := []string{"validator-1", "validator-2", "validator-3"}
	peers, err := pd.DiscoverConsumerPeers(consumerID, validatorSet)
	if err != nil {
		t.Fatalf("Failed to discover peers: %v", err)
	}
	
	t.Logf("Discovered peers: %v", peers)
	
	// Verify we got 3 peers
	if len(peers) != 3 {
		t.Errorf("Expected 3 peers, got %d", len(peers))
	}
	
	// Test with different consumer ID
	consumerID2 := "another-consumer"
	p2pPort3 := pd.GetConsumerP2PPort(consumerID2)
	t.Logf("Consumer ID 2: %s, P2P Port: %d", consumerID2, p2pPort3)
	
	// Verify different consumer gets different port
	if p2pPort == p2pPort3 {
		t.Errorf("Different consumers should get different ports: %d == %d", p2pPort, p2pPort3)
	}
}