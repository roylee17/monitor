package subnet

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"log/slog"
)

// DeterministicPeerDiscovery discovers peers using deterministic node IDs
type DeterministicPeerDiscovery struct {
	logger                *slog.Logger
	nodeKeyGen            *NodeKeyGenerator
	localValidatorMoniker string
}

// NewDeterministicPeerDiscovery creates a new deterministic peer discovery service
func NewDeterministicPeerDiscovery(logger *slog.Logger, localValidatorMoniker string) *DeterministicPeerDiscovery {
	return &DeterministicPeerDiscovery{
		logger:                logger,
		nodeKeyGen:            NewNodeKeyGenerator(),
		localValidatorMoniker: localValidatorMoniker,
	}
}

// DiscoverPeersWithChainID discovers peers for a consumer chain using deterministic node IDs
func (d *DeterministicPeerDiscovery) DiscoverPeersWithChainID(ctx context.Context, consumerID string, chainID string, optedInValidators []string) ([]string, error) {
	d.logger.Info("Discovering peers using deterministic node IDs",
		"consumer_id", consumerID,
		"chain_id", chainID,
		"opted_in_validators", len(optedInValidators))

	var peers []string
	consumerP2PPort := d.getConsumerP2PPort(consumerID)

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

		// Build peer address using cross-namespace DNS
		namespace := fmt.Sprintf("%s-%s", validatorName, chainID)
		peerHost := fmt.Sprintf("%s.%s.svc.cluster.local", chainID, namespace)
		peer := fmt.Sprintf("%s@%s:%d", nodeID, peerHost, consumerP2PPort)

		peers = append(peers, peer)
		d.logger.Info("Added deterministic peer",
			"validator", validatorName,
			"node_id", nodeID[:12]+"...",
			"peer", peer)
	}

	d.logger.Info("Peer discovery complete",
		"chain_id", chainID,
		"total_peers", len(peers))

	return peers, nil
}

// GetNodeKeyJSON returns the deterministic node key JSON for this validator
func (d *DeterministicPeerDiscovery) GetNodeKeyJSON(chainID string) ([]byte, error) {
	return d.nodeKeyGen.GenerateNodeKeyJSON(d.localValidatorMoniker, chainID)
}

// getConsumerP2PPort calculates the P2P port for a consumer chain
func (d *DeterministicPeerDiscovery) getConsumerP2PPort(consumerID string) int {
	hash := sha256.Sum256([]byte(consumerID))
	offset := binary.BigEndian.Uint32(hash[:4]) % 1000
	return 26656 + 100 + int(offset*10)
}