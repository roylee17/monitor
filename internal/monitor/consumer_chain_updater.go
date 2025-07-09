package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/sourcenetwork/ics-operator/internal/subnet"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// ConsumerChainUpdater handles updating consumer chain configurations when validator endpoints change
type ConsumerChainUpdater struct {
	logger           *slog.Logger
	clientset        kubernetes.Interface
	consumerRegistry *ConsumerRegistry
	peerDiscovery    *subnet.PeerDiscovery
	k8sManager       *subnet.K8sManager
	httpClient       *http.Client
	useHybridUpdate  bool // Enable hybrid RPC+restart updates
}

// NewConsumerChainUpdater creates a new consumer chain updater
func NewConsumerChainUpdater(
	logger *slog.Logger,
	clientset kubernetes.Interface,
	consumerRegistry *ConsumerRegistry,
	peerDiscovery *subnet.PeerDiscovery,
	k8sManager *subnet.K8sManager,
) *ConsumerChainUpdater {
	return &ConsumerChainUpdater{
		logger:           logger,
		clientset:        clientset,
		consumerRegistry: consumerRegistry,
		peerDiscovery:    peerDiscovery,
		k8sManager:       k8sManager,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		useHybridUpdate: false, // Default to restart-only for safety
	}
}

// UpdatedValidator represents a validator whose endpoint has changed
type UpdatedValidator struct {
	Name        string
	OldEndpoint string
	NewEndpoint string
}

// UpdateConsumerChainsForValidators updates all consumer chains affected by validator endpoint changes
func (u *ConsumerChainUpdater) UpdateConsumerChainsForValidators(ctx context.Context, updatedValidators []UpdatedValidator) error {
	u.logger.Info("Starting consumer chain updates for validator endpoint changes",
		"updated_validators", len(updatedValidators))

	// Track which consumer chains need updates
	affectedChains := make(map[string][]UpdatedValidator)

	// Find all affected consumer chains
	for _, validator := range updatedValidators {
		consumers := u.consumerRegistry.GetConsumersByValidator(validator.Name)
		for _, consumerChainID := range consumers {
			affectedChains[consumerChainID] = append(affectedChains[consumerChainID], validator)
		}
	}

	if len(affectedChains) == 0 {
		u.logger.Info("No consumer chains affected by validator endpoint changes")
		return nil
	}

	u.logger.Info("Found affected consumer chains",
		"chain_count", len(affectedChains))

	// Update each affected consumer chain
	var updateErrors []error
	for consumerChainID, validators := range affectedChains {
		u.logger.Info("Updating consumer chain",
			"consumer_chain_id", consumerChainID,
			"affected_validators", len(validators))

		if err := u.updateConsumerChain(ctx, consumerChainID, validators); err != nil {
			u.logger.Error("Failed to update consumer chain",
				"consumer_chain_id", consumerChainID,
				"error", err)
			updateErrors = append(updateErrors, fmt.Errorf("chain %s: %w", consumerChainID, err))
		}
	}

	if len(updateErrors) > 0 {
		return fmt.Errorf("failed to update %d consumer chains: %v", len(updateErrors), updateErrors)
	}

	return nil
}

// EnableHybridUpdate enables or disables hybrid RPC+restart updates
func (u *ConsumerChainUpdater) EnableHybridUpdate(enabled bool) {
	u.useHybridUpdate = enabled
	if enabled {
		u.logger.Info("Hybrid peer updates ENABLED (RPC first, restart fallback)")
	} else {
		u.logger.Info("Hybrid peer updates DISABLED (restart-only)")
	}
}

// updateConsumerChain updates a single consumer chain's peer configuration
func (u *ConsumerChainUpdater) updateConsumerChain(ctx context.Context, consumerChainID string, updatedValidators []UpdatedValidator) error {
	// Get all validators for this consumer chain
	validators := u.consumerRegistry.GetValidatorsByConsumer(consumerChainID)
	if len(validators) == 0 {
		return fmt.Errorf("no validators found for consumer chain")
	}

	// For each validator namespace, update the init script ConfigMap
	var updateErrors []error
	for _, validatorName := range validators {
		namespace := fmt.Sprintf("%s-%s", validatorName, consumerChainID)

		u.logger.Debug("Updating consumer chain in namespace",
			"namespace", namespace,
			"validator", validatorName,
			"hybrid_mode", u.useHybridUpdate)

		// Always update the init script ConfigMap for persistence
		if err := u.updateInitScriptConfigMap(ctx, namespace, consumerChainID); err != nil {
			u.logger.Error("Failed to update init script ConfigMap",
				"namespace", namespace,
				"error", err)
			updateErrors = append(updateErrors, err)
			continue
		}

		// Try hybrid update if enabled
		if u.useHybridUpdate {
			if err := u.hybridUpdatePeers(ctx, namespace, consumerChainID); err != nil {
				u.logger.Warn("Hybrid update failed, falling back to restart",
					"namespace", namespace,
					"error", err)
				// Fall back to restart
				if err := u.restartConsumerChain(ctx, namespace); err != nil {
					u.logger.Error("Failed to restart consumer chain",
						"namespace", namespace,
						"error", err)
					updateErrors = append(updateErrors, err)
				}
			} else {
				u.logger.Info("Hybrid update completed successfully - no restart needed",
					"namespace", namespace)
			}
		} else {
			// Traditional restart-based update
			if err := u.restartConsumerChain(ctx, namespace); err != nil {
				u.logger.Error("Failed to restart consumer chain",
					"namespace", namespace,
					"error", err)
				updateErrors = append(updateErrors, err)
			}
		}
	}

	if len(updateErrors) > 0 {
		return fmt.Errorf("failed to update %d validators: %v", len(updateErrors), updateErrors)
	}

	return nil
}

// updateInitScriptConfigMap updates the persistent_peers in the init script ConfigMap
func (u *ConsumerChainUpdater) updateInitScriptConfigMap(ctx context.Context, namespace, chainID string) error {
	configMapName := "init-script"

	// Get the existing ConfigMap
	cm, err := u.clientset.CoreV1().ConfigMaps(namespace).Get(ctx, configMapName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get ConfigMap: %w", err)
	}

	// Get the init script content
	initScript, ok := cm.Data["init.sh"]
	if !ok {
		return fmt.Errorf("init.sh not found in ConfigMap")
	}

	// Generate new peer list
	ports, err := subnet.CalculatePorts(chainID)
	if err != nil {
		return fmt.Errorf("failed to calculate ports: %w", err)
	}
	peers, err := u.peerDiscovery.GetPeersForConsumerChain(chainID, ports.P2P)
	if err != nil {
		return fmt.Errorf("failed to get peers: %w", err)
	}

	// Replace the persistent_peers line in the script
	lines := strings.Split(initScript, "\n")
	for i, line := range lines {
		if strings.Contains(line, "persistent_peers=") {
			// Build new peer line
			newPeerLine := fmt.Sprintf("persistent_peers=\"%s\"", strings.Join(peers, ","))
			lines[i] = newPeerLine
			u.logger.Info("Updated persistent_peers",
				"namespace", namespace,
				"old_line", line,
				"new_line", newPeerLine)
			break
		}
	}

	// Update the ConfigMap
	cm.Data["init.sh"] = strings.Join(lines, "\n")

	_, err = u.clientset.CoreV1().ConfigMaps(namespace).Update(ctx, cm, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update ConfigMap: %w", err)
	}

	u.logger.Info("Successfully updated init script ConfigMap",
		"namespace", namespace,
		"peer_count", len(peers))

	return nil
}

// restartConsumerChain performs a rolling restart of the consumer chain pod
func (u *ConsumerChainUpdater) restartConsumerChain(ctx context.Context, namespace string) error {
	// List pods in the namespace
	pods, err := u.clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app=validator",
	})
	if err != nil {
		return fmt.Errorf("failed to list pods: %w", err)
	}

	if len(pods.Items) == 0 {
		return fmt.Errorf("no validator pods found in namespace")
	}

	// Delete each pod to trigger a restart
	for _, pod := range pods.Items {
		u.logger.Info("Restarting consumer chain pod",
			"namespace", namespace,
			"pod", pod.Name)

		// Delete the pod
		err := u.clientset.CoreV1().Pods(namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{})
		if err != nil && !errors.IsNotFound(err) {
			return fmt.Errorf("failed to delete pod %s: %w", pod.Name, err)
		}

		// Wait for pod to be recreated and become ready
		if err := u.waitForPodReady(ctx, namespace, pod.Labels); err != nil {
			return fmt.Errorf("pod failed to become ready after restart: %w", err)
		}
	}

	return nil
}

// waitForPodReady waits for a pod with given labels to become ready
func (u *ConsumerChainUpdater) waitForPodReady(ctx context.Context, namespace string, labels map[string]string) error {
	// Build label selector
	var labelParts []string
	for k, v := range labels {
		labelParts = append(labelParts, fmt.Sprintf("%s=%s", k, v))
	}
	labelSelector := strings.Join(labelParts, ",")

	// Wait up to 5 minutes for pod to be ready
	timeout := 5 * time.Minute
	checkInterval := 5 * time.Second
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		// Check if context is cancelled
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// List pods with the label selector
		pods, err := u.clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: labelSelector,
		})
		if err != nil {
			return fmt.Errorf("failed to list pods: %w", err)
		}

		// Check if any pod is ready
		for _, pod := range pods.Items {
			if isPodReady(&pod) {
				u.logger.Info("Pod is ready after restart",
					"namespace", namespace,
					"pod", pod.Name)
				return nil
			}
		}

		u.logger.Debug("Waiting for pod to become ready",
			"namespace", namespace,
			"remaining", deadline.Sub(time.Now()))

		time.Sleep(checkInterval)
	}

	return fmt.Errorf("timeout waiting for pod to become ready")
}

// isPodReady checks if a pod is in Ready condition
func isPodReady(pod *corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

// HealthCheck performs a health check on a consumer chain after update
func (u *ConsumerChainUpdater) HealthCheck(ctx context.Context, namespace string) error {
	// Get the validator pod
	pods, err := u.clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app=validator",
	})
	if err != nil {
		return fmt.Errorf("failed to list pods: %w", err)
	}

	if len(pods.Items) == 0 {
		return fmt.Errorf("no validator pods found")
	}

	pod := &pods.Items[0]

	// Check if pod is ready
	if !isPodReady(pod) {
		return fmt.Errorf("pod is not ready")
	}

	// Could add additional health checks here:
	// - Check if the chain is producing blocks
	// - Check if peers are connected
	// - Check consensus participation

	u.logger.Info("Consumer chain health check passed",
		"namespace", namespace,
		"pod", pod.Name)

	return nil
}

// TendermintNetInfo represents the net_info RPC response
type TendermintNetInfo struct {
	NPeers string `json:"n_peers"`
	Peers  []struct {
		NodeInfo struct {
			ID      string `json:"id"`
			Network string `json:"network"`
			Moniker string `json:"moniker"`
		} `json:"node_info"`
		RemoteIP string `json:"remote_ip"`
	} `json:"peers"`
}

// hybridUpdatePeers attempts to update peers via RPC without restart
func (u *ConsumerChainUpdater) hybridUpdatePeers(ctx context.Context, namespace, chainID string) error {
	// Get pod RPC endpoint
	podIP, err := u.getPodRPCEndpoint(ctx, namespace)
	if err != nil {
		return fmt.Errorf("failed to get pod RPC endpoint: %w", err)
	}

	// Get current peers via RPC
	currentPeers, err := u.getCurrentPeersViaRPC(podIP)
	if err != nil {
		return fmt.Errorf("failed to get current peers: %w", err)
	}

	// Calculate new peer list
	ports, err := subnet.CalculatePorts(chainID)
	if err != nil {
		return fmt.Errorf("failed to calculate ports: %w", err)
	}
	newPeers, err := u.peerDiscovery.GetPeersForConsumerChain(chainID, ports.P2P)
	if err != nil {
		return fmt.Errorf("failed to get new peers: %w", err)
	}

	// Determine peers to add
	toAdd := u.calculatePeersToAdd(currentPeers, newPeers)

	u.logger.Info("Hybrid peer update via RPC",
		"namespace", namespace,
		"current_peers", len(currentPeers),
		"new_peers", len(newPeers),
		"to_add", len(toAdd))

	// Add new peers via RPC
	var rpcErrors []error
	for _, peer := range toAdd {
		if err := u.addPeerViaRPC(podIP, peer); err != nil {
			u.logger.Warn("Failed to add peer via RPC",
				"peer", peer,
				"error", err)
			rpcErrors = append(rpcErrors, err)
		} else {
			u.logger.Debug("Successfully added peer via RPC",
				"peer", peer)
		}
	}

	// If all RPC updates failed, return error to trigger restart
	if len(rpcErrors) > 0 && len(rpcErrors) == len(toAdd) {
		return fmt.Errorf("all RPC peer updates failed (%d errors)", len(rpcErrors))
	}

	// Partial success is acceptable
	if len(rpcErrors) > 0 {
		u.logger.Warn("Some peers failed to update via RPC",
			"failed", len(rpcErrors),
			"succeeded", len(toAdd)-len(rpcErrors))
	}

	// Verify connectivity after a short delay
	time.Sleep(3 * time.Second)
	if err := u.verifyPeerConnectivity(podIP, len(newPeers)); err != nil {
		u.logger.Warn("Peer connectivity verification failed", "error", err)
		// Don't fail - connectivity might improve
	}

	return nil
}

// getPodRPCEndpoint gets the RPC endpoint for a consumer chain pod
func (u *ConsumerChainUpdater) getPodRPCEndpoint(ctx context.Context, namespace string) (string, error) {
	pods, err := u.clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app=validator",
	})
	if err != nil {
		return "", fmt.Errorf("failed to list pods: %w", err)
	}

	if len(pods.Items) == 0 {
		return "", fmt.Errorf("no validator pods found")
	}

	pod := &pods.Items[0]
	if pod.Status.PodIP == "" {
		return "", fmt.Errorf("pod has no IP address")
	}

	// Tendermint RPC default port is 26657
	return fmt.Sprintf("http://%s:26657", pod.Status.PodIP), nil
}

// getCurrentPeersViaRPC gets current peers using Tendermint RPC
func (u *ConsumerChainUpdater) getCurrentPeersViaRPC(rpcEndpoint string) (map[string]string, error) {
	resp, err := u.httpClient.Get(fmt.Sprintf("%s/net_info", rpcEndpoint))
	if err != nil {
		return nil, fmt.Errorf("failed to call net_info: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("net_info returned %d: %s", resp.StatusCode, body)
	}

	var result struct {
		Result TendermintNetInfo `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode net_info: %w", err)
	}

	// Map peer ID to remote address
	peers := make(map[string]string)
	for _, peer := range result.Result.Peers {
		peers[peer.NodeInfo.ID] = peer.RemoteIP
	}

	return peers, nil
}

// calculatePeersToAdd determines which peers need to be added
func (u *ConsumerChainUpdater) calculatePeersToAdd(current map[string]string, newPeers []string) []string {
	var toAdd []string

	for _, newPeer := range newPeers {
		// Parse peer string: nodeID@host:port
		parts := strings.Split(newPeer, "@")
		if len(parts) != 2 {
			continue
		}
		nodeID := parts[0]

		// Skip empty node IDs
		if nodeID == "" {
			continue
		}

		// Check if peer already connected
		if _, exists := current[nodeID]; !exists {
			toAdd = append(toAdd, newPeer)
		}
	}

	// Return empty slice instead of nil for consistency
	if len(toAdd) == 0 {
		return []string{}
	}

	return toAdd
}

// addPeerViaRPC adds a peer using Tendermint RPC
func (u *ConsumerChainUpdater) addPeerViaRPC(rpcEndpoint, peer string) error {
	// Tendermint expects URL-encoded array
	url := fmt.Sprintf("%s/dial_peers?persistent=true&peers=[\"%s\"]", rpcEndpoint, peer)

	resp, err := u.httpClient.Post(url, "application/json", nil)
	if err != nil {
		return fmt.Errorf("failed to dial peer: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("dial_peers returned %d: %s", resp.StatusCode, body)
	}

	return nil
}

// verifyPeerConnectivity checks if we have expected number of peers
func (u *ConsumerChainUpdater) verifyPeerConnectivity(rpcEndpoint string, expectedPeers int) error {
	resp, err := u.httpClient.Get(fmt.Sprintf("%s/net_info", rpcEndpoint))
	if err != nil {
		return fmt.Errorf("failed to call net_info: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Result struct {
			NPeers string `json:"n_peers"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("failed to decode net_info: %w", err)
	}

	// NPeers is a string like "3"
	var nPeers int
	fmt.Sscanf(result.Result.NPeers, "%d", &nPeers)

	// Allow for some peers to not be connected yet
	minExpected := expectedPeers - 1
	if minExpected < 1 {
		minExpected = 1
	}

	if nPeers < minExpected {
		return fmt.Errorf("insufficient peers: have %d, expected at least %d", nPeers, minExpected)
	}

	u.logger.Debug("Peer connectivity verified",
		"connected_peers", nPeers,
		"expected_peers", expectedPeers)

	return nil
}
