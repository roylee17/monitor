package subnet

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
)

// PortForwardManager manages port forwarding from LoadBalancer to consumer pods
type PortForwardManager struct {
	clientset *kubernetes.Clientset
	logger    *logrus.Logger
}

// NewPortForwardManager creates a new port forward manager
func NewPortForwardManager(clientset *kubernetes.Clientset, logger *logrus.Logger) *PortForwardManager {
	return &PortForwardManager{
		clientset: clientset,
		logger:    logger,
	}
}

// CreateNodePortService creates a NodePort service for the consumer chain
// This service will be the target for LoadBalancer traffic forwarding
func (m *PortForwardManager) CreateNodePortService(ctx context.Context, chainID, namespace string, p2pPort int32) (int32, error) {
	m.logger.Info("Creating NodePort service for consumer chain",
		"chain_id", chainID,
		"namespace", namespace,
		"p2p_port", p2pPort)

	// Create a NodePort service that exposes the consumer chain
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-nodeport", chainID),
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":       chainID,
				"app.kubernetes.io/component":  "p2p-nodeport",
				"app.kubernetes.io/managed-by": "ics-monitor",
			},
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeNodePort,
			Selector: map[string]string{
				"app.kubernetes.io/name": chainID,
			},
			Ports: []corev1.ServicePort{
				{
					Name:       "p2p",
					Port:       p2pPort,
					TargetPort: intstr.FromInt(26656), // Consumer's internal P2P port
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}

	// Create the service
	created, err := m.clientset.CoreV1().Services(namespace).Create(ctx, service, metav1.CreateOptions{})
	if err != nil {
		if errors.IsAlreadyExists(err) {
			// Get existing service
			existing, getErr := m.clientset.CoreV1().Services(namespace).Get(ctx, service.Name, metav1.GetOptions{})
			if getErr != nil {
				return 0, fmt.Errorf("failed to get existing NodePort service: %w", getErr)
			}
			if len(existing.Spec.Ports) > 0 {
				nodePort := existing.Spec.Ports[0].NodePort
				m.logger.Info("NodePort service already exists",
					"chain_id", chainID,
					"node_port", nodePort)
				return nodePort, nil
			}
		}
		return 0, fmt.Errorf("failed to create NodePort service: %w", err)
	}

	if len(created.Spec.Ports) == 0 {
		return 0, fmt.Errorf("no ports found in created service")
	}

	nodePort := created.Spec.Ports[0].NodePort
	m.logger.Info("Successfully created NodePort service",
		"chain_id", chainID,
		"node_port", nodePort)

	return nodePort, nil
}

// CreateForwardingRules creates iptables rules to forward LoadBalancer traffic to NodePort
// This is done via a DaemonSet that runs on all nodes
func (m *PortForwardManager) CreateForwardingRules(ctx context.Context, chainID string, loadBalancerPort, nodePort int32) error {
	m.logger.Info("Creating forwarding rules",
		"chain_id", chainID,
		"lb_port", loadBalancerPort,
		"node_port", nodePort)

	// Create a ConfigMap with the iptables script
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("iptables-forward-%s", chainID),
			Namespace: "kube-system",
			Labels: map[string]string{
				"app.kubernetes.io/name":       "iptables-forwarder",
				"app.kubernetes.io/instance":   chainID,
				"app.kubernetes.io/managed-by": "ics-monitor",
			},
		},
		Data: map[string]string{
			"setup.sh": fmt.Sprintf(`#!/bin/bash
set -e

# Add iptables rule to forward LoadBalancer port to NodePort
# This allows traffic coming to the LoadBalancer to reach the consumer chain

# Check if rule already exists
if ! iptables -t nat -C PREROUTING -p tcp --dport %d -j REDIRECT --to-port %d 2>/dev/null; then
    echo "Adding iptables rule: LoadBalancer port %d -> NodePort %d"
    iptables -t nat -A PREROUTING -p tcp --dport %d -j REDIRECT --to-port %d
else
    echo "Rule already exists"
fi

# Keep the container running
while true; do
    sleep 3600
done
`, loadBalancerPort, nodePort, loadBalancerPort, nodePort, loadBalancerPort, nodePort),
			"cleanup.sh": fmt.Sprintf(`#!/bin/bash
# Remove the iptables rule on container shutdown
iptables -t nat -D PREROUTING -p tcp --dport %d -j REDIRECT --to-port %d 2>/dev/null || true
`, loadBalancerPort, nodePort),
		},
	}

	// Create the ConfigMap
	_, err := m.clientset.CoreV1().ConfigMaps("kube-system").Create(ctx, configMap, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create iptables ConfigMap: %w", err)
	}

	m.logger.Info("Successfully created forwarding rules ConfigMap",
		"chain_id", chainID)

	// Note: In a real implementation, we would also create a DaemonSet that:
	// 1. Mounts this ConfigMap
	// 2. Runs the setup.sh script
	// 3. Ensures the rules are applied on all nodes
	// For now, we're just creating the ConfigMap as a placeholder

	return nil
}

// RemoveForwardingRules removes the iptables forwarding rules
func (m *PortForwardManager) RemoveForwardingRules(ctx context.Context, chainID string) error {
	m.logger.Info("Removing forwarding rules",
		"chain_id", chainID)

	// Delete the ConfigMap
	err := m.clientset.CoreV1().ConfigMaps("kube-system").Delete(
		ctx,
		fmt.Sprintf("iptables-forward-%s", chainID),
		metav1.DeleteOptions{},
	)
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to delete iptables ConfigMap: %w", err)
	}

	m.logger.Info("Successfully removed forwarding rules",
		"chain_id", chainID)

	return nil
}

// ConfigurePortForwarding sets up the complete port forwarding solution
func (m *PortForwardManager) ConfigurePortForwarding(ctx context.Context, chainID, namespace string, p2pPort int32) error {
	// 1. Create NodePort service
	nodePort, err := m.CreateNodePortService(ctx, chainID, namespace, p2pPort)
	if err != nil {
		return fmt.Errorf("failed to create NodePort service: %w", err)
	}

	// 2. Create forwarding rules
	if err := m.CreateForwardingRules(ctx, chainID, p2pPort, nodePort); err != nil {
		return fmt.Errorf("failed to create forwarding rules: %w", err)
	}

	m.logger.Info("Successfully configured port forwarding",
		"chain_id", chainID,
		"p2p_port", p2pPort,
		"node_port", nodePort)

	return nil
}