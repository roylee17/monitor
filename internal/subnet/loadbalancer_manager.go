package subnet

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/apimachinery/pkg/watch"
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

	// Pod watching
	watchers      map[string]context.CancelFunc
	watchersMutex sync.Mutex
}

// NewLoadBalancerManager creates a new LoadBalancer manager
func NewLoadBalancerManager(clientset kubernetes.Interface, logger *slog.Logger) *LoadBalancerManager {
	return &LoadBalancerManager{
		clientset: clientset,
		logger:    logger,
		watchers:  make(map[string]context.CancelFunc),
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

// StartPodWatcher starts watching pods in a namespace for IP changes
func (m *LoadBalancerManager) StartPodWatcher(ctx context.Context, chainID, namespace string) error {
	m.watchersMutex.Lock()
	defer m.watchersMutex.Unlock()

	// Check if already watching
	if _, exists := m.watchers[chainID]; exists {
		m.logger.Debug("Already watching pods for chain", "chain_id", chainID)
		return nil
	}

	// Create a cancellable context for this watcher
	watchCtx, cancel := context.WithCancel(ctx)
	m.watchers[chainID] = cancel

	// Start the watcher goroutine
	go m.watchPods(watchCtx, chainID, namespace)

	m.logger.Info("Started pod watcher for consumer chain",
		"chain_id", chainID,
		"namespace", namespace)
	return nil
}

// StopPodWatcher stops watching pods for a specific chain
func (m *LoadBalancerManager) StopPodWatcher(chainID string) {
	m.watchersMutex.Lock()
	defer m.watchersMutex.Unlock()

	if cancel, exists := m.watchers[chainID]; exists {
		m.logger.Info("Stopping pod watcher", "chain_id", chainID)
		cancel()
		delete(m.watchers, chainID)
	}
}

// StopAllWatchers stops all pod watchers
func (m *LoadBalancerManager) StopAllWatchers() {
	m.watchersMutex.Lock()
	defer m.watchersMutex.Unlock()

	m.logger.Info("Stopping all pod watchers", "count", len(m.watchers))
	for chainID, cancel := range m.watchers {
		m.logger.Debug("Stopping watcher", "chain_id", chainID)
		cancel()
	}
	m.watchers = make(map[string]context.CancelFunc)
}

// watchPods watches for pod events in a namespace
func (m *LoadBalancerManager) watchPods(ctx context.Context, chainID, namespace string) {
	m.logger.Info("Pod watcher started", "chain_id", chainID, "namespace", namespace)
	defer m.logger.Info("Pod watcher stopped", "chain_id", chainID)

	// Retry logic for watch reconnection
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Create watch with label selector for consumer pods
		watchOpts := metav1.ListOptions{
			LabelSelector: "app=validator",
			Watch:         true,
		}

		watcher, err := m.clientset.CoreV1().Pods(namespace).Watch(ctx, watchOpts)
		if err != nil {
			m.logger.Error("Failed to create pod watcher",
				"chain_id", chainID,
				"error", err)
			// Retry after delay
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
				continue
			}
		}

		// Process watch events
		m.processWatchEvents(ctx, watcher, chainID, namespace)

		// Watcher closed, retry after a short delay
		select {
		case <-ctx.Done():
			return
		case <-time.After(1 * time.Second):
			m.logger.Debug("Restarting pod watcher", "chain_id", chainID)
		}
	}
}

// processWatchEvents processes events from the pod watcher
func (m *LoadBalancerManager) processWatchEvents(ctx context.Context, watcher watch.Interface, chainID, namespace string) {
	defer watcher.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-watcher.ResultChan():
			if !ok {
				m.logger.Debug("Watch channel closed", "chain_id", chainID)
				return
			}

			pod, ok := event.Object.(*corev1.Pod)
			if !ok {
				m.logger.Warn("Unexpected object type in watch event", "chain_id", chainID)
				continue
			}

			m.handlePodEvent(ctx, event.Type, pod, chainID, namespace)
		}
	}
}

// handlePodEvent handles individual pod events
func (m *LoadBalancerManager) handlePodEvent(ctx context.Context, eventType watch.EventType, pod *corev1.Pod, chainID, namespace string) {
	m.logger.Debug("Pod event received",
		"event_type", eventType,
		"pod", pod.Name,
		"chain_id", chainID,
		"pod_ip", pod.Status.PodIP,
		"phase", pod.Status.Phase)

	switch eventType {
	case watch.Added, watch.Modified:
		// Check if pod is ready and has an IP
		if pod.Status.Phase == corev1.PodRunning && pod.Status.PodIP != "" && isPodReady(pod) {
			// Update EndpointSlice with new pod IP
			if err := m.UpdateEndpointSlice(ctx, chainID, namespace, pod.Status.PodIP); err != nil {
				m.logger.Error("Failed to update EndpointSlice on pod event",
					"chain_id", chainID,
					"pod", pod.Name,
					"error", err)
			} else {
				m.logger.Info("EndpointSlice updated for pod event",
					"event_type", eventType,
					"chain_id", chainID,
					"pod", pod.Name,
					"new_ip", pod.Status.PodIP)
			}
		}
	case watch.Deleted:
		// Pod deleted - EndpointSlice will be updated when new pod is created
		m.logger.Info("Pod deleted, waiting for replacement",
			"chain_id", chainID,
			"pod", pod.Name)
	}
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
