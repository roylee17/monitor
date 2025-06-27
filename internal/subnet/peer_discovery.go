package subnet

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	rpcclient "github.com/cometbft/cometbft/rpc/client/http"
)

// PeerDiscovery discovers peers using deterministic node IDs
type PeerDiscovery struct {
	logger                *slog.Logger
	nodeKeyGen            *NodeKeyGenerator
	localValidatorMoniker string
	useGateway            bool              // Whether to use gateway addresses for cross-cluster connectivity
	rpcClient             *rpcclient.HTTP   // RPC client to query provider network info
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

// SetRPCClient sets the RPC client for querying provider network info
func (d *PeerDiscovery) SetRPCClient(client *rpcclient.HTTP) {
	d.rpcClient = client
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

// DiscoverPeersWithChainID discovers peers for a consumer chain using deterministic node IDs
func (d *PeerDiscovery) DiscoverPeersWithChainID(ctx context.Context, consumerID string, chainID string, optedInValidators []string) ([]string, error) {
	d.logger.Info("Discovering peers for consumer chain",
		"consumer_id", consumerID,
		"chain_id", chainID,
		"opted_in_validators", len(optedInValidators))

	// If we have an RPC client, query provider network info to get actual peer addresses
	if d.rpcClient != nil {
		return d.discoverPeersFromProviderNetwork(ctx, chainID, optedInValidators)
	}

	// Fall back to gateway discovery if enabled
	if d.useGateway {
		return d.DiscoverPeersWithGateway(ctx, consumerID, chainID, optedInValidators)
	}

	// Default to DNS-based discovery (single cluster mode)
	return d.discoverPeersWithDNS(ctx, consumerID, chainID, optedInValidators)
}

// GetNodeKeyJSON returns the node key JSON for this validator
func (d *PeerDiscovery) GetNodeKeyJSON(chainID string) ([]byte, error) {
	return d.nodeKeyGen.GenerateNodeKeyJSON(d.localValidatorMoniker, chainID)
}

// SetGatewayMode configures peer discovery to use gateway addresses
func (d *PeerDiscovery) SetGatewayMode(enabled bool) {
	d.useGateway = enabled
}


// DiscoverPeersWithGateway discovers peers for a consumer chain using LoadBalancer addresses
func (d *PeerDiscovery) DiscoverPeersWithGateway(ctx context.Context, consumerID string, chainID string, optedInValidators []string) ([]string, error) {
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

// discoverPeersFromProviderNetwork queries the provider's network info and adapts it for consumer chains
func (d *PeerDiscovery) discoverPeersFromProviderNetwork(ctx context.Context, chainID string, optedInValidators []string) ([]string, error) {
	d.logger.Info("Discovering peers from provider network info")

	// For multi-cluster setup, we need to use LoadBalancer addresses
	// Each validator's consumer P2P port is exposed via LoadBalancer
	if d.useGateway {
		return d.DiscoverPeersWithGateway(ctx, "", chainID, optedInValidators)
	}

	// Query provider's network info
	netInfo, err := d.rpcClient.NetInfo(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to query provider network info: %w", err)
	}

	// Calculate consumer P2P port
	consumerP2PPort := d.getConsumerP2PPort(chainID)

	// Build a map of opted-in validators for quick lookup
	optedInMap := make(map[string]bool)
	for _, validator := range optedInValidators {
		optedInMap[validator] = true
	}

	var peers []string
	processedValidators := make(map[string]bool)

	// Process each peer from provider network
	for _, peer := range netInfo.Peers {
		// Extract moniker from peer info
		moniker := peer.NodeInfo.Moniker
		
		// Skip if not opted in
		if !optedInMap[moniker] {
			continue
		}

		// Skip self
		if moniker == d.localValidatorMoniker {
			continue
		}

		// Skip if already processed (in case of duplicates)
		if processedValidators[moniker] {
			continue
		}
		processedValidators[moniker] = true

		// Calculate deterministic node ID for consumer chain
		consumerNodeID, err := d.nodeKeyGen.GetNodeID(moniker, chainID)
		if err != nil {
			d.logger.Warn("Failed to calculate consumer node ID",
				"validator", moniker,
				"error", err)
			continue
		}

		// Extract host from remote address
		// Remote address format is typically "tcp://host:port" or "host:port"
		remoteAddr := peer.RemoteIP
		
		// Build consumer peer address using same host but consumer port
		consumerPeer := fmt.Sprintf("%s@%s:%d", consumerNodeID, remoteAddr, consumerP2PPort)
		peers = append(peers, consumerPeer)

		d.logger.Info("Added consumer peer from provider network",
			"validator", moniker,
			"provider_node_id", peer.NodeInfo.ID()[:12]+"...",
			"consumer_node_id", consumerNodeID[:12]+"...",
			"host", remoteAddr,
			"consumer_port", consumerP2PPort)
	}

	// Check if we found peers for all opted-in validators
	for _, validator := range optedInValidators {
		if validator != d.localValidatorMoniker && !processedValidators[validator] {
			d.logger.Warn("Could not find provider peer info for opted-in validator",
				"validator", validator)
		}
	}

	d.logger.Info("Provider network-based peer discovery complete",
		"chain_id", chainID,
		"total_peers", len(peers))

	return peers, nil
}

// discoverPeersWithNodePorts discovers peers using NodePort mappings for multi-cluster setup
func (d *PeerDiscovery) discoverPeersWithNodePorts(ctx context.Context, chainID string, optedInValidators []string) ([]string, error) {
	d.logger.Info("Discovering peers using validator registry endpoints",
		"chain_id", chainID,
		"opted_in_validators", len(optedInValidators))

	var peers []string
	for _, validatorName := range optedInValidators {
		// Skip self
		if validatorName == d.localValidatorMoniker {
			continue
		}

		// Check validator registry
		endpoint, ok := d.validatorEndpoints[validatorName]
		if !ok {
			d.logger.Error("No endpoint found for validator in on-chain registry",
				"validator", validatorName,
				"registry_endpoints", len(d.validatorEndpoints))
			continue
		}

		// Extract host from endpoint (remove any existing port)
		hostAddr := endpoint
		if idx := strings.LastIndex(endpoint, ":"); idx != -1 {
			hostAddr = endpoint[:idx]
		}

		// Calculate deterministic node ID for consumer chain
		consumerNodeID, err := d.nodeKeyGen.GetNodeID(validatorName, chainID)
		if err != nil {
			d.logger.Warn("Failed to calculate consumer node ID",
				"validator", validatorName,
				"error", err)
			continue
		}

		// Calculate the unique NodePort for this validator's consumer chain
		validatorNodePort := CalculateValidatorNodePort(chainID, validatorName)

		peer := fmt.Sprintf("%s@%s:%d", consumerNodeID, hostAddr, validatorNodePort)
		peers = append(peers, peer)

		d.logger.Info("Added peer from validator registry",
			"validator", validatorName,
			"consumer_node_id", consumerNodeID[:12]+"...",
			"host", hostAddr,
			"nodeport", validatorNodePort,
			"peer", peer)
	}

	d.logger.Info("Peer discovery complete using validator registry",
		"chain_id", chainID,
		"total_peers", len(peers))

	return peers, nil
}

// discoverPeersWithDNS is the original DNS-based discovery (single cluster mode)
func (d *PeerDiscovery) discoverPeersWithDNS(ctx context.Context, consumerID string, chainID string, optedInValidators []string) ([]string, error) {
	d.logger.Info("Using DNS-based peer discovery (single cluster mode)")

	var peers []string
	consumerP2PPort := d.getConsumerP2PPort(chainID)

	for _, validatorName := range optedInValidators {
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

		// Build peer address using DNS
		namespace := chainID
		peerHost := fmt.Sprintf(ServiceDNSFormat, chainID, namespace)
		peer := fmt.Sprintf("%s@%s:%d", nodeID, peerHost, consumerP2PPort)

		peers = append(peers, peer)
		d.logger.Info("Added DNS peer",
			"validator", validatorName,
			"node_id", nodeID[:12]+"...",
			"peer", peer)
	}

	return peers, nil
}