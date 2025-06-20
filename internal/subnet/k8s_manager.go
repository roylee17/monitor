package subnet

import (
	"context"
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
	*Manager       // Embed the original manager for file-based operations
	deployer       *deployment.K8sDeployer
	hermesDeployer *deployment.HermesDeployer
	logger         *slog.Logger

	// Configuration
	namespacePrefix string // Prefix for consumer chain namespaces (e.g., "alice", "bob")
	consumerImage   string
	defaultReplicas int32
	validatorName   string
	
	// Peer discovery
	peerDiscovery   *SimplePeerDiscovery
	deterministicPeerDiscovery *DeterministicPeerDiscovery
}

// NewK8sManager creates a new Kubernetes-aware subnet manager
func NewK8sManager(baseManager *Manager, logger *slog.Logger, namespacePrefix, consumerImage, validatorName string) (*K8sManager, error) {
	// Create deployer with empty namespace - we'll set it per deployment
	deployer, err := deployment.NewK8sDeployer(logger, "")
	if err != nil {
		return nil, fmt.Errorf("failed to create K8s deployer: %w", err)
	}

	// Get the same clientset for Hermes deployer
	clientset := deployer.GetClientset()
	hermesDeployer := deployment.NewHermesDeployer(logger, clientset)

	return &K8sManager{
		Manager:         baseManager,
		deployer:        deployer,
		hermesDeployer:  hermesDeployer,
		logger:          logger,
		namespacePrefix: namespacePrefix,
		consumerImage:   consumerImage,
		defaultReplicas: 1,
		validatorName:   validatorName,
	}, nil
}

// SetPeerDiscovery sets the peer discovery service
func (m *K8sManager) SetPeerDiscovery(pd *SimplePeerDiscovery) {
	m.peerDiscovery = pd
}

// SetDeterministicPeerDiscovery sets the deterministic peer discovery service
func (m *K8sManager) SetDeterministicPeerDiscovery(pd *DeterministicPeerDiscovery) {
	m.deterministicPeerDiscovery = pd
}

// GetClientset returns the Kubernetes clientset from the deployer
func (m *K8sManager) GetClientset() kubernetes.Interface {
	if m.deployer != nil {
		return m.deployer.GetClientset()
	}
	return nil
}

// getNamespaceForChain returns the namespace for a specific consumer chain
func (m *K8sManager) getNamespaceForChain(chainID string) string {
	if m.namespacePrefix == "" {
		return chainID
	}
	return fmt.Sprintf("%s-%s", m.namespacePrefix, chainID)
}

// DeployConsumerChain orchestrates the full consumer chain deployment workflow
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

	namespace := m.getNamespaceForChain(chainID)
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
	relayerAddr := GenerateRelayerAddress(m.validatorName, chainID)
	relayerAccounts := []RelayerAccount{
		{
			Address: relayerAddr,
			Coins:   sdk.NewCoins(sdk.NewCoin("stake", math.NewInt(10000000))), // 10 STAKE
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
	genesisData, err := json.MarshalIndent(preCCVGenesis, "", "  ")
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
	namespace := m.getNamespaceForChain(chainID)
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

	namespace := m.getNamespaceForChain(chainID)
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
	namespace := m.getNamespaceForChain(chainID)
	return m.deployer.GetConsumerChainStatus(ctx, chainID, namespace)
}

// StartHermes deploys and starts the Hermes relayer for the consumer chain
func (m *K8sManager) StartHermes(ctx context.Context, chainID string) error {
	m.logger.Info("Starting Hermes relayer deployment", "chain_id", chainID)

	namespace := m.getNamespaceForChain(chainID)
	// Generate Hermes configuration
	providerRPC := "http://validator-alice.provider.svc.cluster.local:26657"
	consumerRPC := fmt.Sprintf("http://%s.%s.svc.cluster.local:26657", chainID, namespace)

	configPath, err := m.hermesManager.GenerateConfig(chainID, chainID, providerRPC, consumerRPC)
	if err != nil {
		return fmt.Errorf("failed to generate Hermes config: %w", err)
	}

	// Read the generated config
	configData, err := m.readHermesConfig(configPath)
	if err != nil {
		return fmt.Errorf("failed to read Hermes config: %w", err)
	}

	// Deploy Hermes to Kubernetes
	if err := m.hermesDeployer.DeployHermes(ctx, "provider-1", chainID, namespace, configData); err != nil {
		return fmt.Errorf("failed to deploy Hermes: %w", err)
	}

	// Wait for Hermes to be ready
	if err := m.waitForHermesReady(ctx, chainID); err != nil {
		return fmt.Errorf("Hermes failed to become ready: %w", err)
	}

	m.logger.Info("Hermes relayer deployed successfully", "chain_id", chainID)

	// TODO: Implement IBC path creation after Hermes is running
	m.logger.Info("Note: IBC path creation must be done manually for now",
		"next_steps", "hermes create channel --a-chain provider-1 --b-chain "+chainID)

	return nil
}

// readHermesConfig reads the generated Hermes configuration file
func (m *K8sManager) readHermesConfig(configPath string) ([]byte, error) {
	// Read from the file system where hermesManager saved it
	return os.ReadFile(configPath)
}

// waitForHermesReady waits for Hermes deployment to be ready
func (m *K8sManager) waitForHermesReady(ctx context.Context, chainID string) error {
	m.logger.Info("Waiting for Hermes to be ready", "chain_id", chainID)

	namespace := m.getNamespaceForChain(chainID)
	timeout := time.After(2 * time.Minute)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			return fmt.Errorf("timeout waiting for Hermes to be ready")
		case <-ticker.C:
			status, err := m.hermesDeployer.GetHermesStatus(ctx, chainID, namespace)
			if err != nil {
				m.logger.Debug("Failed to get Hermes status", "error", err)
				continue
			}

			if status.Ready {
				m.logger.Info("Hermes is ready", "chain_id", chainID)
				return nil
			}

			m.logger.Debug("Hermes not ready yet",
				"ready_replicas", status.ReadyReplicas,
				"total_replicas", status.Replicas)
		}
	}
}

// MonitorConsumerChainHealth monitors the health of deployed consumer chains
func (m *K8sManager) MonitorConsumerChainHealth(ctx context.Context, chainID string) error {
	m.logger.Info("Starting health monitoring for consumer chain", "chain_id", chainID)

	ticker := time.NewTicker(30 * time.Second)
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

	namespace := m.getNamespaceForChain(chainID)
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
func (m *K8sManager) DeployConsumerWithPorts(ctx context.Context, chainID, consumerID string, ports ConsumerPorts, peers []string) error {
	return m.DeployConsumerWithPortsAndGenesis(ctx, chainID, consumerID, ports, peers, nil)
}

// DeployConsumerWithPortsAndGenesis deploys a consumer chain with specific port configuration and CCV genesis
func (m *K8sManager) DeployConsumerWithPortsAndGenesis(ctx context.Context, chainID, consumerID string, ports ConsumerPorts, peers []string, ccvPatch map[string]interface{}) error {
	return m.DeployConsumerWithPortsAndGenesisForValidator(ctx, chainID, consumerID, "", ports, peers, ccvPatch)
}

// DeployConsumerWithPortsAndGenesisAndNodeKey deploys a consumer chain with specific port configuration, CCV genesis, and node key
func (m *K8sManager) DeployConsumerWithPortsAndGenesisAndNodeKey(ctx context.Context, chainID, consumerID string, ports ConsumerPorts, peers []string, ccvPatch map[string]interface{}, nodeKeyJSON string) error {
	return m.DeployConsumerWithPortsAndGenesisForValidatorWithNodeKey(ctx, chainID, consumerID, "", ports, peers, ccvPatch, nodeKeyJSON)
}

// DeployConsumerWithPortsAndGenesisForValidator deploys a consumer chain instance for a specific validator
func (m *K8sManager) DeployConsumerWithPortsAndGenesisForValidator(ctx context.Context, chainID, consumerID, validatorName string, ports ConsumerPorts, peers []string, ccvPatch map[string]interface{}) error {
	return m.DeployConsumerWithPortsAndGenesisForValidatorWithNodeKey(ctx, chainID, consumerID, validatorName, ports, peers, ccvPatch, "")
}

// DeployConsumerWithPortsAndGenesisForValidatorWithNodeKey deploys a consumer chain instance with all options
func (m *K8sManager) DeployConsumerWithPortsAndGenesisForValidatorWithNodeKey(ctx context.Context, chainID, consumerID, validatorName string, ports ConsumerPorts, peers []string, ccvPatch map[string]interface{}, nodeKeyJSON string) error {
	m.logger.Info("Deploying consumer with specific ports",
		"chain_id", chainID,
		"consumer_id", consumerID,
		"validator", validatorName,
		"p2p_port", ports.P2P,
		"rpc_port", ports.RPC)

	// Calculate hashes for deployment metadata
	subnetdHash, genesisHash, err := m.Manager.CalculateHashes()
	if err != nil {
		// Don't fail deployment for hash calculation issues
		m.logger.Warn("Failed to calculate hashes for deployment", "error", err)
		subnetdHash, genesisHash = "unknown", "unknown"
	}

	namespace := m.getNamespaceForChain(chainID)
	// Create deployment configuration with deterministic ports
	deploymentConfig := deployment.ConsumerDeployment{
		ChainID:       chainID,
		ConsumerID:    consumerID,
		Image:         m.consumerImage,
		Namespace:     namespace,
		Replicas:      m.defaultReplicas,
		GenesisHash:   genesisHash,
		SubnetdHash:   subnetdHash,
		ValidatorName: validatorName, // Add validator name for unique deployment

		// Use deterministic ports from peer discovery
		P2PPort:  ports.P2P,
		RPCPort:  ports.RPC,
		RESTPort: ports.API,
		GRPCPort: ports.GRPC,

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

	namespace := m.getNamespaceForChain(chainID)
	// Delete Hermes relayer if it exists
	if err := m.hermesDeployer.DeleteHermes(ctx, chainID, namespace); err != nil {
		m.logger.Warn("Failed to delete Hermes deployment", "error", err)
		// Continue with consumer chain removal even if Hermes deletion fails
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
	namespace := m.getNamespaceForChain(chainID)
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

	// Convert CCV patch to JSON
	ccvPatchJSON, err := json.Marshal(ccvPatch)
	if err != nil {
		return fmt.Errorf("failed to marshal CCV patch: %w", err)
	}

	namespace := m.getNamespaceForChain(chainID)
	// Update the ConfigMap through the deployer
	configMapName := fmt.Sprintf("%s-config", chainID)
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

	namespace := m.getNamespaceForChain(chainID)
	deploymentName := fmt.Sprintf("%s-consumer", chainID)
	return m.deployer.RestartDeployment(ctx, deploymentName, namespace)
}

// waitForDeploymentReady waits for the deployment to become ready after restart
func (m *K8sManager) waitForDeploymentReady(ctx context.Context, chainID string) error {
	m.logger.Info("Waiting for deployment to become ready", "chain_id", chainID)

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	timeout := time.After(2 * time.Minute)

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
func (m *K8sManager) DeployConsumerWithDynamicPeers(ctx context.Context, chainID, consumerID string, ports ConsumerPorts, ccvPatch map[string]interface{}) error {
	return m.DeployConsumerWithDynamicPeersAndKey(ctx, chainID, consumerID, ports, ccvPatch, nil)
}

// DeployConsumerWithDynamicPeersAndKey deploys a consumer chain with dynamically discovered peers and optional consumer key
func (m *K8sManager) DeployConsumerWithDynamicPeersAndKey(ctx context.Context, chainID, consumerID string, ports ConsumerPorts, ccvPatch map[string]interface{}, consumerKey *ConsumerKeyInfo) error {
	m.logger.Info("Deploying consumer with dynamic peer discovery",
		"chain_id", chainID,
		"consumer_id", consumerID)

	// Discover peers using deterministic peer discovery if configured
	var peers []string
	var nodeKeyJSON string
	
	if m.deterministicPeerDiscovery != nil {
		// Get the list of opted-in validators from the CCV patch
		var optedInValidators []string
		if ccvPatch != nil {
			if appState, ok := ccvPatch["app_state"].(map[string]interface{}); ok {
				if ccvConsumer, ok := appState["ccvconsumer"].(map[string]interface{}); ok {
					if provider, ok := ccvConsumer["provider"].(map[string]interface{}); ok {
						if initialValSet, ok := provider["initial_val_set"].([]interface{}); ok {
							for range initialValSet {
								// Extract moniker from the validator
								// Note: This is a simplified approach - in production you'd map consensus keys to monikers
								optedInValidators = append(optedInValidators, "alice", "bob", "charlie")
								break // For now, just use all validators
							}
						}
					}
				}
			}
		}
		
		// Fallback to all validators if we couldn't extract from CCV patch
		if len(optedInValidators) == 0 {
			optedInValidators = []string{"alice", "bob", "charlie"}
		}
		
		// Discover peers with deterministic node IDs
		discoveredPeers, err := m.deterministicPeerDiscovery.DiscoverPeersWithChainID(ctx, consumerID, chainID, optedInValidators)
		if err != nil {
			m.logger.Warn("Failed to discover peers with deterministic IDs, continuing without peers",
				"error", err)
			peers = []string{}
		} else {
			peers = discoveredPeers
			m.logger.Info("Discovered peers with deterministic node IDs",
				"chain_id", chainID,
				"peer_count", len(peers),
				"peers", peers)
		}
		
		// Generate deterministic node key for this validator
		nodeKeyBytes, err := m.deterministicPeerDiscovery.GetNodeKeyJSON(chainID)
		if err != nil {
			m.logger.Warn("Failed to generate deterministic node key",
				"error", err)
		} else {
			nodeKeyJSON = string(nodeKeyBytes)
			m.logger.Info("Generated deterministic node key for consumer chain",
				"chain_id", chainID,
				"validator", m.validatorName)
		}
	} else if m.peerDiscovery != nil {
		// Fallback to simple peer discovery
		discoveredPeers, err := m.peerDiscovery.DiscoverPeersWithChainID(ctx, consumerID, chainID)
		if err != nil {
			m.logger.Warn("Failed to discover peers dynamically, continuing without peers",
				"error", err)
			peers = []string{}
		} else {
			peers = discoveredPeers
			m.logger.Info("Discovered peers for consumer chain",
				"chain_id", chainID,
				"peer_count", len(peers),
				"peers", peers)
		}
	} else {
		m.logger.Warn("No peer discovery configured, starting without peers")
		peers = []string{}
	}

	// Deploy with discovered peers and deterministic node key
	return m.DeployConsumerWithPortsAndGenesisAndNodeKey(ctx, chainID, consumerID, ports, peers, ccvPatch, nodeKeyJSON)
}
