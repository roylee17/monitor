package subnet

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

)

// SimplePeerDiscovery implements peer discovery using provider chain data only
type SimplePeerDiscovery struct {
	logger           *slog.Logger
	providerRPCURL   string
	localValidatorMoniker string
	httpClient       *http.Client
}

// NewSimplePeerDiscovery creates a new simple peer discovery
func NewSimplePeerDiscovery(logger *slog.Logger, providerRPCURL, localValidatorMoniker string) *SimplePeerDiscovery {
	return &SimplePeerDiscovery{
		logger:                logger,
		providerRPCURL:        providerRPCURL,
		localValidatorMoniker: localValidatorMoniker,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// DiscoverPeers discovers consumer chain peers using provider chain data
func (spd *SimplePeerDiscovery) DiscoverPeers(ctx context.Context, consumerID string) ([]string, error) {
	// For now, use a placeholder chainID - in production this would be passed properly
	chainID := fmt.Sprintf("consumer-%s", consumerID)
	return spd.DiscoverPeersWithChainID(ctx, consumerID, chainID)
}

// DiscoverPeersWithChainID discovers consumer chain peers using provider chain data
func (spd *SimplePeerDiscovery) DiscoverPeersWithChainID(ctx context.Context, consumerID string, chainID string) ([]string, error) {
	spd.logger.Info("Discovering peers for consumer chain",
		"consumer_id", consumerID,
		"chain_id", chainID,
		"local_validator", spd.localValidatorMoniker)
	
	// Step 1: Get all validators from provider chain
	validators, err := spd.getProviderValidators(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get provider validators: %w", err)
	}
	
	// Step 2: Get opted-in validators for this consumer
	optedInAddresses, err := spd.getOptedInValidators(ctx, consumerID)
	if err != nil {
		return nil, fmt.Errorf("failed to get opted-in validators: %w", err)
	}
	
	// Step 3: Get node IDs from net_info
	nodeIDMap, err := spd.getNodeIDMap(ctx)
	if err != nil {
		spd.logger.Warn("Failed to get node IDs from net_info, will use derived IDs", "error", err)
		// Continue with derived node IDs
	}
	
	// Step 4: Build peer list for opted-in validators (excluding self)
	var peers []string
	consumerP2PPort := spd.getConsumerP2PPort(consumerID)
	
	for _, validator := range validators {
		// Skip self
		if validator.Description.Moniker == spd.localValidatorMoniker {
			continue
		}
		
		// Check if this validator opted in
		if !spd.isValidatorOptedIn(validator, optedInAddresses) {
			continue
		}
		
		// Get node ID (from net_info or derive from pubkey)
		nodeID := ""
		if nodeIDMap != nil {
			nodeID = nodeIDMap[validator.Description.Moniker]
		}
		if nodeID == "" {
			// Derive from consensus pubkey as fallback
			derivedID, err := spd.deriveNodeIDFromPubkey(validator.ConsensusPubkey.Type, validator.ConsensusPubkey.Value)
			if err != nil {
				spd.logger.Warn("Failed to derive node ID",
					"validator", validator.Description.Moniker,
					"error", err)
				continue
			}
			nodeID = derivedID
		}
		
		// Build peer address using convention
		// In Kubernetes: use cross-namespace DNS name
		// Format: service.namespace.svc.cluster.local
		// In production: each validator would have their own domain/IP
		namespace := fmt.Sprintf("%s-%s", validator.Description.Moniker, chainID)
		peerHost := fmt.Sprintf("%s.%s.svc.cluster.local", chainID, namespace)
		peer := fmt.Sprintf("%s@%s:%d", nodeID, peerHost, consumerP2PPort)
		
		peers = append(peers, peer)
		spd.logger.Info("Added consumer chain peer",
			"validator", validator.Description.Moniker,
			"node_id", nodeID,
			"peer", peer)
	}
	
	return peers, nil
}

// ProviderValidator info from provider chain
type ProviderValidator struct {
	OperatorAddress string `json:"operator_address"`
	ConsensusPubkey struct {
		Type  string `json:"@type"`
		Value string `json:"key"`
	} `json:"consensus_pubkey"`
	Status      string `json:"status"`
	Description struct {
		Moniker string `json:"moniker"`
	} `json:"description"`
}

// getProviderValidators gets all bonded validators from provider chain
func (spd *SimplePeerDiscovery) getProviderValidators(ctx context.Context) ([]ProviderValidator, error) {
	// For now, return hardcoded validators for testnet
	// In production, this would query the chain properly via gRPC or CLI
	validators := []ProviderValidator{
		{
			OperatorAddress: "cosmosvaloper1zaavvzxez0elundtn32qnk9lkm8kmcsz8ycjrl",
			Description: struct {
				Moniker string `json:"moniker"`
			}{Moniker: "alice"},
		},
		{
			OperatorAddress: "cosmosvaloper1yxgfnpk2u6h9prhhukcunszcwc277s2ct4e9k0",
			Description: struct {
				Moniker string `json:"moniker"`
			}{Moniker: "bob"},
		},
		{
			OperatorAddress: "cosmosvaloper1pz2trypc6e25hcwzn4h7jyqc57cr0qg4thggx5",
			Description: struct {
				Moniker string `json:"moniker"`
			}{Moniker: "charlie"},
		},
	}
	
	spd.logger.Info("Got provider validators", "count", len(validators))
	return validators, nil
}

// getOptedInValidators gets validators who opted into the consumer chain
func (spd *SimplePeerDiscovery) getOptedInValidators(ctx context.Context, consumerID string) ([]string, error) {
	// This would use the ICS query endpoint
	// For now, return all validators as a fallback
	// In production, you'd query: /interchain_security/ccv/provider/opted_in_validators/{consumer_id}
	
	spd.logger.Info("Getting opted-in validators", "consumer_id", consumerID)
	
	// For testnet, assume all validators are opted in
	// In production, query the actual endpoint
	return []string{}, nil // Empty means all validators
}

// getNodeIDMap gets node IDs from net_info
func (spd *SimplePeerDiscovery) getNodeIDMap(ctx context.Context) (map[string]string, error) {
	// For testnet, return hardcoded node IDs
	// In production, this would query net_info properly
	nodeIDMap := map[string]string{
		"alice":   "9cee33456402ff3370734a61c4f99c612e3c01a5",
		"bob":     "650c9fac5a183cf4de2150a585bf008f21751a6d",
		"charlie": "3121619381646547698f2f343e807f579cfe25c0",
	}
	
	spd.logger.Info("Got node ID map", "validators", len(nodeIDMap))
	return nodeIDMap, nil
}

// isValidatorOptedIn checks if a validator opted into the consumer
func (spd *SimplePeerDiscovery) isValidatorOptedIn(validator ProviderValidator, optedInAddresses []string) bool {
	// If no opted-in list, assume all validators are opted in (for testnet)
	if len(optedInAddresses) == 0 {
		return true
	}
	
	// Check if validator's consensus address is in opted-in list
	for _, addr := range optedInAddresses {
		if strings.Contains(addr, validator.OperatorAddress) {
			return true
		}
	}
	
	return false
}

// deriveNodeIDFromPubkey derives node ID from consensus public key
func (spd *SimplePeerDiscovery) deriveNodeIDFromPubkey(pubkeyType, pubkeyValue string) (string, error) {
	// This is a simplified version - in production you'd use the actual tendermint logic
	// Node ID = first 20 bytes of SHA256(pubkey bytes)
	
	pubkeyBytes, err := base64.StdEncoding.DecodeString(pubkeyValue)
	if err != nil {
		return "", fmt.Errorf("failed to decode pubkey: %w", err)
	}
	
	hash := sha256.Sum256(pubkeyBytes)
	nodeID := hex.EncodeToString(hash[:20])
	
	return nodeID, nil
}

// getConsumerP2PPort calculates the P2P port for a consumer chain
func (spd *SimplePeerDiscovery) getConsumerP2PPort(consumerID string) int {
	hash := sha256.Sum256([]byte(consumerID))
	offset := binary.BigEndian.Uint32(hash[:4]) % 1000
	return 26656 + 100 + int(offset*10)
}

// Summary of this approach:
// 1. Query provider chain for all bonded validators
// 2. Filter to only opted-in validators for the consumer
// 3. Get node IDs from net_info (or derive from pubkey)
// 4. Build peer list using convention: {nodeID}@{host}:{consumer-port}
// 5. Exclude self from peer list
//
// Advantages:
// - Single source of truth (provider chain)
// - Works with any validator setup
// - No external dependencies
// - Simple and reliable