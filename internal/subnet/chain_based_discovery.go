package subnet

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// ChainBasedPeerDiscovery uses provider chain APIs to discover consumer chain peers
type ChainBasedPeerDiscovery struct {
	logger           *slog.Logger
	providerRPCURL   string
	providerGRPCURL  string
	httpClient       *http.Client
}

// NewChainBasedPeerDiscovery creates a new chain-based peer discovery service
func NewChainBasedPeerDiscovery(logger *slog.Logger, providerRPCURL, providerGRPCURL string) *ChainBasedPeerDiscovery {
	return &ChainBasedPeerDiscovery{
		logger:          logger,
		providerRPCURL:  providerRPCURL,
		providerGRPCURL: providerGRPCURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// ValidatorNetInfo contains network information for a validator
type ValidatorNetInfo struct {
	Moniker         string
	OperatorAddress string
	NodeID          string
	ListenAddr      string
	// Consumer chain specific info
	ConsumerNodeID  string
	ConsumerP2PAddr string
}

// GetValidatorNodeIDs queries the provider chain for validator node IDs
func (cbd *ChainBasedPeerDiscovery) GetValidatorNodeIDs(ctx context.Context) (map[string]string, error) {
	// Method 1: Use CometBFT net_info endpoint to get connected peers
	netInfo, err := cbd.getNetInfo()
	if err != nil {
		return nil, fmt.Errorf("failed to get net info: %w", err)
	}
	
	nodeIDs := make(map[string]string)
	
	// Add self
	if netInfo.NodeInfo.Moniker != "" {
		nodeIDs[netInfo.NodeInfo.Moniker] = netInfo.NodeInfo.NodeID
		cbd.logger.Info("Found self node ID",
			"moniker", netInfo.NodeInfo.Moniker,
			"node_id", netInfo.NodeInfo.NodeID)
	}
	
	// Add peers
	for _, peer := range netInfo.Peers {
		if peer.NodeInfo.Moniker != "" {
			nodeIDs[peer.NodeInfo.Moniker] = peer.NodeInfo.NodeID
			cbd.logger.Info("Found peer node ID",
				"moniker", peer.NodeInfo.Moniker,
				"node_id", peer.NodeInfo.NodeID)
		}
	}
	
	return nodeIDs, nil
}

// GetOptedInValidators queries which validators have opted into a consumer chain
func (cbd *ChainBasedPeerDiscovery) GetOptedInValidators(ctx context.Context, consumerID string) ([]string, error) {
	// Query the provider chain for opted-in validators
	// This would use the interchain-security query endpoints
	
	url := fmt.Sprintf("%s/interchain_security/ccv/provider/opted_in_validators/%s", cbd.providerRPCURL, consumerID)
	
	resp, err := cbd.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to query opted-in validators: %w", err)
	}
	defer resp.Body.Close()
	
	var result struct {
		Validators []string `json:"validators"`
	}
	
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	
	cbd.logger.Info("Found opted-in validators",
		"consumer_id", consumerID,
		"count", len(result.Validators))
	
	return result.Validators, nil
}

// GetValidatorConsumerKey queries if a validator has assigned a consumer key
func (cbd *ChainBasedPeerDiscovery) GetValidatorConsumerKey(ctx context.Context, consumerID, validatorAddr string) (string, error) {
	// Query for assigned consumer key
	url := fmt.Sprintf("%s/interchain_security/ccv/provider/validator_consumer_pub_key/%s/%s",
		cbd.providerRPCURL, consumerID, validatorAddr)
	
	resp, err := cbd.httpClient.Get(url)
	if err != nil {
		return "", fmt.Errorf("failed to query consumer key: %w", err)
	}
	defer resp.Body.Close()
	
	var result struct {
		ConsumerPubKey struct {
			Key string `json:"key"`
		} `json:"consumer_pub_key"`
	}
	
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}
	
	return result.ConsumerPubKey.Key, nil
}

// NetInfo represents the CometBFT net_info response
type NetInfo struct {
	Listening bool     `json:"listening"`
	Listeners []string `json:"listeners"`
	NPeers    string   `json:"n_peers"`
	Peers     []Peer   `json:"peers"`
	NodeInfo  NodeInfo `json:"node_info"`
}

// Peer represents a peer in net_info
type Peer struct {
	NodeInfo         NodeInfo `json:"node_info"`
	IsOutbound       bool     `json:"is_outbound"`
	ConnectionStatus struct {
		Duration string `json:"duration"`
	} `json:"connection_status"`
	RemoteIP string `json:"remote_ip"`
}

// getNetInfo queries the CometBFT net_info endpoint
func (cbd *ChainBasedPeerDiscovery) getNetInfo() (*NetInfo, error) {
	url := fmt.Sprintf("%s/net_info", cbd.providerRPCURL)
	
	resp, err := cbd.httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	
	var result struct {
		Result NetInfo `json:"result"`
	}
	
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	
	return &result.Result, nil
}

// DiscoverConsumerPeersFromChain discovers consumer peers using on-chain information
func (cbd *ChainBasedPeerDiscovery) DiscoverConsumerPeersFromChain(
	ctx context.Context,
	consumerID string,
	localValidator string,
) ([]string, error) {
	// Step 1: Get opted-in validators
	optedInValidators, err := cbd.GetOptedInValidators(ctx, consumerID)
	if err != nil {
		cbd.logger.Warn("Failed to get opted-in validators", "error", err)
		// Continue with other methods
	}
	
	// Step 2: Get node IDs from net_info
	nodeIDs, err := cbd.GetValidatorNodeIDs(ctx)
	if err != nil {
		cbd.logger.Warn("Failed to get validator node IDs", "error", err)
	}
	
	// Step 3: Build peer list
	// In production, we would need additional info about consumer chain endpoints
	// This could be:
	// - Stored in validator's operator metadata on-chain
	// - Derived from a naming convention
	// - Retrieved from a gossip protocol
	
	var peers []string
	
	// For now, use a convention-based approach
	for _, valAddr := range optedInValidators {
		// Skip self
		if strings.Contains(valAddr, localValidator) {
			continue
		}
		
		// Try to find moniker for this validator address
		// In production, you'd query the staking module for this
		moniker := cbd.getValidatorMoniker(valAddr)
		
		if nodeID, exists := nodeIDs[moniker]; exists {
			// Use convention: validator-{moniker}:consumer_port
			peer := fmt.Sprintf("%s@validator-%s:%d", nodeID, moniker, GetConsumerP2PPort(consumerID))
			peers = append(peers, peer)
			
			cbd.logger.Info("Discovered consumer peer from chain",
				"validator", moniker,
				"node_id", nodeID,
				"peer", peer)
		}
	}
	
	return peers, nil
}

// getValidatorMoniker is a placeholder - in production this would query the staking module
func (cbd *ChainBasedPeerDiscovery) getValidatorMoniker(validatorAddr string) string {
	// This is simplified - in production you would:
	// 1. Query /cosmos/staking/v1beta1/validators/{validatorAddr}
	// 2. Extract the moniker from the description
	
	// For now, use a simple mapping
	if strings.Contains(validatorAddr, "zaavvzxez0elundtn32qnk9lkm8kmcsz") {
		return "alice"
	} else if strings.Contains(validatorAddr, "pz2trypc6e25hcwzn4h7jyqc57cr0qg4") {
		return "charlie"  
	} else if strings.Contains(validatorAddr, "5puw2h47jqe327vy0pjqgslq9gznl0cn") {
		return "bob"
	}
	
	return ""
}

// GetConsumerP2PPort calculates the consumer chain P2P port
func GetConsumerP2PPort(consumerID string) int {
	// Same logic as before
	hash := sha256.Sum256([]byte(consumerID))
	offset := binary.BigEndian.Uint32(hash[:4]) % 1000
	return 26656 + 100 + int(offset*10)
}

// Enhanced approach using validator metadata stored on-chain
type ValidatorConsumerMetadata struct {
	ConsumerEndpoints map[string]ConsumerEndpoint `json:"consumer_endpoints"`
}

type ConsumerEndpoint struct {
	P2PAddress string `json:"p2p_address"`
	RPCAddress string `json:"rpc_address"`
	NodeID     string `json:"node_id"`
}

// GetValidatorMetadata queries validator metadata from chain
func (cbd *ChainBasedPeerDiscovery) GetValidatorMetadata(ctx context.Context, validatorAddr string) (*ValidatorConsumerMetadata, error) {
	// In ICS v7+, validators could store consumer endpoints in their operator metadata
	// This would be queryable via:
	// /cosmos/staking/v1beta1/validators/{validatorAddr}
	
	// For now, return empty - this shows how it could work
	return &ValidatorConsumerMetadata{
		ConsumerEndpoints: make(map[string]ConsumerEndpoint),
	}, nil
}