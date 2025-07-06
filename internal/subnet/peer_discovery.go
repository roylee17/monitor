package subnet

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

// PeerDiscovery discovers peers using deterministic node IDs
type PeerDiscovery struct {
	logger                *slog.Logger
	nodeKeyGen            *NodeKeyGenerator
	localValidatorMoniker string
	validatorEndpoints    map[string]string // Map of validator moniker to registered P2P endpoint
}

// NewPeerDiscovery creates a new peer discovery service
func NewPeerDiscovery(logger *slog.Logger, localValidatorMoniker string) *PeerDiscovery {
	return &PeerDiscovery{
		logger:                logger,
		nodeKeyGen:            NewNodeKeyGenerator(),
		localValidatorMoniker: localValidatorMoniker,
		validatorEndpoints:    make(map[string]string),
	}
}


// SetValidatorEndpoints sets the validator P2P endpoints from chain registry
func (d *PeerDiscovery) SetValidatorEndpoints(endpoints map[string]string) {
	d.validatorEndpoints = endpoints
	d.logger.Info("Updated validator endpoints",
		"count", len(endpoints))
}

// GetValidatorEndpoints returns the current validator P2P endpoints
func (d *PeerDiscovery) GetValidatorEndpoints() map[string]string {
	// Return a copy to prevent external modifications
	result := make(map[string]string)
	for k, v := range d.validatorEndpoints {
		result[k] = v
	}
	return result
}

// DiscoverPeersWithChainID discovers peers for a consumer chain using LoadBalancer-based discovery
func (d *PeerDiscovery) DiscoverPeersWithChainID(ctx context.Context, consumerID string, chainID string, optedInValidators []string) ([]string, error) {
	d.logger.Info("Discovering peers for consumer chain",
		"consumer_id", consumerID,
		"chain_id", chainID,
		"opted_in_validators", len(optedInValidators))

	// Always use LoadBalancer-based discovery
	return d.discoverPeersWithLoadBalancer(ctx, consumerID, chainID, optedInValidators)
}

// GetNodeKeyJSON returns the node key JSON for this validator
func (d *PeerDiscovery) GetNodeKeyJSON(chainID string) ([]byte, error) {
	return d.nodeKeyGen.GenerateNodeKeyJSON(d.localValidatorMoniker, chainID)
}

// GetPeersForConsumerChain returns the persistent peers for a consumer chain at the given port
// This is used by the consumer chain updater when updating peer configurations
func (d *PeerDiscovery) GetPeersForConsumerChain(chainID string, port int) ([]string, error) {
	// Get all validators from endpoints (we don't know which ones are opted in)
	// In a real implementation, we would query the provider chain for opted-in validators
	var peers []string
	
	for validatorName, endpoint := range d.validatorEndpoints {
		// Skip self
		if validatorName == d.localValidatorMoniker {
			continue
		}
		
		// Calculate deterministic node ID
		nodeID, err := d.nodeKeyGen.GetNodeID(validatorName, chainID)
		if err != nil {
			d.logger.Warn("Failed to calculate node ID",
				"validator", validatorName,
				"error", err)
			continue
		}
		
		// Build peer address
		peer := fmt.Sprintf("%s@%s:%d", nodeID, endpoint, port)
		peers = append(peers, peer)
	}
	
	return peers, nil
}



// discoverPeersWithLoadBalancer discovers peers for a consumer chain using LoadBalancer addresses
func (d *PeerDiscovery) discoverPeersWithLoadBalancer(_ context.Context, consumerID string, chainID string, optedInValidators []string) ([]string, error) {
	d.logger.Info("Discovering peers through LoadBalancer",
		"consumer_id", consumerID,
		"chain_id", chainID,
		"opted_in_validators", len(optedInValidators))

	var peers []string
	
	// Calculate the consumer chain's P2P port (not NodePort!)
	consumerP2PPort := d.getConsumerP2PPort(chainID)
	d.logger.Info("Calculated consumer P2P port",
		"chain_id", chainID,
		"p2p_port", consumerP2PPort)
	
	for _, validatorName := range optedInValidators {
		// Skip self - we don't need to connect to our own gateway
		if validatorName == d.localValidatorMoniker {
			continue
		}

		// IMPORTANT: Only include validators that are actually running consumer chains
		// In production, this should verify the validator has actually deployed
		// For now, we trust the optedInValidators list is accurate
		
		// Calculate deterministic node ID
		nodeID, err := d.nodeKeyGen.GetNodeID(validatorName, chainID)
		if err != nil {
			d.logger.Warn("Failed to calculate node ID",
				"validator", validatorName,
				"error", err)
			continue
		}

		// Build peer address using LoadBalancer
		var peer string
		
		// Check if we have an endpoint from the on-chain registry
		endpoint, ok := d.validatorEndpoints[validatorName]
		if !ok {
			d.logger.Warn("No endpoint found in on-chain registry for validator",
				"validator", validatorName)
			continue
		}
		
		// Extract host from endpoint (remove any existing port)
		host := endpoint
		// Find last colon to separate host:port
		if idx := strings.LastIndex(endpoint, ":"); idx != -1 {
			host = endpoint[:idx]
		}
		
		// Build peer address with consumer P2P port for LoadBalancer connection
		peer = fmt.Sprintf("%s@%s:%d", nodeID, host, consumerP2PPort)

		peers = append(peers, peer)
		d.logger.Info("Added LoadBalancer peer",
			"validator", validatorName,
			"node_id", nodeID[:12]+"...",
			"host", host,
			"port", consumerP2PPort,
			"peer", peer)
	}

	d.logger.Info("LoadBalancer peer discovery complete",
		"chain_id", chainID,
		"total_peers", len(peers))

	return peers, nil
}

// getConsumerP2PPort calculates the P2P port for a consumer chain
func (d *PeerDiscovery) getConsumerP2PPort(chainID string) int {
	// Use the same calculation as CalculatePorts to ensure consistency
	ports, err := CalculatePorts(chainID)
	if err != nil {
		// Fallback to a default if calculation fails
		d.logger.Warn("Failed to calculate ports, using default", "error", err)
		return 26756 // BaseP2PPort + ConsumerOffset
	}
	return ports.P2P
}

