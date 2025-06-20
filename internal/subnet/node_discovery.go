package subnet

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// NodeInfo represents the node information from status endpoint
type NodeInfo struct {
	NodeID     string `json:"id"`
	ListenAddr string `json:"listen_addr"`
	Network    string `json:"network"`
	Version    string `json:"version"`
	Moniker    string `json:"moniker"`
}

// StatusResponse represents the response from /status endpoint
type StatusResponse struct {
	NodeInfo NodeInfo `json:"node_info"`
}

// NodeDiscovery handles discovery of node IDs from validators
type NodeDiscovery struct {
	logger     *slog.Logger
	httpClient *http.Client
}

// NewNodeDiscovery creates a new node discovery service
func NewNodeDiscovery(logger *slog.Logger) *NodeDiscovery {
	return &NodeDiscovery{
		logger: logger,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// GetNodeID queries a validator's RPC endpoint for its node ID
func (nd *NodeDiscovery) GetNodeID(rpcEndpoint string) (string, error) {
	// Ensure endpoint has http:// prefix
	if rpcEndpoint[:4] != "http" {
		rpcEndpoint = "http://" + rpcEndpoint
	}
	
	// Query the status endpoint
	resp, err := nd.httpClient.Get(rpcEndpoint + "/status")
	if err != nil {
		return "", fmt.Errorf("failed to query status: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status endpoint returned %d", resp.StatusCode)
	}
	
	var status StatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return "", fmt.Errorf("failed to decode status response: %w", err)
	}
	
	nd.logger.Info("Retrieved node ID",
		"endpoint", rpcEndpoint,
		"node_id", status.NodeInfo.NodeID,
		"moniker", status.NodeInfo.Moniker)
	
	return status.NodeInfo.NodeID, nil
}

// GetNodeIDs queries multiple validators for their node IDs
func (nd *NodeDiscovery) GetNodeIDs(validators map[string]string) map[string]string {
	nodeIDs := make(map[string]string)
	
	for validator, endpoint := range validators {
		nodeID, err := nd.GetNodeID(endpoint)
		if err != nil {
			nd.logger.Warn("Failed to get node ID",
				"validator", validator,
				"endpoint", endpoint,
				"error", err)
			continue
		}
		nodeIDs[validator] = nodeID
	}
	
	return nodeIDs
}

// GetNodeIDsFromContext gets node IDs using the current context
func (nd *NodeDiscovery) GetNodeIDsFromContext(ctx context.Context, validators []string) map[string]string {
	nodeIDs := make(map[string]string)
	
	// In Kubernetes environment, construct service endpoints
	for _, validator := range validators {
		endpoint := fmt.Sprintf("validator-%s.provider.svc.cluster.local:26657", validator)
		
		select {
		case <-ctx.Done():
			nd.logger.Warn("Context cancelled during node discovery")
			return nodeIDs
		default:
			nodeID, err := nd.GetNodeID(endpoint)
			if err != nil {
				nd.logger.Warn("Failed to get node ID",
					"validator", validator,
					"error", err)
				continue
			}
			nodeIDs[validator] = nodeID
		}
	}
	
	return nodeIDs
}