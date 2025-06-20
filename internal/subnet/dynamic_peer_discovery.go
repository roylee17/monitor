package subnet

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// DynamicPeerDiscovery discovers peers using Kubernetes labels and actual node IDs
type DynamicPeerDiscovery struct {
	logger    *slog.Logger
	clientset kubernetes.Interface
}

// NewDynamicPeerDiscovery creates a new dynamic peer discovery service
func NewDynamicPeerDiscovery(logger *slog.Logger, clientset kubernetes.Interface) *DynamicPeerDiscovery {
	return &DynamicPeerDiscovery{
		logger:    logger,
		clientset: clientset,
	}
}

// DiscoverPeersForConsumer discovers peers for a consumer chain by reading actual node IDs
func (d *DynamicPeerDiscovery) DiscoverPeersForConsumer(ctx context.Context, chainID, namespace string) ([]string, error) {
	d.logger.Info("Discovering peers dynamically for consumer chain",
		"chain_id", chainID,
		"namespace", namespace)

	// Get all namespaces that match the consumer chain pattern
	namespaces, err := d.clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("chain-id=%s", chainID),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list namespaces: %w", err)
	}

	var peers []string
	
	for _, ns := range namespaces.Items {
		if ns.Name == namespace {
			// Skip self
			continue
		}

		// Get the node ID from the ConfigMap (if it exists)
		nodeID, err := d.getNodeIDFromNamespace(ctx, ns.Name, chainID)
		if err != nil {
			d.logger.Warn("Failed to get node ID from namespace",
				"namespace", ns.Name,
				"error", err)
			continue
		}

		// Get the P2P service port
		svc, err := d.clientset.CoreV1().Services(ns.Name).Get(ctx, chainID, metav1.GetOptions{})
		if err != nil {
			d.logger.Warn("Failed to get service",
				"namespace", ns.Name,
				"service", chainID,
				"error", err)
			continue
		}

		// Find P2P port
		var p2pPort int32
		for _, port := range svc.Spec.Ports {
			if port.Name == "p2p" {
				p2pPort = port.Port
				break
			}
		}

		if p2pPort == 0 {
			d.logger.Warn("No P2P port found in service",
				"namespace", ns.Name,
				"service", chainID)
			continue
		}

		// Build peer address
		peerAddr := fmt.Sprintf("%s@%s.%s.svc.cluster.local:%d", nodeID, chainID, ns.Name, p2pPort)
		peers = append(peers, peerAddr)
		
		d.logger.Info("Discovered peer",
			"namespace", ns.Name,
			"node_id", nodeID[:12]+"...",
			"address", peerAddr)
	}

	return peers, nil
}

// getNodeIDFromNamespace reads the node ID from a ConfigMap in the namespace
func (d *DynamicPeerDiscovery) getNodeIDFromNamespace(ctx context.Context, namespace, chainID string) (string, error) {
	configMapName := fmt.Sprintf("%s-node-info", chainID)
	
	cm, err := d.clientset.CoreV1().ConfigMaps(namespace).Get(ctx, configMapName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get ConfigMap: %w", err)
	}

	nodeID, ok := cm.Data["node-id"]
	if !ok {
		return "", fmt.Errorf("node-id not found in ConfigMap")
	}

	return strings.TrimSpace(nodeID), nil
}

// PublishNodeID publishes the node ID to a ConfigMap for discovery by other nodes
func (d *DynamicPeerDiscovery) PublishNodeID(ctx context.Context, namespace, chainID, nodeID string) error {
	configMapName := fmt.Sprintf("%s-node-info", chainID)
	
	configMap := &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: namespace,
			Labels: map[string]string{
				"chain-id": chainID,
				"purpose":  "node-discovery",
			},
		},
		Data: map[string]string{
			"node-id":   nodeID,
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		},
	}

	_, err := d.clientset.CoreV1().ConfigMaps(namespace).Create(ctx, configMap, metav1.CreateOptions{})
	if err != nil {
		// Try to update if it already exists
		_, err = d.clientset.CoreV1().ConfigMaps(namespace).Update(ctx, configMap, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create/update ConfigMap: %w", err)
		}
	}

	d.logger.Info("Published node ID for discovery",
		"namespace", namespace,
		"chain_id", chainID,
		"node_id", nodeID[:12]+"...")

	return nil
}

// WaitForPeers waits for a minimum number of peers to be available
func (d *DynamicPeerDiscovery) WaitForPeers(ctx context.Context, chainID, namespace string, minPeers int, timeout time.Duration) ([]string, error) {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				return nil, fmt.Errorf("timeout waiting for %d peers", minPeers)
			}

			peers, err := d.DiscoverPeersForConsumer(ctx, chainID, namespace)
			if err != nil {
				d.logger.Warn("Error discovering peers", "error", err)
				continue
			}

			if len(peers) >= minPeers {
				d.logger.Info("Found required number of peers",
					"chain_id", chainID,
					"required", minPeers,
					"found", len(peers))
				return peers, nil
			}

			d.logger.Debug("Waiting for more peers",
				"chain_id", chainID,
				"required", minPeers,
				"found", len(peers))
		}
	}
}