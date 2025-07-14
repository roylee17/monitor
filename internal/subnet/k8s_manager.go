package subnet

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/sourcenetwork/ics-operator/internal/constants"
	"github.com/sourcenetwork/ics-operator/internal/deployment"
	"github.com/sourcenetwork/ics-operator/internal/retry"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
)

// K8sManager extends the traditional subnet manager with Kubernetes deployment capabilities
type K8sManager struct {
	*Manager // Embed the original manager for file-based operations
	deployer *deployment.K8sDeployer
	logger   *slog.Logger

	// Configuration
	consumerImage   string
	defaultReplicas int32
	validatorName   string

	// Peer discovery
	peerDiscovery *PeerDiscovery

	// LoadBalancer management
	lbManager *LoadBalancerManager

	// Goroutine management
	tasks      map[string]context.CancelFunc
	tasksMutex sync.Mutex
}

// NewK8sManager creates a new Kubernetes-aware subnet manager
func NewK8sManager(baseManager *Manager, logger *slog.Logger, consumerImage, validatorName string) (*K8sManager, error) {
	// Create deployer with empty namespace - we'll set it per deployment
	deployer, err := deployment.NewK8sDeployer(logger, "")
	if err != nil {
		return nil, fmt.Errorf("failed to create K8s deployer: %w", err)
	}

	// Create LoadBalancer manager
	lbManager := NewLoadBalancerManager(deployer.GetClientset(), logger)

	return &K8sManager{
		Manager:         baseManager,
		deployer:        deployer,
		logger:          logger,
		consumerImage:   consumerImage,
		defaultReplicas: 1,
		validatorName:   validatorName,
		lbManager:       lbManager,
		tasks:           make(map[string]context.CancelFunc),
	}, nil
}

// SetPeerDiscovery sets the peer discovery service
func (m *K8sManager) SetPeerDiscovery(pd *PeerDiscovery) {
	m.peerDiscovery = pd
}

// trackTask tracks a background task for proper cleanup
func (m *K8sManager) trackTask(id string, cancel context.CancelFunc) {
	m.tasksMutex.Lock()
	defer m.tasksMutex.Unlock()

	// Cancel existing task if any
	if existing, ok := m.tasks[id]; ok {
		m.logger.Info("Cancelling existing task", "task_id", id)
		existing()
	}

	m.tasks[id] = cancel
}

// Close cancels all background tasks and cleans up resources
func (m *K8sManager) Close() error {
	m.tasksMutex.Lock()
	defer m.tasksMutex.Unlock()

	m.logger.Info("Closing K8sManager, cancelling background tasks", "task_count", len(m.tasks))

	for id, cancel := range m.tasks {
		m.logger.Debug("Cancelling task", "task_id", id)
		cancel()
	}

	// Clear the map
	m.tasks = make(map[string]context.CancelFunc)

	// Stop all pod watchers
	m.lbManager.StopAllWatchers()

	return nil
}

// GetClientset returns the Kubernetes clientset from the deployer
func (m *K8sManager) GetClientset() (kubernetes.Interface, error) {
	if m.deployer == nil {
		return nil, fmt.Errorf("deployer not initialized")
	}
	return m.deployer.GetClientset(), nil
}

// GetNamespaceForChain returns the namespace for a specific consumer chain
// Since each validator runs in its own cluster, we just use the chainID as namespace
func (m *K8sManager) GetNamespaceForChain(chainID string) string {
	// No need for prefix since each validator has its own cluster
	return chainID
}

// DeployConsumer deploys a consumer chain using the simplified configuration struct
func (m *K8sManager) DeployConsumer(ctx context.Context, config *ConsumerDeploymentConfig) error {
	// Validate configuration
	if err := config.Validate(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	// Apply defaults
	if err := config.SetDefaults(); err != nil {
		return fmt.Errorf("failed to set defaults: %w", err)
	}

	// Handle dynamic peers if requested
	if config.UseDynamicPeers {
		return m.DeployConsumerWithDynamicPeers(ctx, config.ChainID, config.ConsumerID,
			*config.Ports, config.CCVPatch, config.ConsumerKey, nil)
	}

	// Use the existing implementation
	return m.deployConsumerFull(ctx, config.ChainID, config.ConsumerID,
		*config.Ports, config.Peers, config.CCVPatch, config.NodeKeyJSON, config.ConsumerKey)
}

// PrepareConsumerGenesis prepares the consumer genesis without deploying
func (m *K8sManager) PrepareConsumerGenesis(ctx context.Context, chainID, consumerID string) error {
	return m.preparePreCCVGenesis(chainID)
}

// preparePreCCVGenesis handles the pre-CCV genesis creation
func (m *K8sManager) preparePreCCVGenesis(chainID string) error {
	m.logger.Info("Preparing pre-CCV genesis", "chain_id", chainID)

	// Create pre-CCV genesis with funded relayer account
	// Using the well-known test mnemonic relayer address
	relayerAddr := "consumer1r5v5srda7xfth3hn2s26txvrcrntldjumt8mhl"
	relayerAccounts := []RelayerAccount{
		{
			Address: relayerAddr,
			Coins:   sdk.NewCoins(sdk.NewCoin(DefaultDenom, math.NewInt(constants.RelayerInitialFunds))),
		},
	}

	preCCVGenesis, err := CreatePreCCVGenesis(chainID, m.validatorName, relayerAccounts)
	if err != nil {
		return fmt.Errorf("failed to create pre-CCV genesis: %w", err)
	}

	// Save genesis to file
	subnetDir := filepath.Join(m.Manager.workDir, chainID)
	configDir := filepath.Join(subnetDir, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	genesisPath := filepath.Join(configDir, "genesis.json")
	// Use sorted JSON marshaling for deterministic output
	genesisData, err := MarshalSortedJSON(preCCVGenesis)
	if err != nil {
		return fmt.Errorf("failed to marshal genesis: %w", err)
	}

	if err := os.WriteFile(genesisPath, genesisData, 0644); err != nil {
		return fmt.Errorf("failed to write genesis file: %w", err)
	}

	m.logger.Info("Pre-CCV genesis with funded relayer account prepared",
		"chain_id", chainID,
		"relayer_address", relayerAddr,
		"genesis_path", genesisPath)

	return nil
}

// ApplyCCVGenesisAndRedeploy applies CCV genesis patch and redeploys the consumer chain
func (m *K8sManager) ApplyCCVGenesisAndRedeploy(ctx context.Context, chainID string, ccvPatch map[string]interface{}) error {
	m.logger.Info("Applying CCV genesis patch and redeploying", "chain_id", chainID)

	// Phase 1: Apply CCV genesis patch using the embedded manager
	if err := m.Manager.ApplyCCVGenesisPatch(chainID, ccvPatch); err != nil {
		return fmt.Errorf("failed to apply CCV genesis patch: %w", err)
	}

	// Phase 2: Update the ConfigMap with CCV genesis patch
	if err := m.updateConsumerGenesisConfigMap(ctx, chainID, ccvPatch); err != nil {
		return fmt.Errorf("failed to update genesis ConfigMap: %w", err)
	}

	// Phase 3: Restart the deployment to pick up the new genesis
	if err := m.restartConsumerDeployment(ctx, chainID); err != nil {
		return fmt.Errorf("failed to restart consumer deployment: %w", err)
	}

	m.logger.Info("CCV genesis applied and consumer chain redeployed", "chain_id", chainID)

	return nil
}

// restartConsumerDeployment restarts the consumer chain deployment to pick up genesis changes
func (m *K8sManager) restartConsumerDeployment(ctx context.Context, chainID string) error {
	m.logger.Info("Restarting consumer deployment to apply genesis changes", "chain_id", chainID)

	namespace := m.GetNamespaceForChain(chainID)
	// Add restart annotation to trigger rolling update
	if err := m.deployer.RestartDeployment(ctx, chainID, namespace); err != nil {
		return fmt.Errorf("failed to restart deployment: %w", err)
	}

	// Wait for deployment to be ready again
	if err := m.waitForDeploymentReady(ctx, chainID); err != nil {
		return fmt.Errorf("deployment failed to become ready after restart: %w", err)
	}

	m.logger.Info("Consumer deployment restarted successfully", "chain_id", chainID)
	return nil
}

// GetConsumerChainStatus returns the status of a consumer chain
func (m *K8sManager) GetConsumerChainStatus(ctx context.Context, chainID string) (*deployment.ConsumerChainStatus, error) {
	namespace := m.GetNamespaceForChain(chainID)
	return m.deployer.GetConsumerChainStatus(ctx, chainID, namespace)
}

// MonitorConsumerChainHealth monitors the health of deployed consumer chains
func (m *K8sManager) MonitorConsumerChainHealth(ctx context.Context, chainID string) error {
	m.logger.Info("Starting health monitoring for consumer chain", "chain_id", chainID)

	ticker := time.NewTicker(DefaultTickerInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			m.logger.Info("Health monitoring stopped", "chain_id", chainID)
			return ctx.Err()
		case <-ticker.C:
			status, err := m.GetConsumerChainStatus(ctx, chainID)
			if err != nil {
				m.logger.Error("Failed to get consumer chain status", "chain_id", chainID, "error", err)
				continue
			}

			m.logger.Info("Consumer chain health check",
				"chain_id", chainID,
				"ready_replicas", status.ReadyReplicas,
				"desired_replicas", status.DesiredReplicas,
				"available_replicas", status.AvailableReplicas)

			// Check if deployment is healthy
			if status.ReadyReplicas == 0 {
				m.logger.Warn("Consumer chain has no ready replicas", "chain_id", chainID)
			}

			if status.ReadyReplicas < status.DesiredReplicas {
				m.logger.Warn("Consumer chain is not at desired replica count",
					"chain_id", chainID,
					"ready", status.ReadyReplicas,
					"desired", status.DesiredReplicas)
			}
		}
	}
}

// deployConsumerFull deploys a consumer chain instance with all options including consumer key
func (m *K8sManager) deployConsumerFull(ctx context.Context, chainID, consumerID string, ports Ports, peers []string, ccvPatch map[string]interface{}, nodeKeyJSON string, consumerKey *ConsumerKeyInfo) error {
	// Initialize deployment error tracker
	deployErr := NewDeploymentError(chainID, "deployment")
	// Use the validator name from K8sManager, which was set during initialization
	validatorName := m.validatorName
	// Override with consumer key validator name if provided and different
	if consumerKey != nil && consumerKey.ValidatorName != "" && consumerKey.ValidatorName != validatorName {
		m.logger.Warn("Consumer key validator name differs from manager validator name",
			"manager_validator", validatorName,
			"consumer_key_validator", consumerKey.ValidatorName,
			"using", consumerKey.ValidatorName)
		validatorName = consumerKey.ValidatorName
	}

	m.logger.Info("Deploying consumer with specific ports",
		"chain_id", chainID,
		"consumer_id", consumerID,
		"validator", validatorName,
		"p2p_port", ports.P2P,
		"rpc_port", ports.RPC,
		"has_consumer_key", consumerKey != nil)

	// Calculate hashes for deployment metadata
	subnetdHash, genesisHash, err := m.Manager.CalculateHashes()
	if err != nil {
		// Don't fail deployment for hash calculation issues
		m.logger.Warn("Failed to calculate hashes for deployment", "error", err)
		subnetdHash, genesisHash = "unknown", "unknown"
	}

	// Calculate validator NodePort
	baseNodePort, err := CalculateValidatorNodePort(chainID, validatorName)
	if err != nil {
		deployErr.AddCriticalError(fmt.Errorf("failed to calculate NodePort: %w", err))
		return deployErr
	}

	namespace := m.GetNamespaceForChain(chainID)
	// Create deployment configuration with calculated ports
	deploymentConfig := deployment.ConsumerDeployment{
		ChainID:       chainID,
		ConsumerID:    consumerID,
		Image:         m.consumerImage,
		Namespace:     namespace,
		Replicas:      m.defaultReplicas,
		GenesisHash:   genesisHash,
		SubnetdHash:   subnetdHash,
		ValidatorName: validatorName, // Add validator name for unique deployment

		// Use calculated ports
		P2PPort:  int32(ports.P2P),
		RPCPort:  int32(ports.RPC),
		RESTPort: int32(ports.RPC + 660), // API port is RPC + 660
		GRPCPort: int32(ports.GRPC),

		// Set persistent peers
		PersistentPeers: strings.Join(peers, ","),

		// Consumer chain configuration
		ConsumerConfig: deployment.ConsumerChainConfig{
			UnbondingPeriod:       constants.ConsumerUnbondingPeriodStr,
			CCVTimeoutPeriod:      constants.ConsumerCCVTimeoutPeriodStr,
			TransferTimeout:       constants.ConsumerTransferTimeoutStr,
			BlocksPerTransmission: constants.ConsumerBlocksPerTransmission,
			RedistributionFrac:    constants.ConsumerRedistributionFrac,
		},

		// CCV Genesis patch
		CCVPatch: ccvPatch,

		// Deterministic node key
		NodeKeyJSON: nodeKeyJSON,

		// NodePort configuration for cross-cluster connectivity
		// Each validator needs unique NodePorts
		NodePorts: struct {
			P2P  int
			RPC  int
			GRPC int
		}{
			P2P:  int(baseNodePort),
			RPC:  int(baseNodePort) + 1,
			GRPC: int(baseNodePort) + 2,
		},
	}

	// Add consumer validator key if provided
	if consumerKey != nil && consumerKey.PrivValidatorKeyJSON != "" {
		deploymentConfig.ConsumerKeyJSON = consumerKey.PrivValidatorKeyJSON
		m.logger.Info("Including consumer validator key in deployment",
			"validator", validatorName,
			"consumer_id", consumerID)
	}

	// Debug logging
	if ccvPatch != nil {
		m.logger.Info("Deploying with CCV patch",
			"chain_id", chainID,
			"has_ccv_patch", true)
	} else {
		m.logger.Warn("Deploying WITHOUT CCV patch",
			"chain_id", chainID)
	}

	// Deploy to Kubernetes
	if err := m.deployer.DeployConsumerChain(ctx, deploymentConfig); err != nil {
		return fmt.Errorf("failed to deploy consumer chain: %w", err)
	}

	// Wait for pod to be ready and configure LoadBalancer
	m.logger.Info("Waiting for consumer pod to be ready for LoadBalancer configuration",
		"chain_id", chainID)

	// Use retry package for pod readiness check
	retryConfig := retry.Config{
		MaxAttempts:  12,
		InitialDelay: 5 * time.Second,
		MaxDelay:     30 * time.Second,
		Multiplier:   1.0, // Keep constant delay for pod readiness
	}

	podIP, err := retry.DoWithResult(ctx, retryConfig, func() (string, error) {
		// Get consumer chain status
		status, err := m.deployer.GetConsumerChainStatus(ctx, chainID, namespace)
		if err != nil {
			m.logger.Warn("Failed to get consumer chain status, will retry",
				"chain_id", chainID,
				"error", err)
			return "", err
		}

		// Look for a ready pod
		for _, pod := range status.Pods {
			if pod.Ready && pod.PodIP != "" {
				return pod.PodIP, nil
			}
		}

		m.logger.Debug("Consumer pod not ready yet",
			"chain_id", chainID)
		return "", fmt.Errorf("no ready pods found")
	})

	if err != nil {
		m.logger.Warn("Failed to get pod IP after retries",
			"chain_id", chainID,
			"error", err)
		deployErr.AddError(fmt.Errorf("pod readiness check failed: %w", err))
	}

	if podIP == "" {
		// Even after retries, no pod is ready
		m.logger.Warn("Consumer pod still not ready after retries, LoadBalancer configuration deferred",
			"chain_id", chainID)
		// Start a goroutine to configure LoadBalancer later
		taskCtx, cancel := context.WithCancel(context.Background())
		m.trackTask(fmt.Sprintf("loadbalancer-%s", chainID), cancel)
		go m.configureLoadBalancerWhenReady(taskCtx, chainID, namespace, ports.P2P)
	} else {
		// Configure LoadBalancer for this consumer chain
		calculatedPort := ports.P2P

		// Add port to LoadBalancer (critical for peer connectivity)
		if err := m.lbManager.AddConsumerPort(ctx, chainID, int32(calculatedPort)); err != nil {
			m.logger.Error("Failed to add port to LoadBalancer",
				"chain_id", chainID,
				"port", calculatedPort,
				"error", err)
			deployErr.AddCriticalError(fmt.Errorf("LoadBalancer port configuration failed: %w", err))
		}

		// Create EndpointSlice for LoadBalancer routing (critical for peer connectivity)
		if err := m.lbManager.CreateEndpointSlice(ctx, chainID, namespace, podIP, int32(calculatedPort)); err != nil {
			m.logger.Error("Failed to create EndpointSlice for LoadBalancer",
				"chain_id", chainID,
				"error", err)
			deployErr.AddCriticalError(fmt.Errorf("EndpointSlice creation failed: %w", err))
		}

		m.logger.Info("LoadBalancer configured for consumer chain",
			"chain_id", chainID,
			"port", calculatedPort,
			"pod_ip", podIP)
	}

	// Start watching pods for automatic EndpointSlice updates
	if err := m.lbManager.StartPodWatcher(ctx, chainID, namespace); err != nil {
		m.logger.Error("Failed to start pod watcher",
			"chain_id", chainID,
			"error", err)
		// Non-critical error - continue
	} else {
		m.logger.Info("Started automatic pod IP tracking for EndpointSlice updates",
			"chain_id", chainID,
			"namespace", namespace)
	}

	// Check deployment status and return appropriate error
	if deployErr.HasErrors() {
		m.logger.Warn("Consumer chain deployed with errors",
			"chain_id", chainID,
			"validator", validatorName,
			"summary", deployErr.Summary())
		// Only return error if critical
		if deployErr.IsCritical() {
			return deployErr
		}
	} else {
		m.logger.Info("Consumer chain deployed successfully",
			"chain_id", chainID,
			"validator", validatorName,
			"peers_count", len(peers))
	}

	// Deploy Hermes relayer for this consumer chain
	m.logger.Info("Deploying Hermes relayer for consumer chain",
		"chain_id", chainID,
		"namespace", namespace)

	if err := m.deployHermes(ctx, chainID, namespace, ports); err != nil {
		m.logger.Error("Failed to deploy Hermes relayer",
			"chain_id", chainID,
			"error", err)
		// Non-critical error - consumer chain can run without Hermes
		deployErr.AddError(fmt.Errorf("Hermes deployment failed: %w", err))
	}

	return nil
}

// StopConsumerChain gracefully stops a consumer chain
func (m *K8sManager) StopConsumerChain(ctx context.Context, chainID string) error {
	m.logger.Info("Stopping consumer chain", "chain_id", chainID)

	// Scale down deployment to 0 replicas
	if err := m.deployer.ScaleDeployment(ctx, chainID, 0); err != nil {
		return fmt.Errorf("failed to scale down deployment: %w", err)
	}

	m.logger.Info("Consumer chain stopped successfully", "chain_id", chainID)
	return nil
}

// RemoveConsumerChain removes all resources for a consumer chain
func (m *K8sManager) RemoveConsumerChain(ctx context.Context, chainID string) error {
	m.logger.Info("Removing consumer chain", "chain_id", chainID)

	// Cancel any background tasks for this chain
	m.tasksMutex.Lock()
	taskID := fmt.Sprintf("loadbalancer-%s", chainID)
	if cancel, ok := m.tasks[taskID]; ok {
		m.logger.Info("Cancelling background task", "task_id", taskID)
		cancel()
		delete(m.tasks, taskID)
	}
	m.tasksMutex.Unlock()

	// Stop pod watcher for this chain
	m.lbManager.StopPodWatcher(chainID)

	// Calculate port to remove from LoadBalancer
	ports, err := CalculatePorts(chainID)
	if err == nil {
		// Remove port from LoadBalancer
		if err := m.lbManager.RemoveConsumerPort(ctx, chainID, int32(ports.P2P)); err != nil {
			m.logger.Error("Failed to remove port from LoadBalancer",
				"chain_id", chainID,
				"port", ports.P2P,
				"error", err)
		}

		// Delete EndpointSlice
		if err := m.lbManager.DeleteEndpointSlice(ctx, chainID); err != nil {
			m.logger.Error("Failed to delete EndpointSlice",
				"chain_id", chainID,
				"error", err)
		}
	}

	// Delete Hermes deployment and resources
	namespace := m.GetNamespaceForChain(chainID)
	if err := m.deleteHermesResources(ctx, chainID, namespace); err != nil {
		m.logger.Error("Failed to delete Hermes resources",
			"chain_id", chainID,
			"error", err)
		// Non-critical error - continue with cleanup
	}

	// Delete all Kubernetes resources
	if err := m.deployer.DeleteConsumerChain(ctx, chainID); err != nil {
		return fmt.Errorf("failed to delete consumer chain resources: %w", err)
	}

	// Clean up local work directory
	if err := m.Manager.CleanupSubnet(chainID); err != nil {
		m.logger.Warn("Failed to cleanup local files", "error", err)
		// Don't fail the operation for local cleanup issues
	}

	m.logger.Info("Consumer chain removed successfully", "chain_id", chainID)
	return nil
}

// ConsumerDeploymentExists checks if a consumer chain deployment exists
func (m *K8sManager) ConsumerDeploymentExists(ctx context.Context, chainID string) (bool, error) {
	// Use the deployer to check if deployment exists in the chain-specific namespace
	namespace := m.GetNamespaceForChain(chainID)
	return m.deployer.DeploymentExistsInNamespace(ctx, chainID, namespace)
}

// updateConsumerGenesisConfigMap updates the ConfigMap with CCV genesis patch
func (m *K8sManager) updateConsumerGenesisConfigMap(ctx context.Context, chainID string, ccvPatch map[string]interface{}) error {
	m.logger.Info("Updating consumer genesis ConfigMap with CCV patch", "chain_id", chainID)

	// Convert CCV patch to JSON with sorted fields for deterministic output
	ccvPatchJSON, err := MarshalSortedJSON(ccvPatch)
	if err != nil {
		return fmt.Errorf("failed to marshal CCV patch: %w", err)
	}

	namespace := m.GetNamespaceForChain(chainID)
	// Update the ConfigMap through the deployer
	configMapName := fmt.Sprintf(ConfigMapNameFormat, chainID)
	if err := m.deployer.UpdateConfigMap(ctx, namespace, configMapName, map[string]string{
		"ccv-patch.json": string(ccvPatchJSON),
	}); err != nil {
		return fmt.Errorf("failed to update ConfigMap: %w", err)
	}

	m.logger.Info("Consumer genesis ConfigMap updated with CCV patch", "chain_id", chainID)
	return nil
}

// RestartDeployment restarts a consumer chain deployment
func (m *K8sManager) RestartDeployment(ctx context.Context, chainID string) error {
	m.logger.Info("Restarting consumer chain deployment", "chain_id", chainID)

	namespace := m.GetNamespaceForChain(chainID)
	deploymentName := chainID // No suffix, matching createConsumerDeployment
	return m.deployer.RestartDeployment(ctx, deploymentName, namespace)
}

// waitForDeploymentReady waits for the deployment to become ready after restart
func (m *K8sManager) waitForDeploymentReady(ctx context.Context, chainID string) error {
	m.logger.Info("Waiting for deployment to become ready", "chain_id", chainID)

	ticker := time.NewTicker(DefaultPollInterval)
	defer ticker.Stop()

	timeout := time.After(DefaultDeployTimeout)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout:
			return fmt.Errorf("timeout waiting for deployment to become ready")
		case <-ticker.C:
			status, err := m.GetConsumerChainStatus(ctx, chainID)
			if err != nil {
				m.logger.Warn("Failed to get deployment status", "error", err)
				continue
			}

			if status.ReadyReplicas == status.DesiredReplicas && status.ReadyReplicas > 0 {
				m.logger.Info("Deployment is ready",
					"chain_id", chainID,
					"ready_replicas", status.ReadyReplicas)
				return nil
			}

			m.logger.Debug("Deployment not ready yet",
				"chain_id", chainID,
				"ready", status.ReadyReplicas,
				"desired", status.DesiredReplicas)
		}
	}
}

// DeployConsumerWithDynamicPeers deploys a consumer chain with dynamically discovered peers
func (m *K8sManager) DeployConsumerWithDynamicPeers(ctx context.Context, chainID, consumerID string, ports Ports, ccvPatch map[string]interface{}, consumerKey *ConsumerKeyInfo, actualOptedInValidators []string) error {
	m.logger.Info("Deploying consumer with dynamic peer discovery",
		"chain_id", chainID,
		"consumer_id", consumerID)

	// Discover peers using peer discovery if configured
	var peers []string
	var nodeKeyJSON string

	if m.peerDiscovery != nil {
		// Get the list of opted-in validators
		var optedInValidators []string

		// First priority: Use explicitly provided validators
		if len(actualOptedInValidators) > 0 {
			optedInValidators = actualOptedInValidators
			m.logger.Info("Using explicitly provided opted-in validators",
				"consumer_id", consumerID,
				"validators", optedInValidators)
		} else {
			// DO NOT extract validators from CCV patch
			// The CCV patch contains validators selected by the subset algorithm,
			// NOT the validators that actually opted in.
			// The initial_val_set is determined at consumer creation time,
			// but opt-ins happen after that.
			m.logger.Info("Not using CCV patch for validator discovery",
				"reason", "CCV patch contains selected validators, not opted-in validators")
		}

		// If we don't have validators from the CCV patch, that's fine
		// The actual peers will be discovered from running deployments
		if len(optedInValidators) == 0 {
			m.logger.Info("No validators found in CCV patch, will discover peers from actual deployments",
				"consumer_id", consumerID,
				"chain_id", chainID)
		}

		// Discover peers with node IDs
		discoveredPeers, err := m.peerDiscovery.DiscoverPeersWithChainID(ctx, consumerID, chainID, optedInValidators)
		if err != nil {
			m.logger.Warn("Failed to discover peers, continuing without peers",
				"error", err)
			peers = []string{}
		} else {
			peers = discoveredPeers
			m.logger.Info("Discovered peers",
				"chain_id", chainID,
				"peer_count", len(peers),
				"peers", peers)
		}

		// Generate node key for this validator
		nodeKeyBytes, err := m.peerDiscovery.GetNodeKeyJSON(chainID)
		if err != nil {
			m.logger.Warn("Failed to generate node key",
				"error", err)
		} else {
			nodeKeyJSON = string(nodeKeyBytes)
			m.logger.Info("Generated node key for consumer chain",
				"chain_id", chainID,
				"validator", m.validatorName)
		}
	}

	// Deploy with discovered peers, node key, and consumer key
	return m.deployConsumerFull(ctx, chainID, consumerID, ports, peers, ccvPatch, nodeKeyJSON, consumerKey)
}

// EnsureLoadBalancerReady checks if the shared LoadBalancer is ready and returns its endpoint
func (k *K8sManager) EnsureLoadBalancerReady(ctx context.Context) (string, error) {
	return k.lbManager.EnsureLoadBalancerReady(ctx)
}

// configureLoadBalancerWhenReady is a background task that waits for consumer pod to be ready
// and then configures the LoadBalancer. This is critical for peer connectivity.
func (m *K8sManager) configureLoadBalancerWhenReady(ctx context.Context, chainID, namespace string, p2pPort int) {
	m.logger.Info("Starting background LoadBalancer configuration task",
		"chain_id", chainID)

	// Retry configuration for background task
	retryConfig := retry.Config{
		MaxAttempts:  60, // 5 minutes total with 5s intervals
		InitialDelay: 5 * time.Second,
		MaxDelay:     30 * time.Second,
		Multiplier:   1.0, // Keep constant delay for background checks
	}

	err := retry.Do(ctx, retryConfig, func() error {
		// Get consumer chain status
		status, err := m.deployer.GetConsumerChainStatus(ctx, chainID, namespace)
		if err != nil {
			m.logger.Warn("Failed to get consumer chain status in background task",
				"chain_id", chainID,
				"error", err)
			return err
		}

		// Look for a ready pod
		var podIP string
		for _, pod := range status.Pods {
			if pod.Ready && pod.PodIP != "" {
				podIP = pod.PodIP
				break
			}
		}

		if podIP == "" {
			return fmt.Errorf("no ready pods found")
		}

		// Configure LoadBalancer
		if err := m.lbManager.AddConsumerPort(ctx, chainID, int32(p2pPort)); err != nil {
			m.logger.Error("CRITICAL: Failed to add port to LoadBalancer",
				"chain_id", chainID,
				"port", p2pPort,
				"error", err,
				"impact", "Consumer chain will not be able to receive peer connections")
			// Return error to stop retrying and log critical failure
			return fmt.Errorf("critical LoadBalancer error: %w", err)
		}

		// Create EndpointSlice for LoadBalancer routing (using modern API)
		if err := m.lbManager.CreateEndpointSlice(ctx, chainID, namespace, podIP, int32(p2pPort)); err != nil {
			m.logger.Error("CRITICAL: Failed to create EndpointSlice",
				"chain_id", chainID,
				"error", err,
				"impact", "LoadBalancer will not route traffic to consumer chain")
			// Return error to stop retrying and log critical failure
			return fmt.Errorf("critical EndpointSlice error: %w", err)
		}

		m.logger.Info("LoadBalancer configured successfully in background task",
			"chain_id", chainID,
			"port", p2pPort,
			"pod_ip", podIP)

		// Start pod watcher for automatic updates
		if err := m.lbManager.StartPodWatcher(ctx, chainID, namespace); err != nil {
			m.logger.Error("Failed to start pod watcher in background task",
				"chain_id", chainID,
				"error", err)
			// Non-critical - LoadBalancer is configured
		} else {
			m.logger.Info("Started automatic pod IP tracking in background task",
				"chain_id", chainID,
				"namespace", namespace)
		}

		return nil
	})

	if err != nil {
		if ctx.Err() != nil {
			m.logger.Info("LoadBalancer configuration cancelled",
				"chain_id", chainID)
		} else {
			m.logger.Error("CRITICAL: Failed to configure LoadBalancer after all retries",
				"chain_id", chainID,
				"error", err,
				"impact", "Consumer chain deployed but CANNOT receive peer connections",
				"action", "Manual intervention required to fix LoadBalancer configuration")
		}
	}
}

// deployHermes deploys a Hermes relayer instance for the consumer chain
func (m *K8sManager) deployHermes(ctx context.Context, chainID, namespace string, ports Ports) error {
	m.logger.Info("Deploying Hermes relayer",
		"chain_id", chainID,
		"namespace", namespace)

	// Generate Hermes configuration
	hermesConfig, err := m.generateHermesConfig(chainID, ports)
	if err != nil {
		return fmt.Errorf("failed to generate Hermes config: %w", err)
	}

	// Create Hermes ConfigMap
	if err := m.createHermesConfigMap(ctx, chainID, namespace, hermesConfig); err != nil {
		return fmt.Errorf("failed to create Hermes ConfigMap: %w", err)
	}

	// Create Hermes Deployment
	if err := m.createHermesDeployment(ctx, chainID, namespace); err != nil {
		return fmt.Errorf("failed to create Hermes deployment: %w", err)
	}

	m.logger.Info("Hermes relayer deployed successfully",
		"chain_id", chainID,
		"namespace", namespace)

	// Start background task to create CCV channel once Hermes is ready
	taskCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	m.trackTask(fmt.Sprintf("ccv-channel-%s", chainID), cancel)

	go func() {
		defer cancel()
		if err := m.createCCVChannelWhenReady(taskCtx, chainID, namespace); err != nil {
			m.logger.Error("Failed to create CCV channel",
				"chain_id", chainID,
				"error", err)
		}
	}()

	return nil
}

// generateHermesConfig generates the Hermes configuration for the consumer chain
func (m *K8sManager) generateHermesConfig(chainID string, ports Ports) (string, error) {
	// Consumer chain configuration using calculated ports
	consumerRPCPort := ports.RPC
	consumerGRPCPort := ports.GRPC

	// Generate Hermes configuration
	config := fmt.Sprintf(`[global]
log_level = "info"

[mode]

[mode.clients]
enabled = true
refresh = true
misbehaviour = true

[mode.connections]
enabled = false

[mode.channels]
enabled = false

[mode.packets]
enabled = true

[[chains]]
account_prefix = "cosmos"
clock_drift = "5s"
gas_multiplier = 1.1
grpc_addr = "tcp://%s-validator.provider.svc.cluster.local:9090"
id = "provider-1"
key_name = "%s"
max_gas = 20000000
rpc_addr = "http://%s-validator.provider.svc.cluster.local:26657"
rpc_timeout = "10s"
store_prefix = "ibc"
trusting_period = "20hours"
event_source = { mode = 'push', url = 'ws://%s-validator.provider.svc.cluster.local:26657/websocket', batch_delay = '50ms' }
ccv_consumer_chain = false

[chains.gas_price]
denom = "stake"
price = 0.000

[chains.trust_threshold]
denominator = "3"
numerator = "1"

[[chains]]
account_prefix = "consumer"
clock_drift = "5s"
gas_multiplier = 1.1
grpc_addr = "tcp://%s.%s:%d"
id = "%s"
key_name = "%s"
max_gas = 20000000
rpc_addr = "http://%s.%s:%d"
rpc_timeout = "10s"
store_prefix = "ibc"
trusting_period = "20hours"
event_source = { mode = 'push', url = 'ws://%s.%s:%d/websocket', batch_delay = '50ms' }
ccv_consumer_chain = true

[chains.gas_price]
denom = "stake"
price = 0.000

[chains.trust_threshold]
denominator = "3"
numerator = "1"
`, m.validatorName, m.validatorName, m.validatorName, m.validatorName,
		chainID, m.GetNamespaceForChain(chainID), consumerGRPCPort,
		chainID, m.validatorName,
		chainID, m.GetNamespaceForChain(chainID), consumerRPCPort,
		chainID, m.GetNamespaceForChain(chainID), consumerRPCPort)

	return config, nil
}

// createHermesConfigMap creates a ConfigMap for Hermes configuration
func (m *K8sManager) createHermesConfigMap(ctx context.Context, chainID, namespace, config string) error {
	configMapName := fmt.Sprintf("%s-hermes-config", chainID)

	// Create ConfigMap with Hermes configuration
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: namespace,
			Labels: map[string]string{
				"app":                        "hermes",
				"chain-id":                   chainID,
				"validator":                  m.validatorName,
				"app.kubernetes.io/name":     "hermes",
				"app.kubernetes.io/instance": fmt.Sprintf("%s-hermes", chainID),
			},
		},
		Data: map[string]string{
			"config.toml": config,
		},
	}

	_, err := m.deployer.GetClientset().CoreV1().ConfigMaps(namespace).Create(ctx, configMap, metav1.CreateOptions{})
	if err != nil {
		if errors.IsAlreadyExists(err) {
			// Update existing ConfigMap
			_, err = m.deployer.GetClientset().CoreV1().ConfigMaps(namespace).Update(ctx, configMap, metav1.UpdateOptions{})
			if err != nil {
				return fmt.Errorf("failed to update Hermes ConfigMap: %w", err)
			}
			m.logger.Info("Updated existing Hermes ConfigMap", "name", configMapName, "namespace", namespace)
		} else {
			return fmt.Errorf("failed to create Hermes ConfigMap: %w", err)
		}
	} else {
		m.logger.Info("Created Hermes ConfigMap", "name", configMapName, "namespace", namespace)
	}

	return nil
}

// createHermesDeployment creates the Hermes deployment
func (m *K8sManager) createHermesDeployment(ctx context.Context, chainID, namespace string) error {
	deploymentName := fmt.Sprintf("%s-hermes", chainID)
	configMapName := fmt.Sprintf("%s-hermes-config", chainID)

	// Use the standard test mnemonic for relayer
	relayerMnemonic := "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon art"

	replicas := int32(1)
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName,
			Namespace: namespace,
			Labels: map[string]string{
				"app":                        "hermes",
				"chain-id":                   chainID,
				"validator":                  m.validatorName,
				"app.kubernetes.io/name":     "hermes",
				"app.kubernetes.io/instance": fmt.Sprintf("%s-hermes", chainID),
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app":       "hermes",
					"chain-id":  chainID,
					"validator": m.validatorName,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app":       "hermes",
						"chain-id":  chainID,
						"validator": m.validatorName,
					},
				},
				Spec: corev1.PodSpec{
					InitContainers: []corev1.Container{
						{
							Name:    "init-keys",
							Image:   "informalsystems/hermes:1.13.1",
							Command: []string{"/bin/bash", "-c"},
							Args: []string{fmt.Sprintf(`
set -e
# Create .hermes directory if it doesn't exist
mkdir -p /home/hermes/.hermes

# Copy config from ConfigMap
cp /config/config.toml /home/hermes/.hermes/config.toml

# Import keys for both chains
echo "%s" > /tmp/mnemonic.txt

# Import provider key
hermes --config /home/hermes/.hermes/config.toml keys add --chain provider-1 --key-name %s --mnemonic-file /tmp/mnemonic.txt --overwrite

# Import consumer key
hermes --config /home/hermes/.hermes/config.toml keys add --chain %s --key-name %s --mnemonic-file /tmp/mnemonic.txt --overwrite

rm /tmp/mnemonic.txt
`, relayerMnemonic, m.validatorName, chainID, m.validatorName)},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "hermes-home",
									MountPath: "/home/hermes/.hermes",
								},
								{
									Name:      "config",
									MountPath: "/config",
								},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Name:    "hermes",
							Image:   "informalsystems/hermes:1.13.1",
							Command: []string{"hermes", "start"},
							Env: []corev1.EnvVar{
								{
									Name:  "RUST_LOG",
									Value: "info",
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "hermes-home",
									MountPath: "/home/hermes/.hermes",
								},
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("100m"),
									corev1.ResourceMemory: resource.MustParse("256Mi"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("500m"),
									corev1.ResourceMemory: resource.MustParse("512Mi"),
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "hermes-home",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
						{
							Name: "config",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: configMapName,
									},
								},
							},
						},
					},
				},
			},
		},
	}

	_, err := m.deployer.GetClientset().AppsV1().Deployments(namespace).Create(ctx, deployment, metav1.CreateOptions{})
	if err != nil {
		if errors.IsAlreadyExists(err) {
			m.logger.Info("Hermes deployment already exists", "name", deploymentName, "namespace", namespace)
		} else {
			return fmt.Errorf("failed to create Hermes deployment: %w", err)
		}
	} else {
		m.logger.Info("Created Hermes deployment", "name", deploymentName, "namespace", namespace)
	}

	return nil
}

// createCCVChannelWhenReady waits for Hermes to be ready and creates the CCV channel
func (m *K8sManager) createCCVChannelWhenReady(ctx context.Context, chainID, namespace string) error {
	m.logger.Info("Starting CCV channel creation process",
		"chain_id", chainID,
		"namespace", namespace)

	// Wait for Hermes pod to be ready
	hermesDeploymentName := fmt.Sprintf("%s-hermes", chainID)

	// Retry configuration for waiting for Hermes to be ready
	retryConfig := retry.Config{
		MaxAttempts:  60, // 10 minutes with 10s interval
		InitialDelay: 10 * time.Second,
		MaxDelay:     10 * time.Second,
		Multiplier:   1.0,
	}

	err := retry.Do(ctx, retryConfig, func() error {
		// Check if Hermes deployment is ready
		deployment, err := m.deployer.GetClientset().AppsV1().Deployments(namespace).Get(ctx, hermesDeploymentName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get Hermes deployment: %w", err)
		}

		if deployment.Status.ReadyReplicas < 1 {
			return fmt.Errorf("Hermes deployment not ready yet")
		}

		// Get Hermes pod
		pods, err := m.deployer.GetClientset().CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: fmt.Sprintf("app=hermes,chain-id=%s", chainID),
		})
		if err != nil {
			return fmt.Errorf("failed to list Hermes pods: %w", err)
		}

		if len(pods.Items) == 0 {
			return fmt.Errorf("no Hermes pods found")
		}

		// Check if pod is ready
		pod := &pods.Items[0]
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodReady && condition.Status != corev1.ConditionTrue {
				return fmt.Errorf("Hermes pod not ready")
			}
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("Hermes deployment did not become ready: %w", err)
	}

	m.logger.Info("Hermes is ready, waiting before creating CCV channel",
		"chain_id", chainID)

	// Wait a bit more to ensure Hermes has fully initialized
	select {
	case <-time.After(30 * time.Second):
	case <-ctx.Done():
		return ctx.Err()
	}

	// Create CCV channel
	return m.createCCVChannel(ctx, chainID, namespace)
}

// createCCVChannel creates the CCV channel between provider and consumer chains
func (m *K8sManager) createCCVChannel(ctx context.Context, chainID, namespace string) error {
	m.logger.Info("Creating CCV channel with automatic client discovery",
		"chain_id", chainID,
		"namespace", namespace)

	// Get Hermes pod name
	pods, err := m.deployer.GetClientset().CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("app=hermes,chain-id=%s", chainID),
	})
	if err != nil {
		return fmt.Errorf("failed to list Hermes pods: %w", err)
	}

	if len(pods.Items) == 0 {
		return fmt.Errorf("no Hermes pods found")
	}

	podName := pods.Items[0].Name

	// Get provider chain ID from Hermes config
	providerChainID, err := m.getProviderChainIDFromHermes(ctx, namespace, podName)
	if err != nil {
		// Fall back to common default if we can't query it
		m.logger.Warn("Failed to query provider chain ID from Hermes, using default",
			"error", err)
		providerChainID = "provider-1"
	}

	// Query to find the correct provider client ID for this consumer chain
	providerClientID, err := m.queryProviderClientForConsumer(ctx, namespace, podName, chainID, providerChainID)
	if err != nil {
		// If we can't find the client, it might not exist yet
		// This could happen if the consumer chain just launched
		m.logger.Warn("Failed to find provider client ID, will try default",
			"error", err)
		// Try to create the client first or use a fallback
		// For now, we'll return the error
		return fmt.Errorf("failed to find provider client for consumer chain: %w", err)
	}

	// Query for consumer client ID on the consumer chain
	consumerClientID, err := m.queryConsumerClientID(ctx, namespace, podName, chainID)
	if err != nil {
		// Default to standard client-0 if we can't query
		m.logger.Warn("Failed to query consumer client ID, using default",
			"error", err)
		consumerClientID = "07-tendermint-0"
	}

	// Create connection with the correct client IDs
	m.logger.Info("Creating IBC connection",
		"pod", podName,
		"consumer_client", consumerClientID,
		"provider_client", providerClientID)

	connectionCmd := []string{
		"hermes",
		"create", "connection",
		"--a-chain", chainID,
		"--a-client", consumerClientID,
		"--b-client", providerClientID,
	}

	if err := m.execInPod(ctx, namespace, podName, "hermes", connectionCmd); err != nil {
		// Check if connection already exists (non-fatal)
		if !strings.Contains(err.Error(), "connection already exists") &&
			!strings.Contains(err.Error(), "connection handshake already finished") {
			m.logger.Warn("Failed to create connection, will try channel anyway",
				"error", err)
		}
	}

	// Query for the actual connection ID
	connectionID, err := m.queryConnectionID(ctx, namespace, podName, chainID, consumerClientID)
	if err != nil {
		// Default to connection-0 if we can't query
		m.logger.Warn("Failed to query connection ID, using default",
			"error", err)
		connectionID = "connection-0"
	}

	// Create CCV channel
	m.logger.Info("Creating CCV channel",
		"pod", podName,
		"connection", connectionID)

	channelCmd := []string{
		"hermes",
		"create", "channel",
		"--a-chain", chainID,
		"--a-port", "consumer",
		"--b-port", "provider",
		"--order", "ordered",
		"--channel-version", "1",
		"--a-connection", connectionID,
	}

	if err := m.execInPod(ctx, namespace, podName, "hermes", channelCmd); err != nil {
		// Check if channel already exists
		if !strings.Contains(err.Error(), "channel handshake already finished") {
			return fmt.Errorf("failed to create CCV channel: %w", err)
		}
		m.logger.Info("CCV channel already exists",
			"chain_id", chainID)
	} else {
		m.logger.Info("CCV channel created successfully",
			"chain_id", chainID)
	}

	return nil
}

// queryProviderClientForConsumer queries the provider chain to find the IBC client ID for a consumer chain
func (m *K8sManager) queryProviderClientForConsumer(ctx context.Context, namespace, podName, consumerChainID, providerChainID string) (string, error) {
	m.logger.Info("Querying provider chain for consumer client",
		"consumer_chain_id", consumerChainID,
		"provider_chain_id", providerChainID)

	// Query all clients on the provider chain
	queryCmd := []string{
		"hermes",
		"query", "clients",
		"--host-chain", providerChainID,
	}

	var stdout, stderr strings.Builder
	if err := m.execInPodWithOutput(ctx, namespace, podName, "hermes", queryCmd, &stdout, &stderr); err != nil {
		return "", fmt.Errorf("failed to query clients: %w", err)
	}

	// Parse the output to find clients
	output := stdout.String()

	// The output format from Hermes is not standard JSON, it's a custom format
	// Looking for pattern like:
	// ClientChain {
	//     client_id: ClientId(
	//         "07-tendermint-1",
	//     ),
	//     chain_id: ChainId {
	//         id: "consumer-1-1752458708-0",
	//         version: 0,
	//     },
	// }

	// Find all client entries
	lines := strings.Split(output, "\n")
	var currentClientID string

	for i, line := range lines {
		// Look for client_id line
		if strings.Contains(line, "client_id: ClientId(") {
			// Extract client ID from next line or same line
			startIdx := strings.Index(line, "ClientId(")
			if startIdx != -1 {
				// Look for the quoted client ID
				for j := i; j < len(lines) && j < i+3; j++ {
					if idx := strings.Index(lines[j], `"`); idx != -1 {
						endIdx := strings.Index(lines[j][idx+1:], `"`)
						if endIdx != -1 {
							currentClientID = lines[j][idx+1 : idx+1+endIdx]
							break
						}
					}
				}
			}
		}

		// Look for chain_id that matches our consumer chain
		if strings.Contains(line, "chain_id: ChainId") && currentClientID != "" {
			// Check the following lines for the actual chain ID
			for j := i; j < len(lines) && j < i+5; j++ {
				if strings.Contains(lines[j], `id: "`) {
					if strings.Contains(lines[j], consumerChainID) {
						m.logger.Info("Found provider client for consumer chain",
							"consumer_chain_id", consumerChainID,
							"provider_client_id", currentClientID)
						return currentClientID, nil
					}
					break
				}
			}
		}
	}

	return "", fmt.Errorf("no provider client found for consumer chain %s", consumerChainID)
}

// getProviderChainIDFromHermes queries Hermes configuration to get the provider chain ID
func (m *K8sManager) getProviderChainIDFromHermes(ctx context.Context, namespace, podName string) (string, error) {
	// Query Hermes for configured chains
	queryCmd := []string{
		"hermes",
		"query", "chains",
	}

	var stdout, stderr strings.Builder
	if err := m.execInPodWithOutput(ctx, namespace, podName, "hermes", queryCmd, &stdout, &stderr); err != nil {
		return "", fmt.Errorf("failed to query chains: %w", err)
	}

	// Parse output to find provider chain
	lines := strings.Split(stdout.String(), "\n")
	for _, line := range lines {
		// Look for provider chain pattern
		if strings.Contains(line, "provider") {
			// Extract chain ID from output
			// Format is typically: ChainId { id: "provider-1", version: 1 }
			if idx := strings.Index(line, `id: "`); idx != -1 {
				startIdx := idx + 5
				endIdx := strings.Index(line[startIdx:], `"`)
				if endIdx != -1 {
					chainID := line[startIdx : startIdx+endIdx]
					if strings.Contains(chainID, "provider") {
						m.logger.Info("Found provider chain ID from Hermes",
							"chain_id", chainID)
						return chainID, nil
					}
				}
			}
		}
	}

	return "", fmt.Errorf("provider chain not found in Hermes configuration")
}

// queryConsumerClientID queries the consumer chain to find the IBC client ID for the provider
func (m *K8sManager) queryConsumerClientID(ctx context.Context, namespace, podName, consumerChainID string) (string, error) {
	// Query all clients on the consumer chain
	queryCmd := []string{
		"hermes",
		"query", "clients",
		"--host-chain", consumerChainID,
	}

	var stdout, stderr strings.Builder
	if err := m.execInPodWithOutput(ctx, namespace, podName, "hermes", queryCmd, &stdout, &stderr); err != nil {
		return "", fmt.Errorf("failed to query clients: %w", err)
	}

	// Parse the output to find the first client (usually the provider client)
	lines := strings.Split(stdout.String(), "\n")
	for i, line := range lines {
		// Look for client_id line
		if strings.Contains(line, "client_id: ClientId(") {
			// Extract client ID
			for j := i; j < len(lines) && j < i+3; j++ {
				if idx := strings.Index(lines[j], `"`); idx != -1 {
					endIdx := strings.Index(lines[j][idx+1:], `"`)
					if endIdx != -1 {
						clientID := lines[j][idx+1 : idx+1+endIdx]
						m.logger.Info("Found consumer client ID",
							"chain_id", consumerChainID,
							"client_id", clientID)
						return clientID, nil
					}
				}
			}
		}
	}

	return "", fmt.Errorf("no clients found on consumer chain %s", consumerChainID)
}

// queryConnectionID queries the chain to find the connection ID for a given client
func (m *K8sManager) queryConnectionID(ctx context.Context, namespace, podName, chainID, clientID string) (string, error) {
	// Query connections on the chain
	queryCmd := []string{
		"hermes",
		"query", "connections",
		"--chain", chainID,
	}

	var stdout, stderr strings.Builder
	if err := m.execInPodWithOutput(ctx, namespace, podName, "hermes", queryCmd, &stdout, &stderr); err != nil {
		return "", fmt.Errorf("failed to query connections: %w", err)
	}

	// Parse the output to find connection for our client
	lines := strings.Split(stdout.String(), "\n")
	var currentConnectionID string

	for i, line := range lines {
		// Look for connection_id line
		if strings.Contains(line, "connection_id: ConnectionId(") {
			// Extract connection ID
			for j := i; j < len(lines) && j < i+3; j++ {
				if idx := strings.Index(lines[j], `"`); idx != -1 {
					endIdx := strings.Index(lines[j][idx+1:], `"`)
					if endIdx != -1 {
						currentConnectionID = lines[j][idx+1 : idx+1+endIdx]
						break
					}
				}
			}
		}

		// Look for client_id that matches our client
		if strings.Contains(line, "client_id: ClientId(") && currentConnectionID != "" {
			// Check if this connection is for our client
			for j := i; j < len(lines) && j < i+3; j++ {
				if strings.Contains(lines[j], clientID) {
					m.logger.Info("Found connection ID for client",
						"chain_id", chainID,
						"client_id", clientID,
						"connection_id", currentConnectionID)
					return currentConnectionID, nil
				}
			}
		}
	}

	return "", fmt.Errorf("no connection found for client %s on chain %s", clientID, chainID)
}

// execInPodWithOutput executes a command in a pod and captures output
func (m *K8sManager) execInPodWithOutput(ctx context.Context, namespace, podName, containerName string, command []string, stdout, stderr *strings.Builder) error {
	req := m.deployer.GetClientset().CoreV1().RESTClient().
		Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: containerName,
			Command:   command,
			Stdout:    true,
			Stderr:    true,
			TTY:       false,
		}, runtime.NewParameterCodec(scheme.Scheme))

	executor, err := remotecommand.NewSPDYExecutor(m.deployer.GetConfig(), "POST", req.URL())
	if err != nil {
		return fmt.Errorf("failed to create executor: %w", err)
	}

	err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: stdout,
		Stderr: stderr,
	})

	if err != nil {
		return fmt.Errorf("command failed: %w, stdout: %s, stderr: %s", err, stdout.String(), stderr.String())
	}

	// Don't treat stderr output as error if command succeeded
	if stderr.Len() > 0 {
		// Only log as debug since Hermes outputs info to stderr
		m.logger.Debug("Command stderr output", "stderr", stderr.String())
	}

	return nil
}

// execInPod executes a command in a pod
func (m *K8sManager) execInPod(ctx context.Context, namespace, podName, containerName string, command []string) error {
	req := m.deployer.GetClientset().CoreV1().RESTClient().
		Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: containerName,
			Command:   command,
			Stdout:    true,
			Stderr:    true,
			TTY:       false,
		}, runtime.NewParameterCodec(scheme.Scheme))

	executor, err := remotecommand.NewSPDYExecutor(m.deployer.GetConfig(), "POST", req.URL())
	if err != nil {
		return fmt.Errorf("failed to create executor: %w", err)
	}

	var stdout, stderr strings.Builder
	err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})

	if err != nil {
		return fmt.Errorf("command failed: %w, stdout: %s, stderr: %s", err, stdout.String(), stderr.String())
	}

	if stderr.Len() > 0 {
		// Check if it's just a warning
		stderrStr := stderr.String()
		if strings.Contains(stderrStr, "WARN") || strings.Contains(stderrStr, "already exists") {
			m.logger.Warn("Command completed with warnings",
				"stdout", stdout.String(),
				"stderr", stderrStr)
			return nil
		}
		return fmt.Errorf("command error: %s", stderrStr)
	}

	m.logger.Debug("Command executed successfully",
		"stdout", stdout.String())

	return nil
}

// GetHermesStatus returns the status of the Hermes relayer for a consumer chain
func (m *K8sManager) GetHermesStatus(ctx context.Context, chainID string) (*HermesStatus, error) {
	namespace := m.GetNamespaceForChain(chainID)
	deploymentName := fmt.Sprintf("%s-hermes", chainID)

	// Get deployment status
	deployment, err := m.deployer.GetClientset().AppsV1().Deployments(namespace).Get(ctx, deploymentName, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return &HermesStatus{
				ChainID: chainID,
				Status:  "Not Deployed",
			}, nil
		}
		return nil, fmt.Errorf("failed to get Hermes deployment: %w", err)
	}

	// Get pod status
	pods, err := m.deployer.GetClientset().CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("app=hermes,chain-id=%s", chainID),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list Hermes pods: %w", err)
	}

	status := &HermesStatus{
		ChainID:        chainID,
		DeploymentName: deploymentName,
		Namespace:      namespace,
		Replicas:       int(*deployment.Spec.Replicas),
		ReadyReplicas:  int(deployment.Status.ReadyReplicas),
	}

	// Determine overall status
	if len(pods.Items) == 0 {
		status.Status = "No Pods"
	} else {
		pod := &pods.Items[0]
		status.PodName = pod.Name
		status.PodPhase = string(pod.Status.Phase)

		// Check if pod is ready
		isReady := false
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodReady {
				isReady = condition.Status == corev1.ConditionTrue
				break
			}
		}

		if isReady && deployment.Status.ReadyReplicas == *deployment.Spec.Replicas {
			status.Status = "Running"
		} else if pod.Status.Phase == corev1.PodPending {
			status.Status = "Starting"
		} else if pod.Status.Phase == corev1.PodFailed {
			status.Status = "Failed"
		} else {
			status.Status = "Not Ready"
		}

		// Get recent logs (last 10 lines)
		logOptions := &corev1.PodLogOptions{
			Container: "hermes",
			TailLines: int64Ptr(10),
		}
		req := m.deployer.GetClientset().CoreV1().Pods(namespace).GetLogs(pod.Name, logOptions)
		logBytes, err := req.Do(ctx).Raw()
		if err == nil {
			status.RecentLogs = string(logBytes)
		}
	}

	// Check if CCV channel exists by looking for channel in logs
	if strings.Contains(status.RecentLogs, "channel-0") || strings.Contains(status.RecentLogs, "successfully created channel") {
		status.CCVChannelCreated = true
	}

	return status, nil
}

// HermesStatus represents the status of a Hermes relayer instance
type HermesStatus struct {
	ChainID           string
	Status            string
	DeploymentName    string
	Namespace         string
	PodName           string
	PodPhase          string
	Replicas          int
	ReadyReplicas     int
	CCVChannelCreated bool
	RecentLogs        string
}

// int64Ptr returns a pointer to an int64
func int64Ptr(i int64) *int64 {
	return &i
}

// deleteHermesResources deletes all Hermes-related resources for a consumer chain
func (m *K8sManager) deleteHermesResources(ctx context.Context, chainID, namespace string) error {
	m.logger.Info("Deleting Hermes resources", "chain_id", chainID, "namespace", namespace)

	clientset := m.deployer.GetClientset()

	// Delete Hermes deployment
	deploymentName := fmt.Sprintf("%s-hermes", chainID)
	if err := clientset.AppsV1().Deployments(namespace).Delete(ctx, deploymentName, metav1.DeleteOptions{}); err != nil {
		if !errors.IsNotFound(err) {
			m.logger.Warn("Failed to delete Hermes deployment", "name", deploymentName, "error", err)
		}
	} else {
		m.logger.Info("Deleted Hermes deployment", "name", deploymentName)
	}

	// Delete Hermes ConfigMap
	configMapName := fmt.Sprintf("%s-hermes-config", chainID)
	if err := clientset.CoreV1().ConfigMaps(namespace).Delete(ctx, configMapName, metav1.DeleteOptions{}); err != nil {
		if !errors.IsNotFound(err) {
			m.logger.Warn("Failed to delete Hermes ConfigMap", "name", configMapName, "error", err)
		}
	} else {
		m.logger.Info("Deleted Hermes ConfigMap", "name", configMapName)
	}

	return nil
}
