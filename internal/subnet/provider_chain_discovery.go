package subnet

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// ProviderChainDiscovery discovers consumer chain peers using provider chain data
type ProviderChainDiscovery struct {
	logger         *slog.Logger
	providerRPCURL string
	httpClient     *http.Client
}

// NewProviderChainDiscovery creates a new provider chain discovery service
func NewProviderChainDiscovery(logger *slog.Logger, providerRPCURL string) *ProviderChainDiscovery {
	return &ProviderChainDiscovery{
		logger:         logger,
		providerRPCURL: providerRPCURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Let's explore what's actually available from the provider chain

// ExploreAvailableData explores what peer discovery data we can get from the provider
func (pcd *ProviderChainDiscovery) ExploreAvailableData(ctx context.Context, consumerID string) error {
	pcd.logger.Info("Exploring available data from provider chain", "consumer_id", consumerID)
	
	// 1. Check if we can get validator info with connection details
	validators, err := pcd.getValidators(ctx)
	if err != nil {
		pcd.logger.Error("Failed to get validators", "error", err)
	} else {
		pcd.logger.Info("Got validators", "count", len(validators))
		for _, val := range validators {
			pcd.logger.Info("Validator info",
				"moniker", val.Description.Moniker,
				"operator", val.OperatorAddress,
				"status", val.Status)
		}
	}
	
	// 2. Check net_info for peer details
	netInfo, err := pcd.getNetInfo(ctx)
	if err != nil {
		pcd.logger.Error("Failed to get net_info", "error", err)
	} else {
		pcd.logger.Info("Got net_info",
			"self_moniker", netInfo.NodeInfo.Moniker,
			"peer_count", len(netInfo.Peers))
		
		for _, peer := range netInfo.Peers {
			pcd.logger.Info("Peer details",
				"moniker", peer.NodeInfo.Moniker,
				"remote_ip", peer.RemoteIP,
				"listen_addr", peer.NodeInfo.ListenAddr)
		}
	}
	
	// 3. Check if validators store consumer endpoints anywhere
	optedIn, err := pcd.getOptedInValidators(ctx, consumerID)
	if err != nil {
		pcd.logger.Error("Failed to get opted-in validators", "error", err)
	} else {
		pcd.logger.Info("Got opted-in validators", "addresses", optedIn)
	}
	
	return nil
}

// Validator represents staking validator info
type Validator struct {
	OperatorAddress string `json:"operator_address"`
	ConsensusPubkey struct {
		Type string `json:"@type"`
		Key  string `json:"key"`
	} `json:"consensus_pubkey"`
	Status      string `json:"status"`
	Description struct {
		Moniker  string `json:"moniker"`
		Identity string `json:"identity"`
		Website  string `json:"website"`
		Details  string `json:"details"`
	} `json:"description"`
}

// getValidators queries all validators from the provider chain
func (pcd *ProviderChainDiscovery) getValidators(ctx context.Context) ([]Validator, error) {
	url := fmt.Sprintf("%s/cosmos/staking/v1beta1/validators?status=BOND_STATUS_BONDED", pcd.providerRPCURL)
	
	resp, err := pcd.httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	
	var result struct {
		Validators []Validator `json:"validators"`
	}
	
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	
	return result.Validators, nil
}

// ProviderNetInfo from CometBFT
type ProviderNetInfo struct {
	Listening bool `json:"listening"`
	Listeners []string `json:"listeners"`
	NPeers    string `json:"n_peers"`
	Peers     []struct {
		NodeInfo struct {
			ID         string `json:"id"`
			Moniker    string `json:"moniker"`
			Network    string `json:"network"`
			ListenAddr string `json:"listen_addr"`
		} `json:"node_info"`
		RemoteIP string `json:"remote_ip"`
	} `json:"peers"`
	NodeInfo struct {
		ID         string `json:"id"`
		Moniker    string `json:"moniker"`
		Network    string `json:"network"`
		ListenAddr string `json:"listen_addr"`
	} `json:"node_info"`
}

// getNetInfo queries net_info from CometBFT RPC
func (pcd *ProviderChainDiscovery) getNetInfo(ctx context.Context) (*ProviderNetInfo, error) {
	url := fmt.Sprintf("%s/net_info", pcd.providerRPCURL)
	
	resp, err := pcd.httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	
	var result struct {
		Result ProviderNetInfo `json:"result"`
	}
	
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	
	return &result.Result, nil
}

// getOptedInValidators queries which validators opted into a consumer
func (pcd *ProviderChainDiscovery) getOptedInValidators(ctx context.Context, consumerID string) ([]string, error) {
	// Try the ICS query endpoint
	url := fmt.Sprintf("%s/interchain_security/ccv/provider/opted_in_validators/%s", pcd.providerRPCURL, consumerID)
	
	resp, err := pcd.httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("query failed: %s", string(body))
	}
	
	var result struct {
		OptedInValidators []string `json:"opted_in_validators"`
	}
	
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	
	return result.OptedInValidators, nil
}