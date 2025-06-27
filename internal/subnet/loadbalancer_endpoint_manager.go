package subnet

import (
	"context"
	"fmt"
	"log/slog"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// PodEndpointInfo contains information about a pod for endpoint configuration
type PodEndpointInfo struct {
	PodName   string
	PodIP     string
	Namespace string
}

// LoadBalancerEndpointManager manages endpoints for the LoadBalancer service
type LoadBalancerEndpointManager struct {
	clientset *kubernetes.Clientset
	logger    *slog.Logger
}

// NewLoadBalancerEndpointManager creates a new endpoint manager
func NewLoadBalancerEndpointManager(clientset *kubernetes.Clientset, logger *slog.Logger) *LoadBalancerEndpointManager {
	return &LoadBalancerEndpointManager{
		clientset: clientset,
		logger:    logger,
	}
}

// UpdateEndpoints manually updates the endpoints for the LoadBalancer service
// This is used when the LoadBalancer has no selector
func (m *LoadBalancerEndpointManager) UpdateEndpoints(ctx context.Context, chainID, namespace, podIP string, port int32) error {
	m.logger.Info("Updating LoadBalancer endpoints",
		"chain_id", chainID,
		"pod_ip", podIP,
		"port", port)
	
	// The consumer pod's internal P2P port is always 26656
	const internalP2PPort = 26656

	// Get existing endpoints
	endpointsName := LoadBalancerName
	endpoints, err := m.clientset.CoreV1().Endpoints(LoadBalancerNamespace).Get(ctx, endpointsName, metav1.GetOptions{})
	createNew := false
	if err != nil {
		m.logger.Info("Endpoints don't exist, will create new ones",
			"endpoint_name", endpointsName,
			"namespace", LoadBalancerNamespace,
			"error", err)
		// Create new endpoints if they don't exist
		createNew = true
		endpoints = &corev1.Endpoints{
			ObjectMeta: metav1.ObjectMeta{
				Name:      endpointsName,
				Namespace: LoadBalancerNamespace,
			},
			Subsets: []corev1.EndpointSubset{},
		}
	}

	// Find or create subset for this port
	portName := fmt.Sprintf("consumer-%s", chainID)
	subsetIndex := -1
	for i, subset := range endpoints.Subsets {
		for _, p := range subset.Ports {
			if p.Port == port && p.Name == portName {
				m.logger.Info("Found existing subset for port",
					"port_name", portName,
					"port", port,
					"subset_index", i)
				subsetIndex = i
				break
			}
		}
		if subsetIndex != -1 {
			break
		}
	}
	
	if subsetIndex == -1 {
		m.logger.Info("No existing subset found for port, will create new one",
			"port_name", portName,
			"port", port)
	}

	// Create endpoint address for the consumer pod
	address := corev1.EndpointAddress{
		IP: podIP,
		TargetRef: &corev1.ObjectReference{
			Kind:      "Pod",
			Name:      chainID,
			Namespace: namespace,
		},
	}

	if subsetIndex == -1 {
		// Add new subset
		subset := corev1.EndpointSubset{
			Addresses: []corev1.EndpointAddress{address},
			Ports: []corev1.EndpointPort{
				{
					Name:     portName,
					Port:     port,
					Protocol: corev1.ProtocolTCP,
				},
			},
		}
		endpoints.Subsets = append(endpoints.Subsets, subset)
		m.logger.Info("Added new subset to endpoints",
			"port_name", portName,
			"port", port,
			"pod_ip", podIP,
			"total_subsets", len(endpoints.Subsets))
	} else {
		// Update existing subset - add address if not already present
		found := false
		for _, addr := range endpoints.Subsets[subsetIndex].Addresses {
			if addr.IP == podIP {
				found = true
				m.logger.Info("Address already exists in subset",
					"pod_ip", podIP,
					"subset_index", subsetIndex)
				break
			}
		}
		if !found {
			endpoints.Subsets[subsetIndex].Addresses = append(
				endpoints.Subsets[subsetIndex].Addresses,
				address,
			)
			m.logger.Info("Added address to existing subset",
				"pod_ip", podIP,
				"subset_index", subsetIndex,
				"total_addresses", len(endpoints.Subsets[subsetIndex].Addresses))
		}
	}

	// Create or update the endpoints
	if createNew {
		// Create new endpoints
		_, createErr := m.clientset.CoreV1().Endpoints(LoadBalancerNamespace).Create(ctx, endpoints, metav1.CreateOptions{})
		if createErr != nil {
			return fmt.Errorf("failed to create endpoints: %w", createErr)
		}
		m.logger.Info("Created new endpoints for LoadBalancer", "chain_id", chainID)
	} else {
		// Update existing endpoints
		_, updateErr := m.clientset.CoreV1().Endpoints(LoadBalancerNamespace).Update(ctx, endpoints, metav1.UpdateOptions{})
		if updateErr != nil {
			return fmt.Errorf("failed to update endpoints: %w", updateErr)
		}
		m.logger.Info("Updated endpoints for LoadBalancer", "chain_id", chainID)
	}

	return nil
}

// UpdateEndpointsForAllPods updates endpoints for all pods of a consumer chain across all validators
// This is useful for syncing endpoints when a monitor restarts or discovers an already-deployed consumer
func (m *LoadBalancerEndpointManager) UpdateEndpointsForAllPods(ctx context.Context, chainID string, port int32, podInfos []PodEndpointInfo) error {
	m.logger.Info("Updating LoadBalancer endpoints for all pods",
		"chain_id", chainID,
		"port", port,
		"pod_count", len(podInfos))
	
	if len(podInfos) == 0 {
		m.logger.Warn("No pod information provided, skipping endpoint update",
			"chain_id", chainID)
		return nil
	}

	// Get existing endpoints
	endpointsName := LoadBalancerName
	endpoints, err := m.clientset.CoreV1().Endpoints(LoadBalancerNamespace).Get(ctx, endpointsName, metav1.GetOptions{})
	createNew := false
	if err != nil {
		m.logger.Info("Endpoints don't exist, will create new ones",
			"endpoint_name", endpointsName,
			"namespace", LoadBalancerNamespace,
			"error", err)
		// Create new endpoints if they don't exist
		createNew = true
		endpoints = &corev1.Endpoints{
			ObjectMeta: metav1.ObjectMeta{
				Name:      endpointsName,
				Namespace: LoadBalancerNamespace,
			},
			Subsets: []corev1.EndpointSubset{},
		}
	}

	// Find or create subset for this port
	portName := fmt.Sprintf("consumer-%s", chainID)
	subsetIndex := -1
	for i, subset := range endpoints.Subsets {
		for _, p := range subset.Ports {
			if p.Port == port && p.Name == portName {
				m.logger.Info("Found existing subset for port",
					"port_name", portName,
					"port", port,
					"subset_index", i)
				subsetIndex = i
				break
			}
		}
		if subsetIndex != -1 {
			break
		}
	}
	
	// Build the list of addresses
	addresses := []corev1.EndpointAddress{}
	for _, podInfo := range podInfos {
		address := corev1.EndpointAddress{
			IP: podInfo.PodIP,
			TargetRef: &corev1.ObjectReference{
				Kind:      "Pod",
				Name:      podInfo.PodName,
				Namespace: podInfo.Namespace,
			},
		}
		addresses = append(addresses, address)
		m.logger.Debug("Added pod to endpoint addresses",
			"pod_name", podInfo.PodName,
			"pod_ip", podInfo.PodIP,
			"namespace", podInfo.Namespace)
	}

	if subsetIndex == -1 {
		// Add new subset with all addresses
		subset := corev1.EndpointSubset{
			Addresses: addresses,
			Ports: []corev1.EndpointPort{
				{
					Name:     portName,
					Port:     port,
					Protocol: corev1.ProtocolTCP,
				},
			},
		}
		endpoints.Subsets = append(endpoints.Subsets, subset)
		m.logger.Info("Added new subset to endpoints with all addresses",
			"port_name", portName,
			"port", port,
			"address_count", len(addresses),
			"total_subsets", len(endpoints.Subsets))
	} else {
		// Replace addresses in existing subset with the complete list
		endpoints.Subsets[subsetIndex].Addresses = addresses
		m.logger.Info("Replaced addresses in existing subset",
			"subset_index", subsetIndex,
			"address_count", len(addresses))
	}

	// Create or update the endpoints
	if createNew {
		// Create new endpoints
		_, createErr := m.clientset.CoreV1().Endpoints(LoadBalancerNamespace).Create(ctx, endpoints, metav1.CreateOptions{})
		if createErr != nil {
			return fmt.Errorf("failed to create endpoints: %w", createErr)
		}
		m.logger.Info("Created new endpoints for LoadBalancer with all pods", 
			"chain_id", chainID,
			"pod_count", len(podInfos))
	} else {
		// Update existing endpoints
		_, updateErr := m.clientset.CoreV1().Endpoints(LoadBalancerNamespace).Update(ctx, endpoints, metav1.UpdateOptions{})
		if updateErr != nil {
			return fmt.Errorf("failed to update endpoints: %w", updateErr)
		}
		m.logger.Info("Updated endpoints for LoadBalancer with all pods", 
			"chain_id", chainID,
			"pod_count", len(podInfos))
	}

	return nil
}

// RemoveEndpoints removes endpoints for a consumer chain
func (m *LoadBalancerEndpointManager) RemoveEndpoints(ctx context.Context, chainID string, port int32) error {
	m.logger.Info("Removing LoadBalancer endpoints",
		"chain_id", chainID,
		"port", port)

	// Get existing endpoints
	endpointsName := LoadBalancerName
	endpoints, err := m.clientset.CoreV1().Endpoints(LoadBalancerNamespace).Get(ctx, endpointsName, metav1.GetOptions{})
	if err != nil {
		m.logger.Warn("Endpoints not found, nothing to remove", "error", err)
		return nil
	}

	// Find and remove subset for this port
	portName := fmt.Sprintf("consumer-%s", chainID)
	newSubsets := []corev1.EndpointSubset{}
	
	for _, subset := range endpoints.Subsets {
		keep := true
		for _, p := range subset.Ports {
			if p.Port == port && p.Name == portName {
				keep = false
				break
			}
		}
		if keep {
			newSubsets = append(newSubsets, subset)
		}
	}

	// Update endpoints
	endpoints.Subsets = newSubsets
	_, updateErr := m.clientset.CoreV1().Endpoints(LoadBalancerNamespace).Update(ctx, endpoints, metav1.UpdateOptions{})
	if updateErr != nil {
		return fmt.Errorf("failed to update endpoints: %w", updateErr)
	}

	m.logger.Info("Successfully removed endpoints for consumer chain",
		"chain_id", chainID)

	return nil
}