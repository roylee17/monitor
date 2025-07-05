package subnet

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/interchain-security-monitor/internal/deployment"
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
	}, nil
}

// SetPeerDiscovery sets the peer discovery service
func (m *K8sManager) SetPeerDiscovery(pd *PeerDiscovery) {
	m.peerDiscovery = pd
}

// GetClientset returns the Kubernetes clientset from the deployer
func (m *K8sManager) GetClientset() kubernetes.Interface {
	if m.deployer != nil {
		return m.deployer.GetClientset()
	}
	return nil
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
	config.SetDefaults()

	// Handle dynamic peers if requested
	if config.UseDynamicPeers {
		return m.DeployConsumerWithDynamicPeersAndKey(ctx, config.ChainID, config.ConsumerID,
			*config.Ports, config.CCVPatch, config.ConsumerKey)
	}

	// Use the existing implementation
	return m.DeployConsumerWithPortsAndGenesisAndKeys(ctx, config.ChainID, config.ConsumerID,
		*config.Ports, config.Peers, config.CCVPatch, config.NodeKeyJSON, config.ConsumerKey)
}

// DeployConsumerChain orchestrates the full consumer chain deployment workflow
// Deprecated: Use DeployConsumer with ConsumerDeploymentConfig instead
func (m *K8sManager) DeployConsumerChain(ctx context.Context, chainID, consumerID string) error {
	m.logger.Info("Starting consumer chain deployment workflow",
		"chain_id", chainID,
		"consumer_id", consumerID)

	// Phase 1: Pre-CCV Genesis Preparation
	if err := m.preparePreCCVGenesis(chainID); err != nil {
		return fmt.Errorf("failed to prepare pre-CCV genesis: %w", err)
	}

	// Phase 2: Genesis Patching (will be done when CCV data is available)
	// This is handled later in the workflow when the provider chain provides CCV data

	// Phase 3: Kubernetes Deployment
	if err := m.deployToKubernetes(ctx, chainID, consumerID); err != nil {
		return fmt.Errorf("failed to deploy to Kubernetes: %w", err)
	}

	namespace := m.GetNamespaceForChain(chainID)
	m.logger.Info("Consumer chain deployment workflow completed",
		"chain_id", chainID,
		"namespace", namespace)

	return nil
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
			Coins:   sdk.NewCoins(sdk.NewCoin(DefaultDenom, math.NewInt(DefaultRelayerFunds))), // 100M
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

// deployToKubernetes handles the Kubernetes deployment phase
func (m *K8sManager) deployToKubernetes(ctx context.Context, chainID, consumerID string) error {
	namespace := m.GetNamespaceForChain(chainID)
	m.logger.Info("Deploying consumer chain to Kubernetes",
		"chain_id", chainID,
		"namespace", namespace)

	// Calculate hashes for deployment metadata
	subnetdHash, genesisHash, err := m.Manager.CalculateHashes()
	if err != nil {
		// Don't fail deployment for hash calculation issues
		m.logger.Warn("Failed to calculate hashes for deployment", "error", err)
		subnetdHash, genesisHash = "unknown", "unknown"
	}

	// Create deployment configuration
	deploymentConfig := deployment.ConsumerDeployment{
		ChainID:     chainID,
		ConsumerID:  consumerID,
		Image:       m.consumerImage,
		Namespace:   namespace,
		Replicas:    m.defaultReplicas,
		GenesisHash: genesisHash,
		SubnetdHash: subnetdHash,

		// Standard port configuration
		P2PPort:  26656,
		RPCPort:  26657,
		RESTPort: 1317,
		GRPCPort: 9090,

		// Consumer chain configuration
		ConsumerConfig: deployment.ConsumerChainConfig{
			UnbondingPeriod:       "1728000s", // 20 days
			CCVTimeoutPeriod:      "2419200s", // 28 days
			TransferTimeout:       "1800s",    // 30 minutes
			BlocksPerTransmission: 1000,
			RedistributionFrac:    "0.75",
		},
	}

	// Deploy to Kubernetes
	if err := m.deployer.DeployConsumerChain(ctx, deploymentConfig); err != nil {
		return fmt.Errorf("failed to deploy consumer chain: %w", err)
	}

	m.logger.Info("Consumer chain deployed to Kubernetes successfully",
		"chain_id", chainID,
		"namespace", namespace)

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

// Enhanced workflow methods that integrate with the existing manager

// StartSubnetWithK8s starts a subnet using the Kubernetes deployment approach
func (m *K8sManager) StartSubnetWithK8s(ctx context.Context, chainID string) error {
	m.logger.Info("Starting subnet with Kubernetes deployment", "chain_id", chainID)

	// For Kubernetes deployment, the actual "start" happens when the deployment is created
	// We can verify that the deployment is running and healthy
	status, err := m.GetConsumerChainStatus(ctx, chainID)
	if err != nil {
		return fmt.Errorf("failed to get consumer chain status: %w", err)
	}

	if status.ReadyReplicas == 0 {
		return fmt.Errorf("consumer chain deployment is not ready: %d/%d replicas ready",
			status.ReadyReplicas, status.DesiredReplicas)
	}

	m.logger.Info("Subnet started successfully in Kubernetes",
		"chain_id", chainID,
		"ready_replicas", status.ReadyReplicas)

	return nil
}

// JoinSubnetWithK8s joins an existing subnet using Kubernetes deployment
func (m *K8sManager) JoinSubnetWithK8s(ctx context.Context, chainID string, networkInfo NetworkInfo) error {
	m.logger.Info("Joining subnet with Kubernetes deployment",
		"chain_id", chainID)

	namespace := m.GetNamespaceForChain(chainID)
	// Deploy consumer chain
	deploymentConfig := deployment.ConsumerDeployment{
		ChainID:    chainID,
		ConsumerID: "", // Will be set from context
		Image:      m.consumerImage,
		Namespace:  namespace,
		Replicas:   m.defaultReplicas,

		// Standard port configuration
		P2PPort:  26656,
		RPCPort:  26657,
		RESTPort: 1317,

		// Consumer chain configuration
		ConsumerConfig: deployment.ConsumerChainConfig{
			UnbondingPeriod:       "1728000s",
			CCVTimeoutPeriod:      "2419200s",
			TransferTimeout:       "1800s",
			BlocksPerTransmission: 1000,
			RedistributionFrac:    "0.75",
		},
	}

	// Deploy to Kubernetes
	if err := m.deployer.DeployConsumerChain(ctx, deploymentConfig); err != nil {
		return fmt.Errorf("failed to deploy consumer chain: %w", err)
	}

	m.logger.Info("Successfully joined subnet with Kubernetes deployment", "chain_id", chainID)

	return nil
}

// DeployConsumerWithPorts deploys consumer chain with specific ports
// DeployConsumerWithPorts deploys a consumer chain with specific port configuration
// Deprecated: Use DeployConsumerWithPortsAndGenesis instead
func (m *K8sManager) DeployConsumerWithPorts(ctx context.Context, chainID, consumerID string, ports Ports, peers []string) error {
	return m.DeployConsumerWithPortsAndGenesis(ctx, chainID, consumerID, ports, peers, nil)
}

// DeployConsumerWithPortsAndGenesis deploys a consumer chain with specific port configuration and CCV genesis
func (m *K8sManager) DeployConsumerWithPortsAndGenesis(ctx context.Context, chainID, consumerID string, ports Ports, peers []string, ccvPatch map[string]interface{}) error {
	return m.DeployConsumerWithPortsAndGenesisForValidator(ctx, chainID, consumerID, "", ports, peers, ccvPatch)
}

// DeployConsumerWithPortsAndGenesisAndNodeKey deploys a consumer chain with specific port configuration, CCV genesis, and node key
func (m *K8sManager) DeployConsumerWithPortsAndGenesisAndNodeKey(ctx context.Context, chainID, consumerID string, ports Ports, peers []string, ccvPatch map[string]interface{}, nodeKeyJSON string) error {
	return m.DeployConsumerWithPortsAndGenesisForValidatorWithNodeKey(ctx, chainID, consumerID, "", ports, peers, ccvPatch, nodeKeyJSON)
}

// DeployConsumerWithPortsAndGenesisForValidator deploys a consumer chain instance for a specific validator
func (m *K8sManager) DeployConsumerWithPortsAndGenesisForValidator(ctx context.Context, chainID, consumerID, validatorName string, ports Ports, peers []string, ccvPatch map[string]interface{}) error {
	return m.DeployConsumerWithPortsAndGenesisForValidatorWithNodeKey(ctx, chainID, consumerID, validatorName, ports, peers, ccvPatch, "")
}

// DeployConsumerWithPortsAndGenesisForValidatorWithNodeKey deploys a consumer chain instance with all options
func (m *K8sManager) DeployConsumerWithPortsAndGenesisForValidatorWithNodeKey(ctx context.Context, chainID, consumerID, validatorName string, ports Ports, peers []string, ccvPatch map[string]interface{}, nodeKeyJSON string) error {
	return m.DeployConsumerWithPortsAndGenesisAndKeys(ctx, chainID, consumerID, ports, peers, ccvPatch, nodeKeyJSON, nil)
}

// DeployConsumerWithPortsAndGenesisAndKeys deploys a consumer chain instance with all options including consumer key
func (m *K8sManager) DeployConsumerWithPortsAndGenesisAndKeys(ctx context.Context, chainID, consumerID string, ports Ports, peers []string, ccvPatch map[string]interface{}, nodeKeyJSON string, consumerKey *ConsumerKeyInfo) error {
	// Determine validator name from consumer key or environment
	validatorName := ""
	if consumerKey != nil {
		validatorName = consumerKey.ValidatorName
	} else {
		validatorName = os.Getenv("VALIDATOR_NAME")
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
			UnbondingPeriod:       "1728000s", // 20 days
			CCVTimeoutPeriod:      "2419200s", // 28 days
			TransferTimeout:       "1800s",    // 30 minutes
			BlocksPerTransmission: 1000,
			RedistributionFrac:    "0.75",
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
			P2P:  int(CalculateValidatorNodePort(chainID, validatorName)),
			RPC:  int(CalculateValidatorNodePort(chainID, validatorName)) + 1,
			GRPC: int(CalculateValidatorNodePort(chainID, validatorName)) + 2,
		},
	}

	// Add consumer validator key if provided
	if consumerKey != nil && len(consumerKey.PrivateKey) > 0 {
		// Create the priv_validator_key.json structure
		privKeyJSON := map[string]interface{}{
			"address": consumerKey.ConsumerAddress,
			"pub_key": map[string]interface{}{
				"type":  "tendermint/PubKeyEd25519",
				"value": base64.StdEncoding.EncodeToString(consumerKey.PrivateKey[32:]), // Ed25519 public key is last 32 bytes
			},
			"priv_key": map[string]interface{}{
				"type":  "tendermint/PrivKeyEd25519",
				"value": base64.StdEncoding.EncodeToString(consumerKey.PrivateKey), // Full 64 bytes
			},
		}

		privKeyData, err := json.Marshal(privKeyJSON)
		if err != nil {
			return fmt.Errorf("failed to marshal consumer key: %w", err)
		}

		deploymentConfig.ConsumerKeyJSON = string(privKeyData)
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
	
	// Retry for up to 60 seconds
	maxRetries := 12
	retryInterval := 5 * time.Second
	var podIP string
	
	for i := 0; i < maxRetries; i++ {
		// Get consumer chain status
		status, err := m.deployer.GetConsumerChainStatus(ctx, chainID, namespace)
		if err != nil {
			m.logger.Warn("Failed to get consumer chain status, will retry",
				"chain_id", chainID,
				"attempt", i+1,
				"error", err)
			time.Sleep(retryInterval)
			continue
		}

		// Look for a ready pod
		for _, pod := range status.Pods {
			if pod.Ready && pod.PodIP != "" {
				podIP = pod.PodIP
				break
			}
		}

		if podIP != "" {
			break
		}

		m.logger.Info("Consumer pod not ready yet, waiting...",
			"chain_id", chainID,
			"attempt", i+1,
			"max_attempts", maxRetries)
		time.Sleep(retryInterval)
	}

	if podIP == "" {
		// Even after retries, no pod is ready
		m.logger.Warn("Consumer pod still not ready after retries, LoadBalancer configuration deferred",
			"chain_id", chainID)
		// Start a goroutine to configure LoadBalancer later
		go m.configureLoadBalancerWhenReady(ctx, chainID, namespace, ports.P2P)
	} else {
		// Configure LoadBalancer for this consumer chain
		calculatedPort := ports.P2P
		
		// Add port to LoadBalancer
		if err := m.lbManager.AddConsumerPort(ctx, chainID, int32(calculatedPort)); err != nil {
			m.logger.Error("Failed to add port to LoadBalancer", 
				"chain_id", chainID,
				"port", calculatedPort,
				"error", err)
			// Don't fail deployment, but log the error
		}
		
		// Create EndpointSlice for LoadBalancer routing (using modern API)
		if err := m.lbManager.CreateEndpointSlice(ctx, chainID, namespace, podIP, int32(calculatedPort)); err != nil {
			m.logger.Error("Failed to create EndpointSlice for LoadBalancer",
				"chain_id", chainID,
				"error", err)
			// Don't fail deployment, but log the error
		}
		
		m.logger.Info("LoadBalancer configured for consumer chain",
			"chain_id", chainID,
			"port", calculatedPort,
			"pod_ip", podIP)
	}

	m.logger.Info("Consumer chain deployed with stateless peer discovery successfully",
		"chain_id", chainID,
		"validator", validatorName,
		"peers_count", len(peers))

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

// GetNetworkInfoFromK8s gets network info from the Kubernetes deployment
func (m *K8sManager) GetNetworkInfoFromK8s(ctx context.Context, chainID string) (NetworkInfo, error) {
	status, err := m.GetConsumerChainStatus(ctx, chainID)
	if err != nil {
		return NetworkInfo{}, fmt.Errorf("failed to get consumer chain status: %w", err)
	}

	if len(status.Pods) == 0 {
		return NetworkInfo{}, fmt.Errorf("no pods found for consumer chain %s", chainID)
	}

	// Get network info from the first ready pod
	for _, pod := range status.Pods {
		if pod.Ready {
			networkInfo := NetworkInfo{
				P2PAddress: fmt.Sprintf("tcp://%s:26656", pod.PodIP),
				RPCAddress: fmt.Sprintf("tcp://%s:26657", pod.PodIP),
				Peers:      []string{}, // Will be populated from actual P2P discovery
			}

			return networkInfo, nil
		}
	}

	return NetworkInfo{}, fmt.Errorf("no ready pods found for consumer chain %s", chainID)
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
func (m *K8sManager) DeployConsumerWithDynamicPeers(ctx context.Context, chainID, consumerID string, ports Ports, ccvPatch map[string]interface{}) error {
	return m.DeployConsumerWithDynamicPeersAndKey(ctx, chainID, consumerID, ports, ccvPatch, nil)
}

// DeployConsumerWithDynamicPeersAndKey deploys a consumer chain with dynamically discovered peers and optional consumer key
func (m *K8sManager) DeployConsumerWithDynamicPeersAndKey(ctx context.Context, chainID, consumerID string, ports Ports, ccvPatch map[string]interface{}, consumerKey *ConsumerKeyInfo) error {
	return m.DeployConsumerWithDynamicPeersAndKeyAndValidators(ctx, chainID, consumerID, ports, ccvPatch, consumerKey, nil)
}

// DeployConsumerWithDynamicPeersAndKeyAndValidators deploys a consumer chain with dynamically discovered peers, optional consumer key, and explicit validator list
func (m *K8sManager) DeployConsumerWithDynamicPeersAndKeyAndValidators(ctx context.Context, chainID, consumerID string, ports Ports, ccvPatch map[string]interface{}, consumerKey *ConsumerKeyInfo, actualOptedInValidators []string) error {
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
	return m.DeployConsumerWithPortsAndGenesisAndKeys(ctx, chainID, consumerID, ports, peers, ccvPatch, nodeKeyJSON, consumerKey)
}

// EnsureLoadBalancerReady checks if the shared LoadBalancer is ready and returns its endpoint
func (k *K8sManager) EnsureLoadBalancerReady(ctx context.Context) (string, error) {
	return k.lbManager.EnsureLoadBalancerReady(ctx)
}

// configureLoadBalancerWhenReady is a background task that waits for consumer pod to be ready
// and then configures the LoadBalancer
func (m *K8sManager) configureLoadBalancerWhenReady(ctx context.Context, chainID, namespace string, p2pPort int) {
	m.logger.Info("Starting background LoadBalancer configuration task",
		"chain_id", chainID)
	
	// Continue retrying for up to 5 minutes
	maxRetries := 60
	retryInterval := 5 * time.Second
	
	for i := 0; i < maxRetries; i++ {
		// Check if context is cancelled
		select {
		case <-ctx.Done():
			m.logger.Info("LoadBalancer configuration cancelled",
				"chain_id", chainID)
			return
		default:
		}
		
		// Get consumer chain status
		status, err := m.deployer.GetConsumerChainStatus(ctx, chainID, namespace)
		if err != nil {
			m.logger.Warn("Failed to get consumer chain status in background task",
				"chain_id", chainID,
				"attempt", i+1,
				"error", err)
			time.Sleep(retryInterval)
			continue
		}
		
		// Look for a ready pod
		var podIP string
		for _, pod := range status.Pods {
			if pod.Ready && pod.PodIP != "" {
				podIP = pod.PodIP
				break
			}
		}
		
		if podIP != "" {
			// Configure LoadBalancer
			if err := m.lbManager.AddConsumerPort(ctx, chainID, int32(p2pPort)); err != nil {
				m.logger.Error("Failed to add port to LoadBalancer in background task",
					"chain_id", chainID,
					"port", p2pPort,
					"error", err)
			} else {
				// Create EndpointSlice for LoadBalancer routing (using modern API)
				if err := m.lbManager.CreateEndpointSlice(ctx, chainID, namespace, podIP, int32(p2pPort)); err != nil {
					m.logger.Error("Failed to create EndpointSlice in background task",
						"chain_id", chainID,
						"error", err)
				} else {
					m.logger.Info("LoadBalancer configured successfully in background task",
						"chain_id", chainID,
						"port", p2pPort,
						"pod_ip", podIP,
						"attempt", i+1)
				}
			}
			return
		}
		
		time.Sleep(retryInterval)
	}
	
	m.logger.Error("Failed to configure LoadBalancer after all retries",
		"chain_id", chainID,
		"max_retries", maxRetries)
}

