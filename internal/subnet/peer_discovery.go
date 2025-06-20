package subnet

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"
)

// PeerDiscovery handles stateless peer discovery for consumer chains
type PeerDiscovery struct {
	logger            *slog.Logger
	baseProviderPort  int32
	portSpacing       int32
	providerEndpoints []string // Provider validator endpoints
}

// NewPeerDiscovery creates a new peer discovery instance
func NewPeerDiscovery(logger *slog.Logger, providerEndpoints []string) *PeerDiscovery {
	return &PeerDiscovery{
		logger:            logger,
		baseProviderPort:  26656, // Standard Cosmos P2P port
		portSpacing:       10,    // Space between consumer chains
		providerEndpoints: providerEndpoints,
	}
}

// GetConsumerP2PPort derives a deterministic P2P port for a consumer chain
func (pd *PeerDiscovery) GetConsumerP2PPort(consumerID string) int32 {
	hash := sha256.Sum256([]byte(consumerID))
	offset := int32(binary.BigEndian.Uint32(hash[:4]) % 1000)
	return pd.baseProviderPort + 100 + (offset * pd.portSpacing)
}

// GetConsumerRPCPort derives the RPC port based on P2P port
func (pd *PeerDiscovery) GetConsumerRPCPort(consumerID string) int32 {
	return pd.GetConsumerP2PPort(consumerID) + 1
}

// GetConsumerAPIPort derives the API port based on P2P port
func (pd *PeerDiscovery) GetConsumerAPIPort(consumerID string) int32 {
	return pd.GetConsumerP2PPort(consumerID) + 661 // Standard offset (1317 - 656)
}

// GetConsumerGRPCPort derives the gRPC port based on P2P port
func (pd *PeerDiscovery) GetConsumerGRPCPort(consumerID string) int32 {
	return pd.GetConsumerP2PPort(consumerID) + 3434 // Standard offset (9090 - 5656)
}

// DiscoverConsumerPeers generates peer endpoints for a consumer chain
func (pd *PeerDiscovery) DiscoverConsumerPeers(consumerID string, validatorSet []string) ([]string, error) {
	p2pPort := pd.GetConsumerP2PPort(consumerID)
	var peers []string

	// Testnet node IDs - in production these would be queried from validators
	// These are the actual node IDs from the current testnet
	testnetNodeIDs := map[string]string{
		"alice":   "9cee33456402ff3370734a61c4f99c612e3c01a5",
		"bob":     "650c9fac5a183cf4de2150a585bf008f21751a6d",
		"charlie": "3121619381646547698f2f343e807f579cfe25c0",
	}

	for _, validator := range validatorSet {
		// Find matching provider endpoint
		providerEndpoint := pd.findProviderEndpoint(validator)
		if providerEndpoint == "" {
			pd.logger.Warn("No provider endpoint found for validator", "validator", validator)
			continue
		}

		// Extract host from provider endpoint
		host := pd.extractHost(providerEndpoint)
		if host == "" {
			pd.logger.Warn("Could not extract host from endpoint", "endpoint", providerEndpoint)
			continue
		}

		// Generate consumer peer endpoint with node ID for testnet
		var consumerPeer string
		if nodeID, exists := testnetNodeIDs[validator]; exists {
			// Include node ID for proper peer format
			consumerPeer = fmt.Sprintf("%s@%s:%d", nodeID, host, p2pPort)
		} else {
			// Fallback without node ID
			consumerPeer = fmt.Sprintf("%s:%d", host, p2pPort)
			pd.logger.Warn("No node ID found for validator, peer may not connect",
				"validator", validator)
		}
		
		peers = append(peers, consumerPeer)
		
		pd.logger.Info("Discovered consumer peer",
			"validator", validator,
			"consumerID", consumerID,
			"peer", consumerPeer)
	}

	return peers, nil
}

// ProbePeer checks if a peer endpoint is reachable
func (pd *PeerDiscovery) ProbePeer(endpoint string, timeout time.Duration) bool {
	conn, err := net.DialTimeout("tcp", endpoint, timeout)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// ProbeConsumerPeers checks which discovered peers are reachable
func (pd *PeerDiscovery) ProbeConsumerPeers(peers []string) []string {
	var reachable []string
	timeout := 5 * time.Second

	for _, peer := range peers {
		if pd.ProbePeer(peer, timeout) {
			reachable = append(reachable, peer)
			pd.logger.Info("Consumer peer is reachable", "peer", peer)
		} else {
			pd.logger.Warn("Consumer peer not reachable", "peer", peer)
		}
	}

	return reachable
}

// GetPersistentPeers formats peers for Cosmos SDK configuration
func (pd *PeerDiscovery) GetPersistentPeers(consumerID string, nodeIDs map[string]string, peers []string) string {
	var persistentPeers []string

	for _, peer := range peers {
		// Extract validator name from peer endpoint
		validator := pd.getValidatorFromPeer(peer)
		if nodeID, ok := nodeIDs[validator]; ok {
			persistentPeer := fmt.Sprintf("%s@%s", nodeID, peer)
			persistentPeers = append(persistentPeers, persistentPeer)
		}
	}

	return strings.Join(persistentPeers, ",")
}

// findProviderEndpoint finds the provider endpoint for a validator
func (pd *PeerDiscovery) findProviderEndpoint(validator string) string {
	for _, endpoint := range pd.providerEndpoints {
		if strings.Contains(endpoint, validator) {
			return endpoint
		}
	}
	return ""
}

// extractHost extracts the host from an endpoint (host:port)
func (pd *PeerDiscovery) extractHost(endpoint string) string {
	parts := strings.Split(endpoint, ":")
	if len(parts) > 0 {
		return parts[0]
	}
	return ""
}

// getValidatorFromPeer extracts validator name from peer endpoint
func (pd *PeerDiscovery) getValidatorFromPeer(peer string) string {
	// This is a simplified implementation
	// In production, you'd have a more robust mapping
	host := pd.extractHost(peer)
	parts := strings.Split(host, "-")
	if len(parts) >= 2 {
		return fmt.Sprintf("%s-%s", parts[0], parts[1])
	}
	return host
}

// ConsumerPorts holds all derived ports for a consumer chain
type ConsumerPorts struct {
	P2P  int32
	RPC  int32
	API  int32
	GRPC int32
}

// GetConsumerPorts returns all ports for a consumer chain
func (pd *PeerDiscovery) GetConsumerPorts(consumerID string) ConsumerPorts {
	return ConsumerPorts{
		P2P:  pd.GetConsumerP2PPort(consumerID),
		RPC:  pd.GetConsumerRPCPort(consumerID),
		API:  pd.GetConsumerAPIPort(consumerID),
		GRPC: pd.GetConsumerGRPCPort(consumerID),
	}
}