package deployment

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Consumer NodePort constants
const (
	// ConsumerNodePortBase is the base NodePort for consumer chain P2P services
	ConsumerNodePortBase = 30100
	
	// ConsumerMaxPorts is the maximum number of consumer ports (30100-30199)
	ConsumerMaxPorts = 100
)

// calculateConsumerNodePort calculates the NodePort for a consumer chain's P2P service.
// This uses the same logic as gateway ports to ensure consistency.
func calculateConsumerNodePort(chainID string) int32 {
	// Use SHA256 for better distribution
	hash := sha256.Sum256([]byte(chainID))
	
	// Use first 4 bytes as uint32
	hashNum := binary.BigEndian.Uint32(hash[:4])
	
	// Calculate offset within allowed range
	offset := hashNum % ConsumerMaxPorts
	
	return int32(ConsumerNodePortBase + offset)
}

// ScaleDeployment scales a deployment to the specified number of replicas
func (d *K8sDeployer) ScaleDeployment(ctx context.Context, chainID string, replicas int32) error {
	deploymentName := chainID // No suffix, matching createConsumerDeployment

	// Get the deployment
	deployment, err := d.clientset.AppsV1().Deployments(d.namespace).Get(ctx, deploymentName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get deployment: %w", err)
	}

	// Update replica count
	deployment.Spec.Replicas = &replicas

	// Update the deployment
	_, err = d.clientset.AppsV1().Deployments(d.namespace).Update(ctx, deployment, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update deployment: %w", err)
	}

	return nil
}

// DeleteConsumerChain deletes all resources for a consumer chain
func (d *K8sDeployer) DeleteConsumerChain(ctx context.Context, chainID string) error {
	d.logger.Info("Deleting consumer chain resources", "chain_id", chainID, "namespace", d.namespace)

	// Delete deployment
	deploymentName := chainID // No suffix, matching createConsumerDeployment
	if err := d.clientset.AppsV1().Deployments(d.namespace).Delete(ctx, deploymentName, metav1.DeleteOptions{}); err != nil {
		d.logger.Warn("Failed to delete deployment", "error", err)
	}

	// Delete service
	serviceName := chainID // No suffix, matching createConsumerService
	if err := d.clientset.CoreV1().Services(d.namespace).Delete(ctx, serviceName, metav1.DeleteOptions{}); err != nil {
		d.logger.Warn("Failed to delete service", "error", err)
	}

	// Delete ConfigMap
	configMapName := fmt.Sprintf("%s-config", chainID) // Matching createConsumerConfigMap
	if err := d.clientset.CoreV1().ConfigMaps(d.namespace).Delete(ctx, configMapName, metav1.DeleteOptions{}); err != nil {
		d.logger.Warn("Failed to delete ConfigMap", "error", err)
	}

	return nil
}

// GetClientset returns the Kubernetes clientset
func (d *K8sDeployer) GetClientset() kubernetes.Interface {
	return d.clientset
}

// GetConfig returns the Kubernetes REST config
func (d *K8sDeployer) GetConfig() *rest.Config {
	return d.config
}

// UpdateConfigMap updates a ConfigMap with new data
func (d *K8sDeployer) UpdateConfigMap(ctx context.Context, namespace, name string, data map[string]string) error {
	// Get existing ConfigMap
	configMap, err := d.clientset.CoreV1().ConfigMaps(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get ConfigMap: %w", err)
	}

	// Update data
	for k, v := range data {
		configMap.Data[k] = v
	}

	// Update the ConfigMap
	_, err = d.clientset.CoreV1().ConfigMaps(namespace).Update(ctx, configMap, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update ConfigMap: %w", err)
	}

	return nil
}

// ConsumerDeployment represents a consumer chain deployment configuration
type ConsumerDeployment struct {
	ChainID       string
	ConsumerID    string
	Image         string
	Namespace     string
	Replicas      int32
	GenesisHash   string
	SubnetdHash   string
	ValidatorName string // Name of the validator running this instance (for multi-validator deployments)

	// Network configuration
	P2PPort  int32
	RPCPort  int32
	RESTPort int32
	GRPCPort int32

	// Peer configuration
	PersistentPeers string

	// CCV Genesis patch
	CCVPatch map[string]interface{}

	// NodePort configuration
	NodePorts struct {
		P2P  int
		RPC  int
		GRPC int
	}

	// Deterministic node key JSON
	NodeKeyJSON string

	// Consumer validator key (if assigned)
	ConsumerKeyJSON string

	// Consumer chain specific config
	ConsumerConfig ConsumerChainConfig
}

// ConsumerChainConfig holds consumer-specific configuration
type ConsumerChainConfig struct {
	UnbondingPeriod       string
	CCVTimeoutPeriod      string
	TransferTimeout       string
	BlocksPerTransmission int64
	RedistributionFrac    string
}

// K8sDeployer manages Kubernetes deployments for consumer chains
type K8sDeployer struct {
	clientset *kubernetes.Clientset
	config    *rest.Config
	logger    *slog.Logger
	namespace string
}

// NewK8sDeployer creates a new Kubernetes deployer
func NewK8sDeployer(logger *slog.Logger, namespace string) (*K8sDeployer, error) {
	// Try in-cluster config first, then fall back to kubeconfig
	config, err := rest.InClusterConfig()
	if err != nil {
		// Fall back to kubeconfig
		config, err = clientcmd.BuildConfigFromFlags("", clientcmd.RecommendedHomeFile)
		if err != nil {
			return nil, fmt.Errorf("failed to create kubernetes config: %w", err)
		}
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes clientset: %w", err)
	}

	return &K8sDeployer{
		clientset: clientset,
		config:    config,
		logger:    logger,
		namespace: namespace,
	}, nil
}

// EnsureNamespace creates a namespace if it doesn't exist
func (d *K8sDeployer) EnsureNamespace(ctx context.Context, namespace string) error {
	// Get the namespace to check if it exists
	_, err := d.clientset.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			// Create the namespace
			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: namespace,
					Labels: map[string]string{
						"app.kubernetes.io/managed-by": "ics-monitor",
						"app.kubernetes.io/part-of":    "consumer-chains",
					},
				},
			}
			_, err = d.clientset.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("failed to create namespace: %w", err)
			}
			d.logger.Info("Created namespace for consumer chain", "namespace", namespace)
		} else {
			return fmt.Errorf("failed to get namespace: %w", err)
		}
	}
	return nil
}

// DeployConsumerChain deploys a consumer chain to Kubernetes
func (d *K8sDeployer) DeployConsumerChain(ctx context.Context, deployment ConsumerDeployment) error {
	d.logger.Info("Deploying consumer chain to Kubernetes",
		"chain_id", deployment.ChainID,
		"consumer_id", deployment.ConsumerID,
		"namespace", deployment.Namespace)

	// Ensure namespace exists first
	if err := d.EnsureNamespace(ctx, deployment.Namespace); err != nil {
		return fmt.Errorf("failed to ensure namespace: %w", err)
	}

	// Create ConfigMap for genesis and configuration
	if err := d.createConsumerConfigMap(ctx, deployment); err != nil {
		return fmt.Errorf("failed to create config map: %w", err)
	}

	// Create Service for consumer chain
	if err := d.createConsumerService(ctx, deployment); err != nil {
		return fmt.Errorf("failed to create service: %w", err)
	}

	// Create Deployment for consumer chain
	if err := d.createConsumerDeployment(ctx, deployment); err != nil {
		return fmt.Errorf("failed to create deployment: %w", err)
	}

	d.logger.Info("Consumer chain deployment completed successfully",
		"chain_id", deployment.ChainID)

	return nil
}

// getResourceName generates a resource name, optionally including validator name
func (d *K8sDeployer) getResourceName(chainID, validatorName, suffix string) string {
	if validatorName != "" {
		if suffix != "" {
			return fmt.Sprintf("%s-%s-%s", chainID, validatorName, suffix)
		}
		return fmt.Sprintf("%s-%s", chainID, validatorName)
	}
	if suffix != "" {
		return fmt.Sprintf("%s-%s", chainID, suffix)
	}
	return chainID
}

// getLabels returns standard labels for consumer chain resources
func (d *K8sDeployer) getLabels(deployment ConsumerDeployment) map[string]string {
	labels := map[string]string{
		"app":                        "consumer-chain",
		"chain-id":                   deployment.ChainID,
		"consumer-chain-id":          deployment.ChainID,
		"app.kubernetes.io/name":     "consumer-chain",
		"app.kubernetes.io/instance": deployment.ChainID,
	}
	if deployment.ValidatorName != "" {
		labels["validator"] = deployment.ValidatorName
		labels["app.kubernetes.io/instance"] = fmt.Sprintf("%s-%s", deployment.ChainID, deployment.ValidatorName)
	}
	return labels
}

// getSelectors returns pod selectors for consumer chain resources
func (d *K8sDeployer) getSelectors(deployment ConsumerDeployment) map[string]string {
	selectors := map[string]string{
		"app":      "consumer-chain",
		"chain-id": deployment.ChainID,
	}
	if deployment.ValidatorName != "" {
		selectors["validator"] = deployment.ValidatorName
	}
	return selectors
}

// getLabelsWithComponent returns labels with a specific component
func (d *K8sDeployer) getLabelsWithComponent(deployment ConsumerDeployment, component string) map[string]string {
	labels := d.getLabels(deployment)
	labels["app.kubernetes.io/component"] = component
	return labels
}

// createConsumerConfigMap creates a ConfigMap with consumer chain configuration
func (d *K8sDeployer) createConsumerConfigMap(ctx context.Context, deployment ConsumerDeployment) error {
	configMapName := d.getResourceName(deployment.ChainID, deployment.ValidatorName, "config")

	// Consumer chain startup script
	startupScript := fmt.Sprintf(`#!/bin/bash
set -e

# Consumer chain startup script
echo "Starting consumer chain: %s"
echo "Consumer ID: %s"

# Set up directories
export CHAIN_HOME="/data/.%s"
mkdir -p "$CHAIN_HOME/config"

# Initialize consumer chain if not already initialized
if [ ! -f "$CHAIN_HOME/config/config.toml" ]; then
    echo "Initializing consumer chain..."
    /usr/local/bin/interchain-security-cd init consumer --chain-id %s --home "$CHAIN_HOME"

    # Add funded accounts for relayers and operators (required for consumer chains)
    echo "Adding genesis accounts..."
    # Create a relayer account with funds - using the same mnemonic as Hermes
    echo "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon art" | interchain-security-cd keys add relayer --recover --keyring-backend test --home "$CHAIN_HOME" 2>&1
    RELAYER_ADDR=$(interchain-security-cd keys show relayer -a --keyring-backend test --home "$CHAIN_HOME")
    echo "Relayer address: $RELAYER_ADDR"
    interchain-security-cd add-genesis-account $RELAYER_ADDR 100000000stake --home "$CHAIN_HOME"

    # Create an operator account with funds
    echo "operator depend gentle panther pear doll argue torch artist easily prison awkward shift" | interchain-security-cd keys add operator --recover --keyring-backend test --home "$CHAIN_HOME" 2>&1
    OPERATOR_ADDR=$(interchain-security-cd keys show operator -a --keyring-backend test --home "$CHAIN_HOME")
    echo "Operator address: $OPERATOR_ADDR"
    interchain-security-cd add-genesis-account $OPERATOR_ADDR 100000000stake --home "$CHAIN_HOME"
fi

# Check if CCV patch is available
if [ -f "/scripts/ccv-patch.json" ]; then
    echo "CCV patch found, applying to genesis..."

    # Create base genesis if it doesn't exist
    if [ ! -f "$CHAIN_HOME/config/genesis.json" ]; then
        echo "Creating base genesis file..."
        /usr/local/bin/interchain-security-cd init consumer --chain-id %s --home "$CHAIN_HOME" --overwrite
    fi

    # Always add funded accounts when we have CCV patch (consumer chain needs them)
    echo "Adding funded genesis accounts for consumer chain..."
    # Create a relayer account with funds - using the same mnemonic as Hermes
    echo "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon art" | interchain-security-cd keys add relayer --recover --keyring-backend test --home "$CHAIN_HOME" --output json 2>/dev/null || true
    RELAYER_ADDR=$(interchain-security-cd keys show relayer -a --keyring-backend test --home "$CHAIN_HOME" 2>/dev/null || echo "consumer1r5v5srda7xfth3hn2s26txvrcrntldju7725yc")
    echo "Relayer address: $RELAYER_ADDR"

    # Add genesis account only if not already added
    if ! grep -q "$RELAYER_ADDR" "$CHAIN_HOME/config/genesis.json"; then
        interchain-security-cd genesis add-genesis-account "$RELAYER_ADDR" 100000000stake --home "$CHAIN_HOME"
        echo "Added relayer account to genesis"
    fi

    # Debug: Show pre-CCV genesis
    echo "=== Pre-CCV Genesis ==="
    echo "app_state modules:"
    jq '.app_state | keys' "$CHAIN_HOME/config/genesis.json"
    echo "ccvconsumer module (if exists):"
    jq '.app_state.ccvconsumer' "$CHAIN_HOME/config/genesis.json" 2>/dev/null || echo "No ccvconsumer module yet"

    # Debug: Show CCV patch
    echo "=== CCV Patch ==="
    jq '.' "/scripts/ccv-patch.json"

    # Apply CCV patch to genesis
    echo "Merging CCV patch into genesis.json..."

    # Merge the CCV patch into genesis using Python (more reliable than jq for complex merges)
    if [ -f "/scripts/ccv-patch.json" ]; then
        python3 -c "
import json
import sys

# Read genesis
with open('$CHAIN_HOME/config/genesis.json', 'r') as f:
    genesis = json.load(f)

# Read CCV patch
with open('/scripts/ccv-patch.json', 'r') as f:
    patch = json.load(f)

# Initialize ccv_data
ccv_data = None

# The patch should have the structure: {app_state: {ccvconsumer: {...}}}
# We need to merge this into the genesis
if 'app_state' in patch:
    # Merge app_state from patch into genesis
    if 'app_state' not in genesis:
        genesis['app_state'] = {}

    # Update the ccvconsumer module specifically
    if 'ccvconsumer' in patch['app_state']:
        genesis['app_state']['ccvconsumer'] = patch['app_state']['ccvconsumer']
        ccv_data = patch['app_state']['ccvconsumer']
    else:
        print('Error: No ccvconsumer data in patch')
        sys.exit(1)
else:
    print('Error: Invalid patch format - missing app_state')
    sys.exit(1)

# Apply genesis_time if present in patch (for consistent genesis time across validators)
if 'genesis_time' in patch:
    genesis['genesis_time'] = patch['genesis_time']
    print(f'Updated genesis time to: {patch[\"genesis_time\"]}')

# Write back
with open('$CHAIN_HOME/config/genesis.json', 'w') as f:
    json.dump(genesis, f, indent=2)

# Report success
if ccv_data is not None:
    val_count = len(ccv_data.get('provider', {}).get('initial_val_set', []))
    print(f'CCV patch applied successfully with {val_count} validators')
else:
    print('CCV patch applied but no ccvconsumer data found')
"

        if [ $? -eq 0 ]; then
            # Debug: Show post-merge genesis
            echo "=== Post-CCV Genesis ==="
            echo "app_state modules after merge:"
            jq '.app_state | keys' "$CHAIN_HOME/config/genesis.json"
            echo "ccvconsumer module after merge:"
            jq '.app_state.ccvconsumer | keys' "$CHAIN_HOME/config/genesis.json"

            # Verify the merge worked
            if jq -e '.app_state.ccvconsumer.provider.initial_val_set | length > 0' "$CHAIN_HOME/config/genesis.json" > /dev/null; then
                VAL_COUNT=$(jq '.app_state.ccvconsumer.provider.initial_val_set | length' "$CHAIN_HOME/config/genesis.json")
                echo "Genesis has $VAL_COUNT validators in initial set"
                # Debug: Show first validator
                echo "Debug: First validator info:"
                jq '.app_state.ccvconsumer.provider.initial_val_set[0]' "$CHAIN_HOME/config/genesis.json"

                # Debug: Show genesis time
                echo "Debug: Genesis time:"
                jq '.genesis_time' "$CHAIN_HOME/config/genesis.json"

                # Debug: Show CCV consumer params
                echo "Debug: CCV consumer params:"
                jq '.app_state.ccvconsumer.params' "$CHAIN_HOME/config/genesis.json"

                # Debug: Check if validators have sufficient power
                echo "Debug: Validator powers:"
                jq '.app_state.ccvconsumer.provider.initial_val_set[].power' "$CHAIN_HOME/config/genesis.json"
            else
                echo "ERROR: No validators found in merged genesis"
                # Debug: Show genesis structure
                echo "Debug: Genesis app_state modules:"
                jq '.app_state | keys' "$CHAIN_HOME/config/genesis.json"
                echo "Debug: Full CCV consumer data:"
                jq '.app_state.ccvconsumer' "$CHAIN_HOME/config/genesis.json"
                exit 1
            fi
        else
            echo "Failed to apply CCV patch"
            exit 1
        fi
    fi
fi

# Configure consumer chain parameters
echo "Configuring consumer chain parameters..."
sed -i 's/timeout_commit = "5s"/timeout_commit = "2s"/' "$CHAIN_HOME/config/config.toml"
sed -i 's/timeout_propose = "3s"/timeout_propose = "1s"/' "$CHAIN_HOME/config/config.toml"

# Configure persistent peers if provided
if [ -n "$PERSISTENT_PEERS" ]; then
    echo "Configuring persistent peers: $PERSISTENT_PEERS"
    sed -i "s/persistent_peers = \"\"/persistent_peers = \"$PERSISTENT_PEERS\"/" "$CHAIN_HOME/config/config.toml"
fi

# Use consumer validator key if provided (for assigned consensus keys)
if [ -f /scripts/priv_validator_key.json ]; then
    echo "Using assigned consumer validator key"
    cp /scripts/priv_validator_key.json "$CHAIN_HOME/config/priv_validator_key.json"
    echo "Our validator address: $VALIDATOR_ADDR"
else
    echo "WARNING: No consumer validator key provided - chain will not be able to produce blocks!"
fi

# Use deterministic node key if provided
if [ -f /scripts/node_key.json ]; then
    echo "Using deterministic node key"
    cp /scripts/node_key.json "$CHAIN_HOME/config/node_key.json"
    NODE_ID=$(interchain-security-cd comet show-node-id --home "$CHAIN_HOME")
    echo "Our deterministic node ID: $NODE_ID"
else
    echo "No deterministic node key provided, using generated key"
    NODE_ID=$(interchain-security-cd comet show-node-id --home "$CHAIN_HOME")
    echo "Our generated node ID: $NODE_ID"
fi

# Configure ports to match the deterministic allocation
echo "Configuring network ports..."
# P2P port
sed -i "s/laddr = \"tcp:\/\/0.0.0.0:26656\"/laddr = \"tcp:\/\/0.0.0.0:%d\"/" "$CHAIN_HOME/config/config.toml"
# RPC port
sed -i "s/laddr = \"tcp:\/\/127.0.0.1:26657\"/laddr = \"tcp:\/\/0.0.0.0:%d\"/" "$CHAIN_HOME/config/config.toml"
# gRPC port
sed -i "s/address = \"localhost:9090\"/address = \"0.0.0.0:%d\"/" "$CHAIN_HOME/config/app.toml"
# REST/API port
sed -i "s/address = \"tcp:\/\/localhost:1317\"/address = \"tcp:\/\/0.0.0.0:%d\"/" "$CHAIN_HOME/config/app.toml"

# Final genesis validation before starting
echo "=== Final Genesis Validation ==="
echo "Chain ID: $(jq -r '.chain_id' "$CHAIN_HOME/config/genesis.json")"
echo "Genesis Time: $(jq -r '.genesis_time' "$CHAIN_HOME/config/genesis.json")"
echo "CCV Consumer Module Present: $(jq -e '.app_state.ccvconsumer' "$CHAIN_HOME/config/genesis.json" > /dev/null && echo 'YES' || echo 'NO')"
echo "Initial Validator Set Count: $(jq '.app_state.ccvconsumer.provider.initial_val_set | length' "$CHAIN_HOME/config/genesis.json" 2>/dev/null || echo '0')"

# Start consumer chain daemon with increased logging
echo "Starting interchain-security-cd for chain: %s"
echo "Command: interchain-security-cd start --home $CHAIN_HOME --log_level debug"
exec interchain-security-cd start --home "$CHAIN_HOME" --log_level debug
`, deployment.ChainID, deployment.ConsumerID,
		deployment.ChainID, deployment.ChainID, deployment.ChainID,
		deployment.P2PPort, deployment.RPCPort, deployment.GRPCPort, deployment.RESTPort,
		deployment.ChainID)

	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: deployment.Namespace,
			Labels:    d.getLabelsWithComponent(deployment, "config"),
		},
		Data: map[string]string{
			"start-consumer.sh": startupScript,
			"chain-id":          deployment.ChainID,
			"consumer-id":       deployment.ConsumerID,
		},
	}

	// Add CCV patch if provided
	if deployment.CCVPatch != nil {
		ccvPatchJSON, err := json.Marshal(deployment.CCVPatch)
		if err != nil {
			return fmt.Errorf("failed to marshal CCV patch: %w", err)
		}
		configMap.Data["ccv-patch.json"] = string(ccvPatchJSON)
		d.logger.Info("Adding CCV patch to ConfigMap",
			"config_map", configMapName,
			"patch_size", len(ccvPatchJSON))
	} else {
		d.logger.Warn("No CCV patch provided for ConfigMap",
			"config_map", configMapName)
	}

	// Add deterministic node key if provided
	if deployment.NodeKeyJSON != "" {
		configMap.Data["node_key.json"] = deployment.NodeKeyJSON
		d.logger.Info("Adding deterministic node key to ConfigMap",
			"config_map", configMapName)
	}

	// Add consumer validator key if provided (for assigned consensus keys)
	if deployment.ConsumerKeyJSON != "" {
		configMap.Data["priv_validator_key.json"] = deployment.ConsumerKeyJSON
		d.logger.Info("Adding consumer validator key to ConfigMap",
			"config_map", configMapName,
			"validator", deployment.ValidatorName)
	}

	_, err := d.clientset.CoreV1().ConfigMaps(deployment.Namespace).Create(ctx, configMap, metav1.CreateOptions{})
	if err != nil {
		if errors.IsAlreadyExists(err) {
			// Get existing ConfigMap to preserve any data that might already be there
			existingConfigMap, err := d.clientset.CoreV1().ConfigMaps(deployment.Namespace).Get(ctx, configMapName, metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("failed to get existing ConfigMap: %w", err)
			}

			// Update the data while preserving existing entries
			if existingConfigMap.Data == nil {
				existingConfigMap.Data = make(map[string]string)
			}
			for key, value := range configMap.Data {
				existingConfigMap.Data[key] = value
			}

			// Update the ConfigMap
			_, err = d.clientset.CoreV1().ConfigMaps(deployment.Namespace).Update(ctx, existingConfigMap, metav1.UpdateOptions{})
			if err != nil {
				return fmt.Errorf("failed to update existing ConfigMap: %w", err)
			}
			d.logger.Info("Updated existing ConfigMap for consumer chain",
				"config_map", configMapName, "namespace", deployment.Namespace)
		} else {
			return fmt.Errorf("failed to create ConfigMap: %w", err)
		}
	} else {
		d.logger.Info("Created ConfigMap for consumer chain",
			"config_map", configMapName, "namespace", deployment.Namespace)
	}

	return nil
}

// createConsumerService creates a Service for the consumer chain
func (d *K8sDeployer) createConsumerService(ctx context.Context, deployment ConsumerDeployment) error {
	serviceName := deployment.ChainID

	d.logger.Info("Creating consumer service with ClusterIP",
		"chain_id", deployment.ChainID,
		"namespace", deployment.Namespace)

	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName,
			Namespace: deployment.Namespace,
			Labels:    d.getLabels(deployment),
		},
		Spec: corev1.ServiceSpec{
			Selector: d.getSelectors(deployment),
			Ports: []corev1.ServicePort{
				{
					Name:       "p2p",
					Port:       deployment.P2PPort,
					TargetPort: intstr.FromInt(int(deployment.P2PPort)),
					Protocol:   corev1.ProtocolTCP,
				},
				{
					Name:       "rpc",
					Port:       deployment.RPCPort,
					TargetPort: intstr.FromInt(int(deployment.RPCPort)),
					Protocol:   corev1.ProtocolTCP,
				},
				{
					Name:       "rest",
					Port:       deployment.RESTPort,
					TargetPort: intstr.FromInt(int(deployment.RESTPort)),
					Protocol:   corev1.ProtocolTCP,
				},
				{
					Name:       "grpc",
					Port:       deployment.GRPCPort,
					TargetPort: intstr.FromInt(int(deployment.GRPCPort)),
					Protocol:   corev1.ProtocolTCP,
				},
			},
			Type: corev1.ServiceTypeClusterIP,
		},
	}

	_, err := d.clientset.CoreV1().Services(deployment.Namespace).Create(ctx, service, metav1.CreateOptions{})
	if err != nil {
		if errors.IsAlreadyExists(err) {
			// Service already exists, that's fine
			d.logger.Info("Service already exists for consumer chain",
				"service", serviceName, "namespace", deployment.Namespace)
		} else {
			return fmt.Errorf("failed to create Service: %w", err)
		}
	} else {
		d.logger.Info("Created Service for consumer chain",
			"service", serviceName, "namespace", deployment.Namespace)
	}

	return nil
}

// createConsumerDeployment creates the main consumer chain Deployment
func (d *K8sDeployer) createConsumerDeployment(ctx context.Context, deployment ConsumerDeployment) error {
	deploymentName := deployment.ChainID
	configMapName := d.getResourceName(deployment.ChainID, deployment.ValidatorName, "config")

	k8sDeployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName,
			Namespace: deployment.Namespace,
			Labels:    d.getLabelsWithComponent(deployment, "daemon"),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &deployment.Replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: d.getSelectors(deployment),
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: d.getSelectors(deployment),
				},
				Spec: corev1.PodSpec{
					TerminationGracePeriodSeconds: int64Ptr(5),
					Containers: []corev1.Container{
						{
							Name:            "consumer-daemon",
							Image:           deployment.Image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Command:         []string{"/bin/bash", "/scripts/start-consumer.sh"},
							Env: []corev1.EnvVar{
								{
									Name:  "CHAIN_ID",
									Value: deployment.ChainID,
								},
								{
									Name:  "CONSUMER_ID",
									Value: deployment.ConsumerID,
								},
								{
									Name:  "NAMESPACE",
									Value: deployment.Namespace,
								},
								{
									Name:  "PROVIDER_NAMESPACE",
									Value: "provider",
								},
								{
									Name:  "PERSISTENT_PEERS",
									Value: deployment.PersistentPeers,
								},
							},
							Ports: []corev1.ContainerPort{
								{
									Name:          "p2p",
									ContainerPort: deployment.P2PPort,
									Protocol:      corev1.ProtocolTCP,
								},
								{
									Name:          "rpc",
									ContainerPort: deployment.RPCPort,
									Protocol:      corev1.ProtocolTCP,
								},
								{
									Name:          "rest",
									ContainerPort: deployment.RESTPort,
									Protocol:      corev1.ProtocolTCP,
								},
								{
									Name:          "grpc",
									ContainerPort: deployment.GRPCPort,
									Protocol:      corev1.ProtocolTCP,
								},
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceMemory: parseQuantity("512Mi"),
									corev1.ResourceCPU:    parseQuantity("250m"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceMemory: parseQuantity("1Gi"),
									corev1.ResourceCPU:    parseQuantity("500m"),
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "consumer-data",
									MountPath: "/data",
								},
								{
									Name:      "consumer-config",
									MountPath: "/scripts",
								},
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/status",
										Port: intstr.FromInt(int(deployment.RPCPort)),
									},
								},
								InitialDelaySeconds: 30,
								PeriodSeconds:       10,
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/status",
										Port: intstr.FromInt(int(deployment.RPCPort)),
									},
								},
								InitialDelaySeconds: 10,
								PeriodSeconds:       5,
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "consumer-data",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{
									// SizeLimit: parseQuantity("2Gi"), // Optional size limit
								},
							},
						},
						{
							Name: "consumer-config",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: configMapName,
									},
									DefaultMode: int32Ptr(0755),
								},
							},
						},
					},
				},
			},
		},
	}

	_, err := d.clientset.AppsV1().Deployments(deployment.Namespace).Create(ctx, k8sDeployment, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create Deployment: %w", err)
	}

	d.logger.Info("Created Deployment for consumer chain",
		"deployment", deploymentName, "namespace", deployment.Namespace)

	// Create NodePort service for consumer chain to enable cross-cluster P2P connectivity
	if err := d.createConsumerService(ctx, deployment); err != nil {
		return fmt.Errorf("failed to create service: %w", err)
	}

	return nil
}

// GetConsumerChainStatus returns the status of a consumer chain deployment
func (d *K8sDeployer) GetConsumerChainStatus(ctx context.Context, chainID, namespace string) (*ConsumerChainStatus, error) {
	deployment, err := d.clientset.AppsV1().Deployments(namespace).Get(ctx, chainID, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get deployment: %w", err)
	}

	pods, err := d.clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("app=consumer-chain,chain-id=%s", chainID),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list pods: %w", err)
	}

	status := &ConsumerChainStatus{
		ChainID:           chainID,
		Namespace:         namespace,
		DesiredReplicas:   *deployment.Spec.Replicas,
		ReadyReplicas:     deployment.Status.ReadyReplicas,
		AvailableReplicas: deployment.Status.AvailableReplicas,
		UpdatedReplicas:   deployment.Status.UpdatedReplicas,
		Pods:              make([]PodStatus, len(pods.Items)),
	}

	for i, pod := range pods.Items {
		status.Pods[i] = PodStatus{
			Name:   pod.Name,
			Phase:  string(pod.Status.Phase),
			Ready:  isPodReady(pod),
			NodeIP: pod.Status.HostIP,
			PodIP:  pod.Status.PodIP,
		}
	}

	return status, nil
}

// ConsumerChainStatus represents the status of a consumer chain deployment
type ConsumerChainStatus struct {
	ChainID           string      `json:"chain_id"`
	Namespace         string      `json:"namespace"`
	DesiredReplicas   int32       `json:"desired_replicas"`
	ReadyReplicas     int32       `json:"ready_replicas"`
	AvailableReplicas int32       `json:"available_replicas"`
	UpdatedReplicas   int32       `json:"updated_replicas"`
	Pods              []PodStatus `json:"pods"`
}

// PodStatus represents the status of a pod
type PodStatus struct {
	Name   string `json:"name"`
	Phase  string `json:"phase"`
	Ready  bool   `json:"ready"`
	NodeIP string `json:"node_ip"`
	PodIP  string `json:"pod_ip"`
}

// RestartDeployment restarts a deployment by updating its annotations
func (d *K8sDeployer) RestartDeployment(ctx context.Context, deploymentName, namespace string) error {
	deployment, err := d.clientset.AppsV1().Deployments(namespace).Get(ctx, deploymentName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get deployment: %w", err)
	}

	// Add or update restart annotation to trigger rolling update
	if deployment.Spec.Template.ObjectMeta.Annotations == nil {
		deployment.Spec.Template.ObjectMeta.Annotations = make(map[string]string)
	}
	deployment.Spec.Template.ObjectMeta.Annotations["kubectl.kubernetes.io/restartedAt"] = time.Now().Format(time.RFC3339)

	_, err = d.clientset.AppsV1().Deployments(namespace).Update(ctx, deployment, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update deployment: %w", err)
	}

	d.logger.Info("Triggered deployment restart", "deployment", deploymentName, "namespace", namespace)
	return nil
}

// DeploymentExists checks if a deployment exists for the given chain ID
func (d *K8sDeployer) DeploymentExists(ctx context.Context, chainID string) (bool, error) {
	// For backward compatibility, if namespace is empty, return false
	if d.namespace == "" {
		return false, nil
	}

	deploymentName := chainID // No suffix, matching createConsumerDeployment

	_, err := d.clientset.AppsV1().Deployments(d.namespace).Get(ctx, deploymentName, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to check deployment existence: %w", err)
	}

	return true, nil
}

// DeploymentExistsInNamespace checks if a deployment exists for the given chain ID in a specific namespace
func (d *K8sDeployer) DeploymentExistsInNamespace(ctx context.Context, chainID, namespace string) (bool, error) {
	deploymentName := chainID

	_, err := d.clientset.AppsV1().Deployments(namespace).Get(ctx, deploymentName, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to check deployment existence: %w", err)
	}

	return true, nil
}

// Helper functions
func parseQuantity(s string) resource.Quantity {
	quantity, _ := resource.ParseQuantity(s)
	return quantity
}

func int32Ptr(i int32) *int32 {
	return &i
}

func int64Ptr(i int64) *int64 {
	return &i
}

func isPodReady(pod corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}
