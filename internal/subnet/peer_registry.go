package subnet

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// PeerInfo contains information about a validator's consumer chain peer
type PeerInfo struct {
	Validator      string    `json:"validator"`
	NodeID         string    `json:"node_id"`
	ConsumerID     string    `json:"consumer_id"`
	P2PAddress     string    `json:"p2p_address"`     // External address for P2P
	RPCAddress     string    `json:"rpc_address"`     // External address for RPC
	LastUpdated    time.Time `json:"last_updated"`
	Infrastructure string    `json:"infrastructure"`  // e.g., "aws-us-east", "gcp-europe"
}

// PeerRegistry manages peer information for consumer chains across different infrastructures
type PeerRegistry struct {
	logger      *slog.Logger
	peers       map[string]map[string]PeerInfo // map[consumerID]map[validator]PeerInfo
	mu          sync.RWMutex
	persistence PeerPersistence
}

// PeerPersistence interface for storing/retrieving peer information
type PeerPersistence interface {
	Save(consumerID string, peers map[string]PeerInfo) error
	Load(consumerID string) (map[string]PeerInfo, error)
	List() ([]string, error)
}

// NewPeerRegistry creates a new peer registry
func NewPeerRegistry(logger *slog.Logger, persistence PeerPersistence) *PeerRegistry {
	return &PeerRegistry{
		logger:      logger,
		peers:       make(map[string]map[string]PeerInfo),
		persistence: persistence,
	}
}

// RegisterPeer registers a validator's consumer chain peer information
func (pr *PeerRegistry) RegisterPeer(consumerID string, info PeerInfo) error {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	
	if pr.peers[consumerID] == nil {
		pr.peers[consumerID] = make(map[string]PeerInfo)
	}
	
	info.LastUpdated = time.Now()
	pr.peers[consumerID][info.Validator] = info
	
	pr.logger.Info("Registered peer",
		"consumer_id", consumerID,
		"validator", info.Validator,
		"node_id", info.NodeID,
		"p2p_address", info.P2PAddress)
	
	// Persist to storage
	if pr.persistence != nil {
		if err := pr.persistence.Save(consumerID, pr.peers[consumerID]); err != nil {
			pr.logger.Error("Failed to persist peer info", "error", err)
		}
	}
	
	return nil
}

// GetPeers retrieves all registered peers for a consumer chain
func (pr *PeerRegistry) GetPeers(consumerID string) ([]PeerInfo, error) {
	pr.mu.RLock()
	defer pr.mu.RUnlock()
	
	peers, exists := pr.peers[consumerID]
	if !exists {
		// Try loading from persistence
		if pr.persistence != nil {
			loaded, err := pr.persistence.Load(consumerID)
			if err == nil && loaded != nil {
				pr.mu.RUnlock()
				pr.mu.Lock()
				pr.peers[consumerID] = loaded
				pr.mu.Unlock()
				pr.mu.RLock()
				peers = loaded
			}
		}
	}
	
	var result []PeerInfo
	for _, peer := range peers {
		result = append(result, peer)
	}
	
	return result, nil
}

// GetPeerAddresses returns formatted peer addresses for CometBFT configuration
func (pr *PeerRegistry) GetPeerAddresses(consumerID string, excludeValidator string) ([]string, error) {
	peers, err := pr.GetPeers(consumerID)
	if err != nil {
		return nil, err
	}
	
	var addresses []string
	for _, peer := range peers {
		// Skip self
		if peer.Validator == excludeValidator {
			continue
		}
		
		// Skip stale peers (older than 1 hour)
		if time.Since(peer.LastUpdated) > time.Hour {
			pr.logger.Warn("Skipping stale peer",
				"validator", peer.Validator,
				"last_updated", peer.LastUpdated)
			continue
		}
		
		// Format: nodeID@host:port
		peerAddr := fmt.Sprintf("%s@%s", peer.NodeID, peer.P2PAddress)
		addresses = append(addresses, peerAddr)
	}
	
	return addresses, nil
}

// PeerDiscoveryService handles peer discovery across different infrastructures
type PeerDiscoveryService struct {
	logger       *slog.Logger
	registry     *PeerRegistry
	nodeDiscovery *NodeDiscovery
	localValidator string
	externalP2PAddr string // External address where this validator's consumer chain is reachable
	externalRPCAddr string
}

// NewPeerDiscoveryService creates a new peer discovery service
func NewPeerDiscoveryService(
	logger *slog.Logger,
	registry *PeerRegistry,
	nodeDiscovery *NodeDiscovery,
	localValidator string,
	externalP2PAddr string,
	externalRPCAddr string,
) *PeerDiscoveryService {
	return &PeerDiscoveryService{
		logger:          logger,
		registry:        registry,
		nodeDiscovery:   nodeDiscovery,
		localValidator:  localValidator,
		externalP2PAddr: externalP2PAddr,
		externalRPCAddr: externalRPCAddr,
	}
}

// RegisterSelf registers this validator's consumer chain in the peer registry
func (pds *PeerDiscoveryService) RegisterSelf(ctx context.Context, consumerID string, providerRPCEndpoint string) error {
	// Get our node ID from the provider chain
	nodeID, err := pds.nodeDiscovery.GetNodeID(providerRPCEndpoint)
	if err != nil {
		return fmt.Errorf("failed to get node ID: %w", err)
	}
	
	info := PeerInfo{
		Validator:      pds.localValidator,
		NodeID:         nodeID,
		ConsumerID:     consumerID,
		P2PAddress:     pds.externalP2PAddr,
		RPCAddress:     pds.externalRPCAddr,
		Infrastructure: pds.getInfrastructureID(),
	}
	
	return pds.registry.RegisterPeer(consumerID, info)
}

// DiscoverPeers discovers peers for a consumer chain
func (pds *PeerDiscoveryService) DiscoverPeers(ctx context.Context, consumerID string) ([]string, error) {
	return pds.registry.GetPeerAddresses(consumerID, pds.localValidator)
}

// getInfrastructureID returns an identifier for this validator's infrastructure
func (pds *PeerDiscoveryService) getInfrastructureID() string {
	// In production, this could be:
	// - AWS region: "aws-us-east-1"
	// - GCP zone: "gcp-us-central1-a"  
	// - Bare metal: "datacenter-1"
	// For now, return a simple identifier
	return fmt.Sprintf("%s-infra", pds.localValidator)
}

// Production Peer Discovery Flow:
// 1. Each validator's monitor registers their consumer chain endpoint when deployed
// 2. The registration includes external addresses accessible from other infrastructures
// 3. When starting consumer chains, monitors query the registry for peer addresses
// 4. Consumer chains connect to peers across different infrastructures

// Example usage in production:
// 
// Alice's infrastructure (AWS):
//   - Provider: alice.validators.com:26656
//   - Consumer: alice-consumer.validators.com:27656
//
// Bob's infrastructure (GCP):  
//   - Provider: bob-validator.example.org:26656
//   - Consumer: bob-consumer.example.org:27656
//
// Charlie's infrastructure (Bare metal):
//   - Provider: 203.0.113.10:26656
//   - Consumer: 203.0.113.10:27656