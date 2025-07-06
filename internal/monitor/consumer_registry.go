package monitor

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"k8s.io/client-go/kubernetes"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ConsumerRegistry tracks which validators are participating in which consumer chains
type ConsumerRegistry struct {
	logger    *slog.Logger
	clientset kubernetes.Interface
	mu        sync.RWMutex
	// Map of consumer chain ID to set of validator names
	consumerValidators map[string]map[string]bool
	// Map of validator name to set of consumer chain IDs
	validatorConsumers map[string]map[string]bool
}

// NewConsumerRegistry creates a new consumer registry
func NewConsumerRegistry(logger *slog.Logger, clientset kubernetes.Interface) *ConsumerRegistry {
	return &ConsumerRegistry{
		logger:             logger,
		clientset:          clientset,
		consumerValidators: make(map[string]map[string]bool),
		validatorConsumers: make(map[string]map[string]bool),
	}
}

// RegisterConsumer records that a validator is participating in a consumer chain
func (r *ConsumerRegistry) RegisterConsumer(consumerChainID, validatorName string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Update consumer -> validators mapping
	if r.consumerValidators[consumerChainID] == nil {
		r.consumerValidators[consumerChainID] = make(map[string]bool)
	}
	r.consumerValidators[consumerChainID][validatorName] = true

	// Update validator -> consumers mapping
	if r.validatorConsumers[validatorName] == nil {
		r.validatorConsumers[validatorName] = make(map[string]bool)
	}
	r.validatorConsumers[validatorName][consumerChainID] = true

	r.logger.Debug("Registered validator for consumer chain",
		"consumer_chain_id", consumerChainID,
		"validator", validatorName)
}

// UnregisterConsumer removes a consumer chain from the registry
func (r *ConsumerRegistry) UnregisterConsumer(consumerChainID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Get validators for this consumer
	validators := r.consumerValidators[consumerChainID]
	if validators == nil {
		return
	}

	// Remove consumer from each validator's list
	for validatorName := range validators {
		if r.validatorConsumers[validatorName] != nil {
			delete(r.validatorConsumers[validatorName], consumerChainID)
			// Clean up empty maps
			if len(r.validatorConsumers[validatorName]) == 0 {
				delete(r.validatorConsumers, validatorName)
			}
		}
	}

	// Remove consumer from registry
	delete(r.consumerValidators, consumerChainID)

	r.logger.Info("Unregistered consumer chain",
		"consumer_chain_id", consumerChainID,
		"validator_count", len(validators))
}

// GetConsumersByValidator returns all consumer chains that a validator is participating in
func (r *ConsumerRegistry) GetConsumersByValidator(validatorName string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	consumers := r.validatorConsumers[validatorName]
	if consumers == nil {
		return nil
	}

	result := make([]string, 0, len(consumers))
	for consumerChainID := range consumers {
		result = append(result, consumerChainID)
	}
	return result
}

// GetValidatorsByConsumer returns all validators participating in a consumer chain
func (r *ConsumerRegistry) GetValidatorsByConsumer(consumerChainID string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	validators := r.consumerValidators[consumerChainID]
	if validators == nil {
		return nil
	}

	result := make([]string, 0, len(validators))
	for validatorName := range validators {
		result = append(result, validatorName)
	}
	return result
}

// RefreshFromKubernetes scans Kubernetes namespaces to rebuild the registry
func (r *ConsumerRegistry) RefreshFromKubernetes(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Clear existing data
	r.consumerValidators = make(map[string]map[string]bool)
	r.validatorConsumers = make(map[string]map[string]bool)

	// List all namespaces with consumer chain label
	namespaces, err := r.clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/part-of=consumer-chains",
	})
	if err != nil {
		return fmt.Errorf("failed to list consumer namespaces: %w", err)
	}

	for _, ns := range namespaces.Items {
		// Extract validator name and chain ID from namespace name
		// Format: {validator}-{chain-id}
		parts := extractValidatorAndChainID(ns.Name)
		if parts == nil {
			r.logger.Warn("Failed to parse consumer namespace name",
				"namespace", ns.Name)
			continue
		}

		validatorName := parts[0]
		chainID := parts[1]

		// Register the mapping
		if r.consumerValidators[chainID] == nil {
			r.consumerValidators[chainID] = make(map[string]bool)
		}
		r.consumerValidators[chainID][validatorName] = true

		if r.validatorConsumers[validatorName] == nil {
			r.validatorConsumers[validatorName] = make(map[string]bool)
		}
		r.validatorConsumers[validatorName][chainID] = true
	}

	r.logger.Info("Refreshed consumer registry from Kubernetes",
		"consumer_chains", len(r.consumerValidators),
		"validators", len(r.validatorConsumers))

	return nil
}

// extractValidatorAndChainID parses namespace name to get validator and chain ID
func extractValidatorAndChainID(namespace string) []string {
	// Expected format: {validator}-{chain-id}
	// Example: alice-testchain-0
	
	// Find the first hyphen to separate validator name
	firstHyphen := -1
	for i, ch := range namespace {
		if ch == '-' {
			firstHyphen = i
			break
		}
	}
	
	if firstHyphen == -1 || firstHyphen == len(namespace)-1 {
		return nil
	}
	
	validatorName := namespace[:firstHyphen]
	chainID := namespace[firstHyphen+1:]
	
	return []string{validatorName, chainID}
}

// GetAllConsumers returns all registered consumer chain IDs
func (r *ConsumerRegistry) GetAllConsumers() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]string, 0, len(r.consumerValidators))
	for consumerChainID := range r.consumerValidators {
		result = append(result, consumerChainID)
	}
	return result
}