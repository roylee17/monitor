package subnet

import (
	"context"
	"fmt"
	"log/slog"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/client-go/kubernetes"
)

const (
	// LoadBalancerName is the name of the shared LoadBalancer service
	LoadBalancerName = "p2p-loadbalancer"
	
	// LoadBalancerNamespace is the namespace where the LoadBalancer exists
	LoadBalancerNamespace = "provider"
)

// LoadBalancerManager manages the shared LoadBalancer service for consumer chains
type LoadBalancerManager struct {
	clientset kubernetes.Interface
	logger    *slog.Logger
}

// NewLoadBalancerManager creates a new LoadBalancer manager
func NewLoadBalancerManager(clientset kubernetes.Interface, logger *slog.Logger) *LoadBalancerManager {
	return &LoadBalancerManager{
		clientset: clientset,
		logger:    logger,
	}
}

// EnsureLoadBalancerReady checks if the LoadBalancer exists and has an external endpoint
func (m *LoadBalancerManager) EnsureLoadBalancerReady(ctx context.Context) (string, error) {
	svc, err := m.clientset.CoreV1().Services(LoadBalancerNamespace).Get(ctx, LoadBalancerName, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return "", fmt.Errorf("LoadBalancer service %s/%s not found - ensure Helm chart is deployed correctly", LoadBalancerNamespace, LoadBalancerName)
		}
		return "", fmt.Errorf("failed to get LoadBalancer service: %w", err)
	}

	if svc.Spec.Type != corev1.ServiceTypeLoadBalancer {
		return "", fmt.Errorf("service %s/%s is not of type LoadBalancer (found: %s)", LoadBalancerNamespace, LoadBalancerName, svc.Spec.Type)
	}

	// Wait for LoadBalancer to get external IP/hostname
	if len(svc.Status.LoadBalancer.Ingress) == 0 {
		return "", fmt.Errorf("LoadBalancer %s/%s has no external IP/hostname yet", LoadBalancerNamespace, LoadBalancerName)
	}

	// Return the first ingress point (IP or hostname)
	ingress := svc.Status.LoadBalancer.Ingress[0]
	if ingress.IP != "" {
		m.logger.Info("LoadBalancer ready with IP", "ip", ingress.IP)
		return ingress.IP, nil
	}
	if ingress.Hostname != "" {
		m.logger.Info("LoadBalancer ready with hostname", "hostname", ingress.Hostname)
		return ingress.Hostname, nil
	}

	return "", fmt.Errorf("LoadBalancer has ingress but no IP or hostname")
}

// AddConsumerPort adds a port to the LoadBalancer for a consumer chain
func (m *LoadBalancerManager) AddConsumerPort(ctx context.Context, chainID string, port int32) error {
	m.logger.Info("Adding port to LoadBalancer", 
		"chain_id", chainID,
		"port", port)

	// Create patch to add the port
	patch := []map[string]interface{}{
		{
			"op":   "add",
			"path": fmt.Sprintf("/spec/ports/-"),
			"value": map[string]interface{}{
				"name":       fmt.Sprintf("consumer-%s", chainID),
				"port":       port,
				"targetPort": port,
				"protocol":   "TCP",
			},
		},
	}

	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("failed to marshal patch: %w", err)
	}

	// Apply the patch
	_, err = m.clientset.CoreV1().Services(LoadBalancerNamespace).Patch(
		ctx, 
		LoadBalancerName, 
		types.JSONPatchType, 
		patchBytes, 
		metav1.PatchOptions{},
	)
	if err != nil {
		// If port already exists, that's OK
		if !errors.IsInvalid(err) {
			return fmt.Errorf("failed to patch LoadBalancer service: %w", err)
		}
		m.logger.Info("Port may already exist, checking...")
		
		// Check if port already exists
		svc, getErr := m.clientset.CoreV1().Services(LoadBalancerNamespace).Get(ctx, LoadBalancerName, metav1.GetOptions{})
		if getErr != nil {
			return fmt.Errorf("failed to get LoadBalancer service: %w", getErr)
		}
		
		for _, p := range svc.Spec.Ports {
			if p.Port == port {
				m.logger.Info("Port already exists in LoadBalancer", "port", port)
				return nil
			}
		}
		
		return fmt.Errorf("failed to add port and port doesn't exist: %w", err)
	}

	m.logger.Info("Successfully added port to LoadBalancer", 
		"chain_id", chainID,
		"port", port)
	return nil
}

// RemoveConsumerPort removes a port from the LoadBalancer when a consumer chain is deleted
func (m *LoadBalancerManager) RemoveConsumerPort(ctx context.Context, chainID string, port int32) error {
	m.logger.Info("Removing port from LoadBalancer", 
		"chain_id", chainID,
		"port", port)

	// Get current service to find the port index
	svc, err := m.clientset.CoreV1().Services(LoadBalancerNamespace).Get(ctx, LoadBalancerName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get LoadBalancer service: %w", err)
	}

	// Find port index
	portIndex := -1
	for i, p := range svc.Spec.Ports {
		if p.Port == port {
			portIndex = i
			break
		}
	}

	if portIndex == -1 {
		m.logger.Info("Port not found in LoadBalancer, nothing to remove", "port", port)
		return nil
	}

	// Create patch to remove the port
	patch := []map[string]interface{}{
		{
			"op":   "remove",
			"path": fmt.Sprintf("/spec/ports/%d", portIndex),
		},
	}

	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("failed to marshal patch: %w", err)
	}

	// Apply the patch
	_, err = m.clientset.CoreV1().Services(LoadBalancerNamespace).Patch(
		ctx, 
		LoadBalancerName, 
		types.JSONPatchType, 
		patchBytes, 
		metav1.PatchOptions{},
	)
	if err != nil {
		return fmt.Errorf("failed to patch LoadBalancer service: %w", err)
	}

	m.logger.Info("Successfully removed port from LoadBalancer", 
		"chain_id", chainID,
		"port", port)
	return nil
}

// CreateEndpointSlice creates an EndpointSlice to route traffic from LoadBalancer to consumer pod
func (m *LoadBalancerManager) CreateEndpointSlice(ctx context.Context, chainID string, namespace string, podIP string, port int32) error {
	m.logger.Info("Creating EndpointSlice for consumer chain",
		"chain_id", chainID,
		"namespace", namespace,
		"pod_ip", podIP,
		"port", port)

	// Create EndpointSlice
	portName := fmt.Sprintf("consumer-%s", chainID)
	endpointSlice := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("consumer-%s", chainID),
			Namespace: LoadBalancerNamespace,
			Labels: map[string]string{
				"kubernetes.io/service-name": LoadBalancerName,
				"consumer-chain-id":          chainID,
			},
		},
		AddressType: discoveryv1.AddressTypeIPv4,
		Endpoints: []discoveryv1.Endpoint{
			{
				Addresses: []string{podIP},
				Conditions: discoveryv1.EndpointConditions{
					Ready:       &[]bool{true}[0],
					Serving:     &[]bool{true}[0],
					Terminating: &[]bool{false}[0],
				},
				TargetRef: &corev1.ObjectReference{
					Kind:      "Pod",
					Name:      chainID,
					Namespace: namespace,
				},
			},
		},
		Ports: []discoveryv1.EndpointPort{
			{
				Name:     &portName,
				Port:     &port, // LoadBalancer port that maps to consumer's P2P port
				Protocol: &[]corev1.Protocol{corev1.ProtocolTCP}[0],
			},
		},
	}

	// Create the EndpointSlice
	_, err := m.clientset.DiscoveryV1().EndpointSlices(LoadBalancerNamespace).Create(ctx, endpointSlice, metav1.CreateOptions{})
	if err != nil {
		if errors.IsAlreadyExists(err) {
			// Update existing EndpointSlice
			_, updateErr := m.clientset.DiscoveryV1().EndpointSlices(LoadBalancerNamespace).Update(ctx, endpointSlice, metav1.UpdateOptions{})
			if updateErr != nil {
				return fmt.Errorf("failed to update existing EndpointSlice: %w", updateErr)
			}
			m.logger.Info("Updated existing EndpointSlice", "chain_id", chainID)
			return nil
		}
		return fmt.Errorf("failed to create EndpointSlice: %w", err)
	}

	m.logger.Info("Successfully created EndpointSlice", "chain_id", chainID)
	return nil
}

// DeleteEndpointSlice removes the EndpointSlice when a consumer chain is deleted
func (m *LoadBalancerManager) DeleteEndpointSlice(ctx context.Context, chainID string) error {
	m.logger.Info("Deleting EndpointSlice for consumer chain", "chain_id", chainID)

	err := m.clientset.DiscoveryV1().EndpointSlices(LoadBalancerNamespace).Delete(
		ctx, 
		fmt.Sprintf("consumer-%s", chainID), 
		metav1.DeleteOptions{},
	)
	if err != nil {
		if errors.IsNotFound(err) {
			m.logger.Info("EndpointSlice not found, nothing to delete", "chain_id", chainID)
			return nil
		}
		return fmt.Errorf("failed to delete EndpointSlice: %w", err)
	}

	m.logger.Info("Successfully deleted EndpointSlice", "chain_id", chainID)
	return nil
}

// UpdateEndpointSlice updates the EndpointSlice when consumer pod IP changes
func (m *LoadBalancerManager) UpdateEndpointSlice(ctx context.Context, chainID string, namespace string, podIP string) error {
	m.logger.Info("Updating EndpointSlice for consumer chain",
		"chain_id", chainID,
		"pod_ip", podIP)

	// Get existing EndpointSlice
	endpointSlice, err := m.clientset.DiscoveryV1().EndpointSlices(LoadBalancerNamespace).Get(
		ctx, 
		fmt.Sprintf("consumer-%s", chainID), 
		metav1.GetOptions{},
	)
	if err != nil {
		return fmt.Errorf("failed to get EndpointSlice: %w", err)
	}

	// Update the pod IP
	if len(endpointSlice.Endpoints) > 0 {
		endpointSlice.Endpoints[0].Addresses = []string{podIP}
		endpointSlice.Endpoints[0].TargetRef = &corev1.ObjectReference{
			Kind:      "Pod",
			Name:      chainID,
			Namespace: namespace,
		}
	} else {
		// Create new endpoint if none exists
		endpointSlice.Endpoints = []discoveryv1.Endpoint{
			{
				Addresses: []string{podIP},
				Conditions: discoveryv1.EndpointConditions{
					Ready:       &[]bool{true}[0],
					Serving:     &[]bool{true}[0],
					Terminating: &[]bool{false}[0],
				},
				TargetRef: &corev1.ObjectReference{
					Kind:      "Pod",
					Name:      chainID,
					Namespace: namespace,
				},
			},
		}
	}

	// Update the EndpointSlice
	_, err = m.clientset.DiscoveryV1().EndpointSlices(LoadBalancerNamespace).Update(ctx, endpointSlice, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update EndpointSlice: %w", err)
	}

	m.logger.Info("Successfully updated EndpointSlice", "chain_id", chainID)
	return nil
}