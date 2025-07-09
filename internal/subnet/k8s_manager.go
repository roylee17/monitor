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
	"k8s.io/client-go/kubernetes"
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
