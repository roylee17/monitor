package monitor

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/sourcenetwork/ics-operator/internal/subnet"
)

// HealthMonitor monitors consumer chain health
type HealthMonitor struct {
	logger     *slog.Logger
	k8sManager *subnet.K8sManager
	interval   time.Duration

	// Track monitored chains
	mu         sync.RWMutex
	monitoring map[string]context.CancelFunc // chainID -> cancel function
}

// NewHealthMonitor creates a new health monitor
func NewHealthMonitor(logger *slog.Logger, k8sManager *subnet.K8sManager) *HealthMonitor {
	return &HealthMonitor{
		logger:     logger,
		k8sManager: k8sManager,
		interval:   30 * time.Second,
		monitoring: make(map[string]context.CancelFunc),
	}
}

// StartMonitoring starts health monitoring for a consumer chain
func (hm *HealthMonitor) StartMonitoring(chainID string) {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	// Stop existing monitoring if any
	if cancel, exists := hm.monitoring[chainID]; exists {
		cancel()
	}

	// Create new monitoring context
	ctx, cancel := context.WithCancel(context.Background())
	hm.monitoring[chainID] = cancel

	// Start monitoring goroutine
	go hm.monitorChain(ctx, chainID)

	hm.logger.Info("Started health monitoring for consumer chain", "chain_id", chainID)
}

// StopMonitoring stops health monitoring for a consumer chain
func (hm *HealthMonitor) StopMonitoring(chainID string) {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	if cancel, exists := hm.monitoring[chainID]; exists {
		cancel()
		delete(hm.monitoring, chainID)
		hm.logger.Info("Stopped health monitoring for consumer chain", "chain_id", chainID)
	}
}

// monitorChain monitors a single consumer chain
func (hm *HealthMonitor) monitorChain(ctx context.Context, chainID string) {
	ticker := time.NewTicker(hm.interval)
	defer ticker.Stop()

	consecutiveFailures := 0
	const maxFailures = 3

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := hm.checkChainHealth(ctx, chainID); err != nil {
				consecutiveFailures++
				hm.logger.Warn("Consumer chain health check failed",
					"chain_id", chainID,
					"error", err,
					"consecutive_failures", consecutiveFailures)

				if consecutiveFailures >= maxFailures {
					hm.logger.Error("Consumer chain appears unhealthy",
						"chain_id", chainID,
						"consecutive_failures", consecutiveFailures)

					// Trigger recovery workflow
					hm.triggerRecovery(chainID)
					consecutiveFailures = 0 // Reset after recovery attempt
				}
			} else {
				if consecutiveFailures > 0 {
					hm.logger.Info("Consumer chain recovered",
						"chain_id", chainID,
						"previous_failures", consecutiveFailures)
				}
				consecutiveFailures = 0
			}
		}
	}
}

// checkChainHealth checks the health of a consumer chain
func (hm *HealthMonitor) checkChainHealth(ctx context.Context, chainID string) error {
	status, err := hm.k8sManager.GetConsumerChainStatus(ctx, chainID)
	if err != nil {
		return fmt.Errorf("failed to get chain status: %w", err)
	}

	// Check if deployment exists
	if status == nil {
		return fmt.Errorf("deployment not found")
	}

	// Check if any replicas are ready
	if status.ReadyReplicas == 0 {
		return fmt.Errorf("no ready replicas")
	}

	// Check if desired replicas match ready replicas
	if status.ReadyReplicas < status.DesiredReplicas {
		return fmt.Errorf("insufficient ready replicas: %d/%d",
			status.ReadyReplicas, status.DesiredReplicas)
	}

	// Additional health checks can be added here
	// - Check if chain is producing blocks
	// - Check if IBC connections are healthy
	// - Check if validator is signing blocks

	hm.logger.Debug("Consumer chain health check passed",
		"chain_id", chainID,
		"ready_replicas", status.ReadyReplicas,
		"available_replicas", status.AvailableReplicas)

	return nil
}

// triggerRecovery attempts to recover an unhealthy consumer chain
func (hm *HealthMonitor) triggerRecovery(chainID string) {
	hm.logger.Info("Triggering recovery for consumer chain", "chain_id", chainID)

	ctx := context.Background()

	// First, try to restart the deployment
	if err := hm.k8sManager.RestartDeployment(ctx, chainID); err != nil {
		hm.logger.Error("Failed to restart consumer chain deployment",
			"chain_id", chainID,
			"error", err)
		return
	}

	// Wait for deployment to come back up
	time.Sleep(10 * time.Second)

	// Check health again
	if err := hm.checkChainHealth(ctx, chainID); err != nil {
		hm.logger.Error("Consumer chain still unhealthy after restart",
			"chain_id", chainID,
			"error", err)
		// Further recovery actions could be implemented here
		// - Recreate deployment
		// - Alert operators
		// - Trigger manual intervention
	} else {
		hm.logger.Info("Consumer chain recovered successfully after restart",
			"chain_id", chainID)
	}
}

// GetMonitoredChains returns a list of chains being monitored
func (hm *HealthMonitor) GetMonitoredChains() []string {
	hm.mu.RLock()
	defer hm.mu.RUnlock()

	chains := make([]string, 0, len(hm.monitoring))
	for chainID := range hm.monitoring {
		chains = append(chains, chainID)
	}
	return chains
}

// StopAll stops monitoring all chains
func (hm *HealthMonitor) StopAll() {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	for chainID, cancel := range hm.monitoring {
		cancel()
		hm.logger.Info("Stopped health monitoring", "chain_id", chainID)
	}

	hm.monitoring = make(map[string]context.CancelFunc)
}
