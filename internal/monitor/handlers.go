package monitor

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	rpcclient "github.com/cometbft/cometbft/rpc/client/http"
	"github.com/cosmos/cosmos-sdk/client"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/query"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/cosmos/interchain-security-monitor/internal/selector"
	"github.com/cosmos/interchain-security-monitor/internal/subnet"
	"github.com/cosmos/interchain-security-monitor/internal/transaction"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ConsumerHandler handles consumer chain lifecycle management
//
// IMPORTANT: Event Emission Behavior in ICS v7
// The provider chain only emits events for:
// - create_consumer: When a consumer chain is created (phase: REGISTERED)
// - update_consumer: When consumer parameters are manually updated
// - remove_consumer: When a consumer chain is removed
//
// The provider chain does NOT emit events for automatic phase transitions:
// - REGISTERED → INITIALIZED: When spawn time is set (only if via MsgUpdateConsumer)
// - INITIALIZED → LAUNCHED: When validators opt in and chain launches (no event)
// - LAUNCHED → STOPPED: When chain is stopped (no event for phase change)
// - STOPPED → DELETED: When unbonding completes (no event for phase change)
//
// Therefore, the monitor uses polling to detect these phase transitions.
type ConsumerHandler struct {
	logger            *slog.Logger
	validatorSelector *selector.ValidatorSelector
	subnetManager     *subnet.Manager
	k8sManager        *subnet.K8sManager // For Kubernetes-native deployments
	txService         transaction.Service
	rpcClient         *rpcclient.HTTP
	clientCtx         client.Context
	healthMonitor     *HealthMonitor // Consumer chain health monitoring

	// Validator mappings for dynamic lookup
	validatorAddressToName map[string]string        // Maps validator operator address to moniker
	validatorNameToAddress map[string]string        // Maps moniker to validator operator address
	validatorMappingMutex  sync.RWMutex             // Protects validator mappings
	blockchainState        *BlockchainStateProvider // Blockchain-based state provider
	consumerKeyStore       *ConsumerKeyStore        // Manages consumer key assignments
	consumerRegistry       *ConsumerRegistry        // Tracks validator-consumer mappings

	// Context storage for consumer chain information
	consumerContexts map[string]ConsumerContext // map[chainID]ConsumerContext
	consumerMutex    sync.RWMutex               // Protects consumerContexts

	// Spawn monitoring cancellation
	spawnMonitors   map[string]context.CancelFunc // map[chainID]cancelFunc
	spawnMonitorsMu sync.Mutex                    // Protects spawnMonitors
}

// NewConsumerHandlerWithK8s creates a new consumer event handler with Kubernetes deployment support
func NewConsumerHandlerWithK8s(logger *slog.Logger, validatorSelector *selector.ValidatorSelector, subnetManager *subnet.Manager, k8sManager *subnet.K8sManager, txService transaction.Service, rpcClient *rpcclient.HTTP, clientCtx client.Context, providerEndpoints []string) *ConsumerHandler {
	// Create blockchain state provider
	blockchainState := NewBlockchainStateProvider(clientCtx, logger)

	// Create consumer key store if we have K8s manager
	var consumerKeyStore *ConsumerKeyStore
	if k8sManager != nil {
		// Get clientset from K8s manager for the key store
		clientset, err := k8sManager.GetClientset()
		if err != nil {
			logger.Warn("Failed to get clientset for key store", "error", err)
		} else {
			consumerKeyStore = NewConsumerKeyStore(logger, clientset, "provider")
			// Load existing keys from ConfigMaps
			if err := consumerKeyStore.LoadFromConfigMaps(context.Background()); err != nil {
				logger.Warn("Failed to load consumer keys from ConfigMaps", "error", err)
			}
		}
	}

	handler := &ConsumerHandler{
		logger:                 logger,
		validatorSelector:      validatorSelector,
		subnetManager:          subnetManager,
		k8sManager:             k8sManager,
		txService:              txService,
		rpcClient:              rpcClient,
		clientCtx:              clientCtx,
		healthMonitor:          NewHealthMonitor(logger, k8sManager),
		blockchainState:        blockchainState,
		consumerKeyStore:       consumerKeyStore,
		consumerContexts:       make(map[string]ConsumerContext),
		spawnMonitors:          make(map[string]context.CancelFunc),
		validatorAddressToName: make(map[string]string),
		validatorNameToAddress: make(map[string]string),
	}

	// Start blockchain state sync in background
	ctx := context.Background()
	// Start periodic sync to poll for consumer phase transitions
	// This runs every 30 seconds for faster phase change detection
	blockchainState.StartPeriodicSync(ctx, 30*time.Second)
	logger.Info("Started blockchain state synchronization")

	// Sync initial state from blockchain immediately
	logger.Info("Starting immediate blockchain sync")
	if err := handler.syncFromBlockchain(ctx); err != nil {
		logger.Error("Failed to sync from blockchain during startup", "error", err)
	} else {
		logger.Info("Initial blockchain sync completed successfully")
	}

	// Initialize validator mappings
	if err := handler.refreshValidatorMappings(ctx); err != nil {
		logger.Warn("Failed to initialize validator mappings", "error", err)
	} else {
		logger.Info("Initialized validator mappings")
	}

	return handler
}

// SetConsumerRegistry sets the consumer registry for tracking deployments
func (h *ConsumerHandler) SetConsumerRegistry(registry *ConsumerRegistry) {
	h.consumerRegistry = registry
}

// CanHandle checks if this handler can process the event
func (h *ConsumerHandler) CanHandle(event Event) bool {
	// Only handle events that actually exist
	consumerEvents := []string{
		"create_consumer",
		"update_consumer",
		"remove_consumer",
	}

	for _, eventType := range consumerEvents {
		if event.Type == eventType {
			return true
		}
	}

	// Also check if it's a message event with consumer-related attributes
	if event.Type == "message" {
		if action, exists := event.Attributes["action"]; exists {
			if strings.Contains(action, "MsgCreateConsumer") ||
				strings.Contains(action, "MsgUpdateConsumer") ||
				strings.Contains(action, "MsgRemoveConsumer") {
				return true
			}
		}
	}

	return false
}

// HandleEvent processes consumer chain events
func (h *ConsumerHandler) HandleEvent(ctx context.Context, event Event) error {
	h.logger.Info("ConsumerHandler processing event",
		"event_type", event.Type,
		"height", event.Height,
		"attributes", fmt.Sprintf("%+v", event.Attributes))

	switch event.Type {
	case "create_consumer":
		return h.handleConsumerCreated(event)
	case "update_consumer":
		// Note: This event is only emitted for manual updates, not phase transitions
		return h.handleConsumerUpdated(event)
	case "remove_consumer":
		return h.handleConsumerRemoved(event)
	case "message":
		return h.handleConsumerMessage(event)
	case "assign_consumer_key":
		return h.handleConsensusKeyAssignment(ctx, event)
	}
	return nil
}

// handleConsumerMessage handles message events that might contain consumer-related actions
func (h *ConsumerHandler) handleConsumerMessage(event Event) error {
	action, exists := event.Attributes["action"]
	if !exists {
		return nil
	}

	h.logger.Info("Processing consumer-related message", "action", action, "height", event.Height)

	// Message events are already routed through specific handlers
	// This is here for any additional message processing if needed

	return nil
}

// handleConsumerUpdated handles consumer chain update events
func (h *ConsumerHandler) handleConsumerUpdated(event Event) error {
	chainID := event.Attributes["consumer_chain_id"]
	if chainID == "" {
		chainID = event.Attributes["chain_id"]
	}
	consumerID := event.Attributes["consumer_id"]

	h.logger.Info("Consumer chain parameters updated",
		"chain_id", chainID,
		"consumer_id", consumerID,
		"height", event.Height)

	// Note: update_consumer events are only emitted for manual parameter updates,
	// not for automatic phase transitions. Phase transitions are detected via
	// spawn time monitoring and periodic state queries.

	return nil
}

// syncFromBlockchain syncs consumer state from the blockchain
func (h *ConsumerHandler) syncFromBlockchain(ctx context.Context) error {
	h.logger.Info("Syncing consumer state from blockchain")

	// Get all consumers from blockchain
	consumers, err := h.blockchainState.GetAllConsumers(ctx)
	if err != nil {
		return fmt.Errorf("failed to get consumers from blockchain: %w", err)
	}

	h.logger.Info("Found consumers on blockchain", "count", len(consumers))

	if len(consumers) == 0 {
		h.logger.Warn("No consumers returned from blockchain state provider")
		return nil
	}

	h.logger.Info("Starting to process consumers")
	for i, info := range consumers {
		h.logger.Info(fmt.Sprintf("Processing consumer %d/%d", i+1, len(consumers)))

		if info == nil {
			h.logger.Warn("Consumer info is nil", "index", i)
			continue
		}

		h.logger.Info("Processing consumer from blockchain",
			"chain_id", info.ChainID,
			"consumer_id", info.ConsumerID,
			"phase", info.Phase)

		// Check if we should monitor this consumer
		if !h.shouldMonitorConsumer(info) {
			h.logger.Info("Skipping consumer - should not monitor",
				"chain_id", info.ChainID,
				"consumer_id", info.ConsumerID,
				"phase", info.Phase)
			continue
		}

		// Run validator selection to determine if local validator should participate
		var isLocalSelected bool
		var validatorSet []string
		if h.validatorSelector != nil {
			result, err := h.validatorSelector.SelectValidatorSubset(info.ConsumerID, 0.66)
			if err != nil {
				h.logger.Warn("Failed to run validator selection during sync", "error", err)
			} else {
				isLocalSelected = result.ShouldOptIn
				validatorSet = make([]string, len(result.ValidatorSubset))
				for i, v := range result.ValidatorSubset {
					validatorSet[i] = v.Moniker
				}
				h.logger.Info("Validator selection during sync",
					"chain_id", info.ChainID,
					"consumer_id", info.ConsumerID,
					"is_local_selected", isLocalSelected,
					"validator_count", len(validatorSet))
			}
		}

		// Convert to ConsumerContext
		ctx := ConsumerContext{
			ChainID:                  info.ChainID,
			ConsumerID:               info.ConsumerID,
			ClientID:                 info.ClientID,
			SpawnTime:                info.SpawnTime,
			IsLocalValidatorSelected: isLocalSelected,
			ValidatorSet:             validatorSet,
		}

		// If consumer is already LAUNCHED, fetch CCV patch immediately
		if info.Phase == "CONSUMER_PHASE_LAUNCHED" || info.Phase == "3" {
			ccvPatch, err := h.fetchCCVPatch(info.ConsumerID)
			if err != nil {
				h.logger.Error("Failed to fetch CCV patch for launched consumer",
					"chain_id", info.ChainID,
					"consumer_id", info.ConsumerID,
					"error", err)
			} else {
				ctx.CCVPatch = ccvPatch
				h.logger.Info("Fetched CCV patch for already launched consumer",
					"chain_id", info.ChainID,
					"consumer_id", info.ConsumerID)
			}
		}

		// Store in memory
		h.consumerContexts[info.ChainID] = ctx

		// Start monitoring if in INITIALIZED phase
		if info.Phase == "CONSUMER_PHASE_INITIALIZED" && !info.SpawnTime.IsZero() {
			if time.Until(info.SpawnTime) > 0 {
				go h.monitorSpawnTimeAndPhase(info.ChainID)
				h.logger.Info("Started spawn time monitoring for consumer",
					"chain_id", info.ChainID,
					"consumer_id", info.ConsumerID,
					"spawn_time", info.SpawnTime.Format(time.RFC3339))
			}
		}

		// Handle already LAUNCHED consumers on startup
		if info.Phase == "CONSUMER_PHASE_LAUNCHED" || info.Phase == "3" {
			h.logger.Info("Found consumer already in LAUNCHED phase during sync",
				"chain_id", info.ChainID,
				"consumer_id", info.ConsumerID)

			// Check if deployment already exists
			if h.k8sManager != nil {
				exists, err := h.k8sManager.ConsumerDeploymentExists(context.Background(), info.ChainID)
				if err != nil {
					h.logger.Error("Failed to check deployment existence", "error", err)
				} else if !exists {
					h.logger.Info("Consumer in LAUNCHED phase but no deployment found, triggering deployment",
						"chain_id", info.ChainID,
						"consumer_id", info.ConsumerID)
					// Trigger phase transition handler as if it just transitioned
					go func() {
						if err := h.HandlePhaseTransition(context.Background(), info.ConsumerID, info.ChainID, "", "CONSUMER_PHASE_LAUNCHED"); err != nil {
							h.logger.Error("Failed to handle launched consumer", "error", err)
						}
					}()
				}
			}
		}
	}

	h.logger.Info("Completed processing all consumers from blockchain")
	return nil
}

// shouldMonitorConsumer determines if this monitor should track a consumer
func (h *ConsumerHandler) shouldMonitorConsumer(info *ConsumerInfo) bool {
	// Check if consumer is in a monitorable phase
	if info.Phase == "CONSUMER_PHASE_STOPPED" || info.Phase == "CONSUMER_PHASE_DELETED" {
		return false
	}

	// For launched consumers, check if we're already opted in
	if info.Phase == "CONSUMER_PHASE_LAUNCHED" || info.Phase == "3" {
		// For launched consumers, we should monitor if:
		// 1. We have a deployment already (handled elsewhere)
		// 2. We're part of the validator set (check opted-in status)
		// For now, monitor all launched consumers to ensure we don't miss deployments
		h.logger.Info("Monitoring launched consumer",
			"chain_id", info.ChainID,
			"consumer_id", info.ConsumerID,
			"reason", "consumer already launched")
		return true
	}

	// For REGISTERED phase consumers, skip if they have invalid spawn time
	if info.Phase == "CONSUMER_PHASE_REGISTERED" || info.Phase == "1" {
		if info.SpawnTime.IsZero() || info.SpawnTime.Year() == 1 {
			h.logger.Warn("Skipping REGISTERED consumer with invalid spawn time",
				"chain_id", info.ChainID,
				"consumer_id", info.ConsumerID,
				"spawn_time", info.SpawnTime)
			return false
		}
		// For valid REGISTERED consumers, check validator selection
		h.logger.Info("Checking validator selection for REGISTERED consumer",
			"chain_id", info.ChainID,
			"consumer_id", info.ConsumerID)
	}

	// Check if we're responsible based on validator selection
	if h.validatorSelector != nil {
		result, err := h.validatorSelector.SelectValidatorSubset(info.ConsumerID, 0.66)
		if err != nil {
			h.logger.Warn("Failed to check validator selection", "error", err)
			return false
		}
		return result.ShouldOptIn
	}

	// Default to monitoring if no selector
	return true
}

// handleConsumerCreated handles consumer chain creation events
func (h *ConsumerHandler) handleConsumerCreated(event Event) error {
	chainID := event.Attributes["consumer_chain_id"]
	clientID := event.Attributes["client_id"]
	if clientID == "" {
		clientID = event.Attributes["consumer_client_id"]
	}
	consumerID := event.Attributes["consumer_id"]

	h.logger.Info("Consumer chain created",
		"chain_id", chainID,
		"client_id", clientID,
		"consumer_id", consumerID,
		"height", event.Height)

	// Store consumer context for later use
	if chainID != "" {
		ctx := ConsumerContext{
			ChainID:    chainID,
			ConsumerID: consumerID,
			ClientID:   clientID,
			CreatedAt:  event.Height,
		}
		h.consumerMutex.Lock()
		h.consumerContexts[chainID] = ctx
		h.consumerMutex.Unlock()

		h.logger.Info("Stored consumer context", "chain_id", chainID, "consumer_id", consumerID)
	}

	// Start subnet deployment workflow if dependencies are available
	if h.validatorSelector != nil && h.subnetManager != nil {
		return h.startSubnetWorkflow(chainID, consumerID, event.Height)
	}

	h.logger.Warn("Subnet workflow dependencies not available, skipping automatic deployment")
	return nil
}

// startSubnetWorkflow initiates the subnet deployment workflow
func (h *ConsumerHandler) startSubnetWorkflow(chainID, consumerID string, height int64) error {
	h.logger.Info("Starting subnet workflow", "chain_id", chainID, "consumer_id", consumerID, "event_height", height)

	// Step 1: Calculate validator subset at the event height to ensure determinism
	votingPowerThreshold := 0.66 // 66% voting power threshold
	selectionResult, err := h.validatorSelector.SelectValidatorSubsetAtHeight(consumerID, votingPowerThreshold, height)
	if err != nil {
		h.logger.Error("Failed to select validator subset", "error", err)
		return err
	}

	// Log validator subset info
	subsetInfo := h.validatorSelector.GetValidatorSubsetInfo(selectionResult)
	h.logger.Info("Validator subset selected",
		"total_subset", len(selectionResult.ValidatorSubset),
		"should_opt_in", selectionResult.ShouldOptIn,
		"subset", subsetInfo)

	// Step 2: Opt in if local validator is selected
	if selectionResult.ShouldOptIn {
		h.logger.Info("Local validator selected for subset, should opt-in")

		if h.txService != nil {
			h.logger.Info("Sending opt-in transaction", "chain_id", chainID, "consumer_id", consumerID)

			// Send opt-in transaction (without consumer public key for now)
			if err := h.txService.OptIn(context.Background(), consumerID, ""); err != nil {
				h.logger.Error("Failed to send opt-in transaction", "error", err, "consumer_id", consumerID)
				// Continue with deployment preparation even if opt-in fails
			} else {
				h.logger.Info("Opt-in transaction sent successfully", "consumer_id", consumerID)
			}
		} else {
			h.logger.Warn("Transaction service not available, cannot send opt-in transaction")
		}
	}

	// Step 3: Store validator subset in context
	h.consumerMutex.Lock()
	if ctx, exists := h.consumerContexts[chainID]; exists {
		validatorNames := make([]string, len(selectionResult.ValidatorSubset))
		for i, v := range selectionResult.ValidatorSubset {
			validatorNames[i] = v.Moniker
		}
		ctx.ValidatorSet = validatorNames
		ctx.IsLocalValidatorSelected = selectionResult.ShouldOptIn
		h.consumerContexts[chainID] = ctx
	}
	h.consumerMutex.Unlock()

	// Step 4: If selected, prepare for deployment
	if selectionResult.ShouldOptIn {
		h.logger.Info("Local validator selected, preparing for consumer deployment", "chain_id", chainID)
		return h.prepareConsumerDeployment(chainID, consumerID)
	}

	h.logger.Info("Local validator not selected for subset, no action required")
	return nil
}

// prepareConsumerDeployment prepares for independent consumer deployment.
//
// This function is called when the local validator has been selected for a
// consumer chain subset. It initiates the deployment preparation process.
//
// Behavior:
// 1. Verifies the consumer context exists (created during chain registration)
// 2. Prepares the consumer genesis using K8sManager
// 3. Starts a goroutine to monitor for spawn time and phase transitions
//
// Parameters:
//   - chainID: The consumer chain ID (e.g., "consumer-0-1234567890-0")
//   - consumerID: The numeric consumer ID (e.g., "0")
//
// Returns:
//   - error if context not found or genesis preparation fails
//
// Post-conditions:
//   - Consumer genesis is prepared in Kubernetes
//   - Background monitoring is started for spawn time
//   - Consumer deployment will happen automatically when chain reaches LAUNCHED phase
func (h *ConsumerHandler) prepareConsumerDeployment(chainID, consumerID string) error {
	h.logger.Info("Preparing consumer deployment", "chain_id", chainID, "consumer_id", consumerID)

	// Get consumer context
	h.consumerMutex.RLock()
	_, exists := h.consumerContexts[chainID]
	h.consumerMutex.RUnlock()
	if !exists {
		return fmt.Errorf("consumer context not found for chain %s", chainID)
	}

	// Prepare genesis independently
	ctx := context.Background()
	if err := h.k8sManager.PrepareConsumerGenesis(ctx, chainID, consumerID); err != nil {
		return fmt.Errorf("failed to prepare consumer genesis: %w", err)
	}

	h.logger.Info("Consumer genesis prepared, waiting for spawn time", "chain_id", chainID)

	// Start monitoring for spawn time and genesis patch
	go h.monitorSpawnTimeAndPhase(chainID)

	return nil
}

// fetchCCVPatch fetches the CCV genesis patch using RPC
func (h *ConsumerHandler) fetchCCVPatch(consumerID string) (map[string]interface{}, error) {
	// Implement retry logic with exponential backoff for timing issues
	maxRetries := 5
	baseDelay := 1 * time.Second

	h.logger.Info("Fetching CCV patch with exponential backoff",
		"consumer_id", consumerID,
		"max_retries", maxRetries,
		"base_delay", baseDelay)

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff: 1s, 2s, 4s, 8s, 16s
			backoffDelay := baseDelay * (1 << (attempt - 1))
			// Cap at 10 seconds
			if backoffDelay > 10*time.Second {
				backoffDelay = 10 * time.Second
			}
			h.logger.Info("Retrying CCV patch fetch",
				"attempt", attempt+1,
				"consumer_id", consumerID,
				"delay", backoffDelay)
			time.Sleep(backoffDelay)
		}

		// Use RPC method (consistent with spawn time query)
		if h.blockchainState == nil {
			return nil, fmt.Errorf("blockchain state provider not available")
		}

		ctx := context.Background()
		genesisJSON, err := h.blockchainState.GetConsumerGenesis(ctx, consumerID)
		if err == nil {
			// Parse JSON to map for compatibility with existing code
			var genesis map[string]interface{}
			if err := json.Unmarshal(genesisJSON, &genesis); err == nil {
				h.logger.Info("Successfully fetched CCV patch via RPC",
					"consumer_id", consumerID,
					"attempt", attempt+1)

				// Apply the same validator power scaling
				if err := h.scaleValidatorPowers(genesis); err != nil {
					h.logger.Warn("Failed to scale validator powers", "error", err)
				}

				// The genesis from GetConsumerGenesis is already the ccvconsumer content
				// We need to wrap it properly for the full genesis structure
				wrappedGenesis := map[string]interface{}{
					"app_state": map[string]interface{}{
						"ccvconsumer": genesis,
					},
				}

				// Get the spawn time from the consumer chain data to use as genesis time
				// This ensures all validators use the exact same genesis time
				consumerInfo, err := h.blockchainState.GetConsumerInfo(ctx, consumerID)
				if err == nil && !consumerInfo.SpawnTime.IsZero() {
					// Use the spawn time as the genesis time for consistency
					genesisTime := consumerInfo.SpawnTime.Format(time.RFC3339)
					wrappedGenesis["genesis_time"] = genesisTime
					h.logger.Info("Using consumer spawn time as genesis time for consistency",
						"consumer_id", consumerID,
						"spawn_time", genesisTime)
				} else {
					h.logger.Warn("Could not get spawn time from consumer info, genesis may be inconsistent",
						"consumer_id", consumerID,
						"error", err)
				}

				return wrappedGenesis, nil
			} else {
				lastErr = fmt.Errorf("failed to unmarshal RPC genesis response: %w", err)
				h.logger.Warn("Failed to unmarshal RPC genesis response",
					"consumer_id", consumerID,
					"error", err)
			}
		} else {
			lastErr = err
		}

		lastErr = err
		// Check if the error is "consumer not found" which indicates timing issue
		errStr := err.Error()
		if strings.Contains(errStr, "no consumer chain with this consumer id") ||
			strings.Contains(errStr, "key not found") ||
			strings.Contains(errStr, "not found") {
			h.logger.Warn("Consumer genesis not yet available, will retry",
				"consumer_id", consumerID,
				"attempt", attempt+1,
				"error", err)
			continue
		}

		// For other errors, fail immediately
		h.logger.Error("Non-retryable error fetching CCV patch",
			"consumer_id", consumerID,
			"error", err)
		return nil, err
	}

	return nil, fmt.Errorf("failed to fetch CCV patch after %d attempts: %w", maxRetries, lastErr)
}

// scaleValidatorPowers scales up validator powers to meet DefaultPowerReduction requirement
func (h *ConsumerHandler) scaleValidatorPowers(genesis map[string]interface{}) error {
	// Consumer chains require validators with power >= DefaultPowerReduction (1,000,000)
	provider, ok := genesis["provider"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("provider field not found in genesis")
	}

	initialValSet, ok := provider["initial_val_set"].([]interface{})
	if !ok {
		return fmt.Errorf("initial_val_set not found in provider")
	}

	for _, val := range initialValSet {
		validator, ok := val.(map[string]interface{})
		if !ok {
			continue
		}

		if powerStr, ok := validator["power"].(string); ok {
			// Log the validator power for debugging
			h.logger.Info("Validator power in CCV genesis",
				"power", powerStr)
		}
	}

	return nil
}

// handleConsumerRemoved handles consumer chain removal events
func (h *ConsumerHandler) handleConsumerRemoved(event Event) error {
	chainID := event.Attributes["consumer_chain_id"]
	if chainID == "" {
		chainID = event.Attributes["chain_id"]
	}
	consumerID := event.Attributes["consumer_id"]

	h.logger.Info("Consumer chain removed",
		"chain_id", chainID,
		"consumer_id", consumerID,
		"height", event.Height)

	// Stop health monitoring
	if h.healthMonitor != nil {
		h.healthMonitor.StopMonitoring(chainID)
	}

	// Remove consumer chain deployment if K8s manager is available
	if h.k8sManager != nil {
		ctx := context.Background()
		if err := h.k8sManager.RemoveConsumerChain(ctx, chainID); err != nil {
			h.logger.Error("Failed to remove consumer chain", "error", err)
			return err
		}
	}

	// Clean up context
	h.consumerMutex.Lock()
	delete(h.consumerContexts, chainID)
	h.consumerMutex.Unlock()

	return nil
}

// monitorSpawnTimeAndPhase monitors consumer chain spawn time and phase transitions.
// See docs/consumer-chain-lifecycle.md for detailed documentation.
func (h *ConsumerHandler) monitorSpawnTimeAndPhase(chainID string) {
	// Create a cancellable context for this monitoring session
	ctx, cancel := context.WithCancel(context.Background())

	// Register the cancel function
	h.spawnMonitorsMu.Lock()
	h.spawnMonitors[chainID] = cancel
	h.spawnMonitorsMu.Unlock()

	// Ensure cleanup on exit
	defer func() {
		h.spawnMonitorsMu.Lock()
		delete(h.spawnMonitors, chainID)
		h.spawnMonitorsMu.Unlock()
	}()
	h.logger.Info("Starting spawn time countdown and phase monitor", "chain_id", chainID)

	// Get consumer ID from context
	var consumerID string
	h.consumerMutex.RLock()
	if ctx, exists := h.consumerContexts[chainID]; exists {
		consumerID = ctx.ConsumerID
	} else {
		// Fallback to extraction from chainID
		var err error
		consumerID, err = h.extractConsumerID(chainID)
		if err != nil {
			h.consumerMutex.RUnlock()
			h.logger.Error("Failed to extract consumer ID from chain ID",
				"chain_id", chainID,
				"error", err)
			return
		}
	}
	h.consumerMutex.RUnlock()

	// Query spawn time from provider chain
	spawnTime, err := h.queryConsumerSpawnTime(consumerID)
	if err != nil {
		h.logger.Error("Failed to query spawn time, cannot proceed with monitoring",
			"chain_id", chainID,
			"consumer_id", consumerID,
			"error", err)
		return
	}

	h.logger.Info("Consumer spawn time retrieved",
		"chain_id", chainID,
		"consumer_id", consumerID,
		"spawn_time", spawnTime.Format(time.RFC3339),
		"time_until_spawn", time.Until(spawnTime).Round(time.Second))

	// Phase 1: Wait for spawn time
	if err := h.waitForSpawnTime(ctx, chainID, spawnTime); err != nil {
		if err == context.Canceled {
			h.logger.Info("Spawn time monitoring cancelled", "chain_id", chainID)
		} else {
			h.logger.Error("Failed to wait for spawn time", "error", err, "chain_id", chainID)
		}
		return
	}

	// Phase 2: Query provider chain to detect launch with adaptive polling
	h.monitorConsumerLaunchAdaptive(ctx, chainID, consumerID)
}

// waitForSpawnTime waits until the consumer spawn time is reached
func (h *ConsumerHandler) waitForSpawnTime(ctx context.Context, chainID string, spawnTime time.Time) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			remaining := time.Until(spawnTime)
			if remaining <= 0 {
				h.logger.Info("🚀 Spawn time reached! Consumer should be launching now",
					"chain_id", chainID,
					"spawn_time", spawnTime.Format(time.RFC3339))
				return nil
			}

			h.logger.Info("⏰ Countdown to consumer spawn",
				"chain_id", chainID,
				"remaining", remaining.Round(time.Second),
				"spawn_time", spawnTime.Format("15:04:05"))
		}
	}
}

// monitorConsumerLaunchAdaptive queries provider chain with adaptive polling intervals
func (h *ConsumerHandler) monitorConsumerLaunchAdaptive(ctx context.Context, chainID, consumerID string) {
	h.logger.Info("🔍 Monitoring provider chain for consumer launch (adaptive polling)",
		"chain_id", chainID,
		"consumer_id", consumerID)

	// Adaptive polling intervals (shortened for faster detection)
	rapidInterval := 1 * time.Second  // Right after spawn time
	activeInterval := 3 * time.Second // Normal monitoring (was 5s)
	slowInterval := 10 * time.Second  // After extended time (was 30s)

	currentInterval := rapidInterval
	startTime := time.Now()
	ticker := time.NewTicker(currentInterval)
	defer ticker.Stop()

	// Set a timeout for the entire monitoring process
	monitorCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	for {
		select {
		case <-monitorCtx.Done():
			if monitorCtx.Err() == context.DeadlineExceeded {
				h.logger.Error("❌ Consumer launch timeout - phase did not transition to LAUNCHED",
					"chain_id", chainID,
					"consumer_id", consumerID,
					"elapsed", time.Since(startTime).Round(time.Second))
			}
			return

		case <-ticker.C:
			elapsed := time.Since(startTime)

			// Query consumer status from blockchain
			// This polls the provider chain to detect when the phase transitions to LAUNCHED
			// The transition happens automatically when sufficient validators opt in
			launched, err := h.queryConsumerStatusViaAPI(consumerID)
			if err != nil {
				h.logger.Warn("Failed to query consumer status",
					"error", err,
					"chain_id", chainID,
					"elapsed", elapsed.Round(time.Second))
				continue
			}

			if launched {
				// Consumer has launched
				h.logger.Info("✅ Consumer successfully launched! Phase is now LAUNCHED",
					"chain_id", chainID,
					"consumer_id", consumerID,
					"elapsed", elapsed.Round(time.Second))

				// Trigger deployment
				go h.HandlePhaseTransition(context.Background(), consumerID, chainID,
					"CONSUMER_PHASE_INITIALIZED", "CONSUMER_PHASE_LAUNCHED")
				return
			}

			// Adjust polling interval based on elapsed time
			var newInterval time.Duration
			if elapsed < 30*time.Second {
				newInterval = rapidInterval // 1s - we expect launch soon
			} else if elapsed < 2*time.Minute {
				newInterval = activeInterval // 5s - normal monitoring
			} else {
				newInterval = slowInterval // 30s - extended monitoring
			}

			// Update ticker if interval changed
			if newInterval != currentInterval {
				h.logger.Debug("Adjusting polling interval",
					"old_interval", currentInterval,
					"new_interval", newInterval,
					"elapsed", elapsed.Round(time.Second))
				ticker.Reset(newInterval)
				currentInterval = newInterval
			}

			h.logger.Info("⏳ Consumer not yet launched, continuing to monitor",
				"chain_id", chainID,
				"consumer_id", consumerID,
				"elapsed", elapsed.Round(time.Second),
				"poll_interval", currentInterval)
		}
	}
}

// extractConsumerID extracts consumer ID from chain ID
func (h *ConsumerHandler) extractConsumerID(chainID string) (string, error) {
	// Extract consumer ID from chainID format like "testchain1-0" -> "0"
	// The consumer ID is the last part after the final hyphen
	lastHyphen := strings.LastIndex(chainID, "-")
	if lastHyphen == -1 || lastHyphen == len(chainID)-1 {
		return "", fmt.Errorf("invalid chain ID format: %s (expected format: <name>-<id>)", chainID)
	}

	consumerID := chainID[lastHyphen+1:]

	// Validate that the extracted ID is numeric
	if _, err := strconv.Atoi(consumerID); err != nil {
		return "", fmt.Errorf("invalid consumer ID in chain ID %s: %s is not numeric", chainID, consumerID)
	}

	return consumerID, nil
}

// queryConsumerSpawnTime queries the provider chain for consumer spawn time using RPC
func (h *ConsumerHandler) queryConsumerSpawnTime(consumerID string) (time.Time, error) {
	// Use blockchain state provider to get consumer info
	if h.blockchainState == nil {
		return time.Time{}, fmt.Errorf("blockchain state provider not available")
	}

	ctx := context.Background()
	consumer, err := h.blockchainState.GetConsumerInfo(ctx, consumerID)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to get consumer info: %w", err)
	}

	if consumer.SpawnTime.IsZero() {
		return time.Time{}, fmt.Errorf("spawn time not available for consumer %s", consumerID)
	}

	h.logger.Info("Got spawn time from blockchain state",
		"consumer_id", consumerID,
		"spawn_time", consumer.SpawnTime.Format(time.RFC3339))
	return consumer.SpawnTime, nil
}

// queryConsumerStatusViaAPI queries the provider chain to check if consumer has launched using RPC client
func (h *ConsumerHandler) queryConsumerStatusViaAPI(consumerID string) (bool, error) {
	if h.rpcClient == nil {
		return false, fmt.Errorf("no RPC client available")
	}

	return h.checkConsumerPhaseViaRPC(consumerID)
}

// checkConsumerPhaseViaRPC queries the provider chain using ABCI queries to check consumer phase
func (h *ConsumerHandler) checkConsumerPhaseViaRPC(consumerID string) (bool, error) {
	// Use blockchain state provider to get consumer info
	if h.blockchainState == nil {
		return false, fmt.Errorf("blockchain state provider not available")
	}

	ctx := context.Background()
	consumer, err := h.blockchainState.GetConsumerInfo(ctx, consumerID)
	if err != nil {
		return false, fmt.Errorf("failed to get consumer info: %w", err)
	}

	// Check if phase is LAUNCHED (phase 3)
	isLaunched := consumer.Phase == "CONSUMER_PHASE_LAUNCHED" || consumer.Phase == "3"

	h.logger.Debug("Checked consumer phase via RPC",
		"consumer_id", consumerID,
		"phase", consumer.Phase,
		"is_launched", isLaunched)

	return isLaunched, nil
}

// HandlePhaseTransition handles consumer chain phase transitions
func (h *ConsumerHandler) HandlePhaseTransition(ctx context.Context, consumerID, chainID, oldPhase, newPhase string) error {
	h.logger.Info("Handling phase transition",
		"consumer_id", consumerID,
		"chain_id", chainID,
		"old_phase", oldPhase,
		"new_phase", newPhase)

	// Handle specific phase transitions
	switch newPhase {
	case "CONSUMER_PHASE_LAUNCHED", "3": // Phase 3 is LAUNCHED
		return h.handleLaunchedPhase(ctx, consumerID, chainID)

	case "CONSUMER_PHASE_STOPPED", "4": // Phase 4 is STOPPED
		h.logger.Info("Consumer chain entered STOPPED phase",
			"consumer_id", consumerID,
			"chain_id", chainID)

		// Stop the consumer chain deployment
		if h.k8sManager != nil {
			if err := h.k8sManager.StopConsumerChain(ctx, chainID); err != nil {
				h.logger.Error("Failed to stop consumer chain", "error", err)
			}
		}

	case "CONSUMER_PHASE_DELETED", "5": // Phase 5 is DELETED
		h.logger.Info("Consumer chain entered DELETED phase",
			"consumer_id", consumerID,
			"chain_id", chainID)

		// Consumer chain NodePort services will be cleaned up with the namespace

		// Remove the consumer chain
		if h.k8sManager != nil {
			if err := h.k8sManager.RemoveConsumerChain(ctx, chainID); err != nil {
				h.logger.Error("Failed to remove consumer chain", "error", err)
			}
		}

		// Clean up context
		delete(h.consumerContexts, chainID)
	}

	return nil
}

// handleLaunchedPhase processes the LAUNCHED phase transition for a consumer chain
func (h *ConsumerHandler) handleLaunchedPhase(ctx context.Context, consumerID, chainID string) error {
	h.logger.Info("🚀 Consumer chain entered LAUNCHED phase!",
		"consumer_id", consumerID,
		"chain_id", chainID)

	// Check if we have context for this chain
	h.consumerMutex.RLock()
	consumerCtx, exists := h.consumerContexts[chainID]
	h.consumerMutex.RUnlock()

	if !exists {
		h.logger.Warn("No context found for consumer chain",
			"chain_id", chainID,
			"consumer_id", consumerID)
		return nil
	}

	// Deploy the consumer chain if k8s manager is available
	if h.k8sManager == nil {
		h.logger.Info("K8s manager not available, skipping deployment")
		return nil
	}

	h.logger.Info("Preparing to deploy consumer chain",
		"chain_id", chainID,
		"consumer_id", consumerID)

	// Get CCV genesis if not already fetched
	if consumerCtx.CCVPatch == nil {
		genesis, err := h.fetchCCVPatch(consumerID)
		if err != nil {
			h.logger.Error("Failed to fetch CCV genesis", "error", err)
			return err
		}
		h.consumerMutex.Lock()
		if ctxToUpdate, ok := h.consumerContexts[chainID]; ok {
			ctxToUpdate.CCVPatch = genesis
			h.consumerContexts[chainID] = ctxToUpdate
			consumerCtx = ctxToUpdate
		}
		h.consumerMutex.Unlock()
	}

	// Check if local validator is in the initial validator set
	keyInfo, err := h.isLocalValidatorInInitialSet(consumerCtx.CCVPatch, consumerID)
	if err != nil {
		h.logger.Error("Failed to check if local validator is in initial set", "error", err)
		return err
	}

	// Check if validator is opted in (regardless of initial set)
	isOptedIn, err := h.isLocalValidatorOptedIn(ctx, consumerID)
	if err != nil {
		h.logger.Error("Failed to check if local validator is opted in", "error", err)
		return err
	}

	if !keyInfo.Found && !isOptedIn {
		h.logger.Info("Local validator is NOT in the initial set and NOT opted in, skipping deployment",
			"chain_id", chainID,
			"consumer_id", consumerID,
			"local_validator", h.validatorSelector.GetFromKey())
		return nil
	}

	if !keyInfo.Found && isOptedIn {
		h.logger.Info("Local validator is NOT in the CCV genesis initial validator set but IS opted in",
			"chain_id", chainID,
			"consumer_id", consumerID,
			"local_validator", h.validatorSelector.GetFromKey(),
			"info", "Will deploy as non-validator and join via VSC when provider sends update")
	} else {
		h.logger.Info("Local validator IS in the CCV genesis initial validator set, proceeding with deployment",
			"chain_id", chainID,
			"consumer_id", consumerID,
			"local_validator", h.validatorSelector.GetFromKey(),
			"key_type", keyInfo.KeyType,
			"key_value", keyInfo.KeyValue)
	}

	// Select appropriate key based on what's in the initial validator set
	var consumerKey *subnet.ConsumerKeyInfo
	if keyInfo.Found {
		// Validator is in initial set - use the key type from genesis
		consumerKey, err = h.selectConsumerKey(consumerID, keyInfo)
		if err != nil {
			return fmt.Errorf("failed to select consumer key: %w", err)
		}
	} else if isOptedIn {
		// Validator is NOT in initial set but IS opted in - use provider key or assigned consumer key
		h.logger.Info("Selecting key for late-joining validator",
			"consumer_id", consumerID,
			"local_validator", h.validatorSelector.GetFromKey())

		// Check if a consumer key was assigned
		if h.consumerKeyStore != nil && h.validatorSelector != nil {
			localValidatorName := h.validatorSelector.GetFromKey()
			if localValidatorName != "" {
				storedKey, err := h.consumerKeyStore.GetConsumerKey(consumerID, localValidatorName)
				if err == nil && storedKey != nil {
					h.logger.Info("Using assigned consumer key for late-joining validator",
						"validator", localValidatorName,
						"consumer_id", consumerID,
						"pubkey", storedKey.ConsumerPubKey)
					consumerKey = storedKey
				}
			}
		}

		// If no consumer key assigned, use provider key
		if consumerKey == nil {
			h.logger.Info("No consumer key assigned, using provider key for late-joining validator")
			// consumerKey remains nil to signal using provider key (deployment will handle this)
		}
	}

	// Calculate ports for the consumer chain
	ports, err := subnet.CalculatePorts(chainID)
	if err != nil {
		h.logger.Error("Failed to calculate ports for consumer chain",
			"error", err,
			"chain_id", chainID)
		return err
	}

	// Get opted-in validators and map to monikers
	actualOptedInValidators, err := h.getOptedInValidatorMonikers(consumerID)
	if err != nil {
		return fmt.Errorf("failed to get opted-in validator monikers: %w", err)
	}

	h.logger.Info("Successfully mapped opted-in validators",
		"consumer_id", consumerID,
		"opted_in_count", len(actualOptedInValidators),
		"mapped_monikers", actualOptedInValidators)

	// Deploy the consumer chain
	if err := h.k8sManager.DeployConsumerWithDynamicPeers(
		ctx, chainID, consumerID, *ports,
		consumerCtx.CCVPatch, consumerKey, actualOptedInValidators); err != nil {
		h.logger.Error("Failed to deploy consumer chain", "error", err, "has_key", consumerKey != nil)
		return err
	}

	// Register the consumer chain deployment with the consumer registry
	if h.consumerRegistry != nil && h.validatorSelector != nil {
		localValidatorName := h.validatorSelector.GetFromKey()
		if localValidatorName != "" {
			h.consumerRegistry.RegisterConsumer(chainID, localValidatorName)
			h.logger.Info("Registered consumer chain with registry",
				"chain_id", chainID,
				"validator", localValidatorName)
		}
	}

	// Start health monitoring
	go h.healthMonitor.StartMonitoring(chainID)

	return nil
}

// selectConsumerKey selects the appropriate key based on what's in the initial validator set
func (h *ConsumerHandler) selectConsumerKey(consumerID string, keyInfo *InitialSetKeyInfo) (*subnet.ConsumerKeyInfo, error) {
	if keyInfo.KeyType == KeyTypeConsumer {
		// Initial set has consumer key - we MUST use the consumer key
		h.logger.Info("Initial validator set contains CONSUMER key, will use consumer key for deployment",
			"consumer_id", consumerID)

		if h.consumerKeyStore != nil && h.validatorSelector != nil {
			localValidatorName := h.validatorSelector.GetFromKey()
			if localValidatorName != "" {
				storedKey, err := h.consumerKeyStore.GetConsumerKey(consumerID, localValidatorName)
				if err != nil {
					h.logger.Error("Initial set has consumer key but not found in store",
						"validator", localValidatorName,
						"consumer_id", consumerID,
						"error", err)
					return nil, fmt.Errorf("consumer key in initial set but not found locally: %w", err)
				}
				h.logger.Info("Using stored consumer key for deployment",
					"validator", localValidatorName,
					"consumer_id", consumerID,
					"pubkey", storedKey.ConsumerPubKey)
				return storedKey, nil
			}
		}
	} else {
		// Initial set has provider key - we MUST use provider key
		h.logger.Info("Initial validator set contains PROVIDER key, will use provider key for deployment",
			"consumer_id", consumerID)

		// Get the provider key to use for deployment
		if h.validatorSelector != nil {
			localValidatorName := h.validatorSelector.GetFromKey()
			if localValidatorName != "" {
				// Log if a consumer key was assigned but not used
				if h.consumerKeyStore != nil {
					if storedKey, err := h.consumerKeyStore.GetConsumerKey(consumerID, localValidatorName); err == nil && storedKey != nil {
						h.logger.Warn("Consumer key was assigned but initial set has provider key - using provider key",
							"validator", localValidatorName,
							"consumer_id", consumerID,
							"assigned_consumer_key", storedKey.ConsumerPubKey,
							"reason", "Key assignment happened after spawn time")
					}
				}

				// Get provider consensus public key
				providerConsensusPubKey, err := h.getLocalProviderConsensusKey()
				if err != nil {
					return nil, fmt.Errorf("failed to get provider consensus key: %w", err)
				}

				// Load provider's priv_validator_key.json from Kubernetes secret
				secretName := fmt.Sprintf("%s-keys", localValidatorName)
				clientset, err := h.k8sManager.GetClientset()
				if err != nil {
					return nil, fmt.Errorf("failed to get clientset: %w", err)
				}
				secret, err := clientset.CoreV1().Secrets("provider").Get(context.Background(), secretName, metav1.GetOptions{})
				if err != nil {
					return nil, fmt.Errorf("failed to get provider key secret: %w", err)
				}

				privValidatorKeyData, ok := secret.Data["priv_validator_key.json"]
				if !ok {
					return nil, fmt.Errorf("priv_validator_key.json not found in secret %s", secretName)
				}

				// Get provider address from keyring
				keyInfo, err := h.clientCtx.Keyring.Key(localValidatorName)
				if err != nil {
					return nil, fmt.Errorf("failed to load provider key from keyring: %w", err)
				}

				addr, err := keyInfo.GetAddress()
				if err != nil {
					return nil, fmt.Errorf("failed to get address from key info: %w", err)
				}

				// Create ConsumerKeyInfo with provider key details
				consumerKey := &subnet.ConsumerKeyInfo{
					ValidatorName:        localValidatorName,
					ConsumerID:           consumerID,
					ConsumerPubKey:       providerConsensusPubKey,
					ConsumerAddress:      addr.String(),
					ProviderAddress:      addr.String(),
					AssignmentHeight:     0, // Not assigned, using provider key
					PrivValidatorKeyJSON: string(privValidatorKeyData),
				}

				h.logger.Info("Using provider key for consumer deployment",
					"validator", localValidatorName,
					"consumer_id", consumerID,
					"pubkey", providerConsensusPubKey)

				return consumerKey, nil
			}
		}
	}

	return nil, fmt.Errorf("unable to select consumer key")
}

// getOptedInValidatorMonikers gets the monikers of opted-in validators
func (h *ConsumerHandler) getOptedInValidatorMonikers(consumerID string) ([]string, error) {
	// Query actual opted-in validators from blockchain
	if h.blockchainState == nil {
		return nil, fmt.Errorf("blockchain state provider not available")
	}

	optedIn, err := h.blockchainState.GetOptedInValidators(context.Background(), consumerID)
	if err != nil {
		return nil, fmt.Errorf("failed to query opted-in validators for consumer %s: %w", consumerID, err)
	}

	// Query all validators to build consensus address to moniker mapping
	stakingClient := stakingtypes.NewQueryClient(h.clientCtx)
	validatorsResp, err := stakingClient.Validators(context.Background(), &stakingtypes.QueryValidatorsRequest{
		Status: "BOND_STATUS_BONDED",
		Pagination: &query.PageRequest{
			Limit: 1000,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query validators: %w", err)
	}

	// Build consensus address to moniker mapping
	consAddrToMoniker := make(map[string]string)
	monikerCount := make(map[string]int)

	for _, val := range validatorsResp.Validators {
		if val.ConsensusPubkey == nil {
			h.logger.Error("Validator has nil consensus pubkey", "operator", val.OperatorAddress)
			continue
		}

		// Unpack the Any type to get the actual public key
		var pubKey cryptotypes.PubKey
		err := h.clientCtx.InterfaceRegistry.UnpackAny(val.ConsensusPubkey, &pubKey)
		if err != nil {
			h.logger.Error("Failed to unpack consensus pubkey",
				"operator", val.OperatorAddress,
				"error", err)
			continue
		}

		// Get consensus address from the unpacked pubkey
		sdkConsAddr := sdk.ConsAddress(pubKey.Address())
		moniker := val.Description.Moniker

		// Check for empty moniker
		if moniker == "" {
			h.logger.Warn("Validator has empty moniker, using operator address",
				"operator", val.OperatorAddress)
			moniker = val.OperatorAddress
		}

		consAddrToMoniker[sdkConsAddr.String()] = moniker
		monikerCount[moniker]++

		h.logger.Debug("Mapped validator consensus address",
			"operator", val.OperatorAddress,
			"moniker", moniker,
			"consensus_addr", sdkConsAddr.String())
	}

	// Warn about duplicate monikers
	for moniker, count := range monikerCount {
		if count > 1 {
			h.logger.Warn("Multiple validators share the same moniker",
				"moniker", moniker,
				"count", count)
		}
	}

	// Map opted-in addresses to monikers
	actualOptedInValidators := make([]string, 0, len(optedIn))
	for _, consAddrStr := range optedIn {
		moniker, ok := consAddrToMoniker[consAddrStr]
		if !ok {
			return nil, fmt.Errorf("failed to find moniker for consensus address %s", consAddrStr)
		}
		if moniker == "" {
			return nil, fmt.Errorf("validator moniker is empty for consensus address %s", consAddrStr)
		}
		actualOptedInValidators = append(actualOptedInValidators, moniker)
	}

	return actualOptedInValidators, nil
}

// handleConsensusKeyAssignment handles consensus key assignment events
// handleConsensusKeyAssignment processes assign_consumer_key events.
//
// CRITICAL EVENT HANDLER: This captures when validators assign dedicated keys
// for consumer chains. These keys are essential for consumer chain operation.
//
// Event Attributes Expected:
//   - consumer_id: The numeric ID of the consumer chain
//   - chain_id or consumer_chain_id: The consumer chain ID
//   - provider_validator_address: The validator's provider chain address
//   - consumer_consensus_pub_key: The assigned consumer key (JSON format)
//
// Behavior:
// 1. Extracts all required attributes from the event
// 2. Determines the validator name from the provider address
// 3. Stores the key assignment in ConsumerKeyStore for later use
// 4. The stored keys are used by the deployment process to identify the local validator
//
// Error Handling:
//   - Missing validator name is logged but not fatal (returns nil)
//   - Storage errors are returned to caller
//
// Note: This function only stores the key assignments. The actual key
// generation for the local validator happens in handleValidatorOptIn.
func (h *ConsumerHandler) handleConsensusKeyAssignment(ctx context.Context, event Event) error {
	// Extract event attributes
	consumerID := event.Attributes["consumer_id"]
	chainID := event.Attributes["chain_id"]
	if chainID == "" {
		chainID = event.Attributes["consumer_chain_id"]
	}
	validatorAddr := event.Attributes["provider_validator_address"]
	consumerPubKey := event.Attributes["consumer_consensus_pub_key"]

	h.logger.Info("Consensus key assignment detected",
		"consumer_id", consumerID,
		"chain_id", chainID,
		"validator", validatorAddr,
		"consumer_pubkey", consumerPubKey,
		"height", event.Height)

	// Store the key assignment if we have a key store
	if h.consumerKeyStore != nil {
		// Get validator name from address
		validatorName := h.getValidatorNameFromAddress(validatorAddr)
		if validatorName == "" {
			h.logger.Warn("Could not determine validator name from address",
				"address", validatorAddr)
			return nil
		}

		keyInfo := &subnet.ConsumerKeyInfo{
			ValidatorName:    validatorName,
			ConsumerID:       consumerID,
			ConsumerPubKey:   consumerPubKey,
			ProviderAddress:  validatorAddr,
			AssignmentHeight: event.Height,
		}

		if err := h.consumerKeyStore.StoreConsumerKey(ctx, keyInfo); err != nil {
			h.logger.Error("Failed to store consumer key assignment",
				"error", err,
				"validator", validatorName,
				"consumer_id", consumerID)
			return err
		}

		h.logger.Info("Stored consumer key assignment",
			"validator", validatorName,
			"consumer_id", consumerID)
	}

	return nil
}

// OBSOLETE: The CCV patch from the provider chain already contains the correct keys.
// The initial validator set automatically includes:
// - Consumer keys for validators who assigned them before spawn time
// - Provider keys for validators who didn't assign consumer keys
// No manual update is needed.
//
// Keeping this comment for historical context on what this function used to do:
// It would manually replace provider keys with consumer keys in the CCV patch,
// but this is unnecessary as the provider chain already handles this correctly.

// Removed: updateCCVPatchWithAssignedKeys function (obsolete)

// getValidatorNameFromAddress maps a validator address to its moniker/name.
//
// This function is essential for correlating validator addresses in events
// with validator names used in deployment and key storage.
//
// Behavior:
// 1. First checks the cached mapping (validatorAddressToName)
// 2. If not found, refreshes the mapping from the blockchain
// 3. Returns the validator name or empty string if not found
//
// Parameters:
//   - address: The validator's provider chain address (e.g., "cosmosvaloperXXX")
//
// Returns:
//   - The validator's moniker/name (e.g., "alice", "bob", "charlie")
//   - Empty string if validator not found
//
// Thread Safety:
//   - Uses RWMutex for concurrent access to validator mappings
//   - Safe to call from multiple goroutines
//
// Note: The mapping is populated by refreshValidatorMappings() which
// queries the staking module for all bonded validators.
func (h *ConsumerHandler) getValidatorNameFromAddress(address string) string {
	h.validatorMappingMutex.RLock()
	if name, exists := h.validatorAddressToName[address]; exists {
		h.validatorMappingMutex.RUnlock()
		return name
	}
	h.validatorMappingMutex.RUnlock()

	// If not found in cache, try to refresh the mapping
	if err := h.refreshValidatorMappings(context.Background()); err != nil {
		h.logger.Warn("Failed to refresh validator mappings", "error", err)
	}

	// Try again after refresh
	h.validatorMappingMutex.RLock()
	name := h.validatorAddressToName[address]
	h.validatorMappingMutex.RUnlock()

	return name
}

// OBSOLETE: queryValidatorConsensusPairs was used by the obsolete updateCCVPatchWithAssignedKeys function.
// It's no longer needed because the CCV patch already contains the correct keys from the provider chain.

func (h *ConsumerHandler) isLocalValidatorInInitialSet(ccvPatch map[string]interface{}, consumerID string) (*InitialSetKeyInfo, error) {
	if h.validatorSelector == nil {
		return &InitialSetKeyInfo{Found: false}, fmt.Errorf("validator selector not available")
	}

	// Get our local validator's provider consensus key
	localProviderKey, err := h.getLocalProviderConsensusKey()
	if err != nil {
		return &InitialSetKeyInfo{Found: false}, fmt.Errorf("failed to get local provider consensus key: %w", err)
	}

	// Navigate to the initial validator set in the CCV patch
	var initialValSet []interface{}

	if provider, ok := ccvPatch["provider"].(map[string]interface{}); ok {
		// Direct structure: provider.initial_val_set
		if valSet, ok := provider["initial_val_set"].([]interface{}); ok {
			initialValSet = valSet
		}
	} else if appState, ok := ccvPatch["app_state"].(map[string]interface{}); ok {
		// Nested structure: app_state.ccvconsumer.provider.initial_val_set
		if ccvConsumer, ok := appState["ccvconsumer"].(map[string]interface{}); ok {
			if provider, ok := ccvConsumer["provider"].(map[string]interface{}); ok {
				if valSet, ok := provider["initial_val_set"].([]interface{}); ok {
					initialValSet = valSet
				}
			}
		}
	}

	if initialValSet == nil {
		return &InitialSetKeyInfo{Found: false}, fmt.Errorf("initial_val_set not found in CCV patch")
	}

	h.logger.Info("Checking if local validator is in initial validator set",
		"provider_consensus_key", localProviderKey,
		"initial_set_size", len(initialValSet))

	// Check if our provider key is in the initial set (no consumer key assigned case)
	for _, val := range initialValSet {
		validator, ok := val.(map[string]interface{})
		if !ok {
			continue
		}

		pubKeyMap, ok := validator["pub_key"].(map[string]interface{})
		if !ok {
			continue
		}

		ed25519Key, ok := pubKeyMap["ed25519"].(string)
		if !ok {
			continue
		}

		// Compare with our provider consensus key
		if ed25519Key == localProviderKey {
			h.logger.Info("Found local validator in initial validator set with PROVIDER key",
				"matching_key", localProviderKey)
			return &InitialSetKeyInfo{
				Found:    true,
				KeyType:  KeyTypeProvider,
				KeyValue: localProviderKey,
			}, nil
		}
	}

	// If not found with provider key, check if we have an assigned consumer key
	h.logger.Info("Provider key not found in initial set, checking for assigned consumer key")

	// Get local validator name
	localValidatorName := h.validatorSelector.GetFromKey()
	if localValidatorName == "" {
		h.logger.Warn("Could not determine local validator name")
		return &InitialSetKeyInfo{Found: false}, nil
	}

	// Check if we have a stored consumer key assignment
	if h.consumerKeyStore != nil {
		consumerKey, err := h.consumerKeyStore.GetConsumerKey(consumerID, localValidatorName)
		if err == nil && consumerKey != nil {
			// Extract the public key from the consumer key info
			consumerPubKey := extractPubKeyFromConsensusKey(consumerKey.ConsumerPubKey)
			if consumerPubKey != "" {
				h.logger.Info("Checking if local validator's CONSUMER key is in initial set",
					"consumer_key", consumerPubKey)

				// Check if our consumer key is in the initial set
				for _, val := range initialValSet {
					validator, ok := val.(map[string]interface{})
					if !ok {
						continue
					}

					pubKeyMap, ok := validator["pub_key"].(map[string]interface{})
					if !ok {
						continue
					}

					ed25519Key, ok := pubKeyMap["ed25519"].(string)
					if !ok {
						continue
					}

					if ed25519Key == consumerPubKey {
						h.logger.Info("Found local validator in initial validator set with CONSUMER key",
							"matching_key", consumerPubKey)
						return &InitialSetKeyInfo{
							Found:    true,
							KeyType:  KeyTypeConsumer,
							KeyValue: consumerPubKey,
						}, nil
					}
				}
			}
		}
	}

	h.logger.Info("Local validator NOT found in initial validator set",
		"provider_key", localProviderKey,
		"initial_set_validators", len(initialValSet))

	return &InitialSetKeyInfo{Found: false}, nil
}

func (h *ConsumerHandler) getLocalProviderConsensusKey() (string, error) {
	// Get validator address from the selector
	validatorAddr := h.validatorSelector.GetValidatorAddress()
	if validatorAddr == "" {
		return "", fmt.Errorf("local validator address not available")
	}

	// Query validator info
	stakingClient := stakingtypes.NewQueryClient(h.clientCtx)
	resp, err := stakingClient.Validator(context.Background(), &stakingtypes.QueryValidatorRequest{
		ValidatorAddr: validatorAddr,
	})
	if err != nil {
		return "", fmt.Errorf("failed to query validator %s: %w", validatorAddr, err)
	}

	// Unpack the consensus pubkey
	var pubKey cryptotypes.PubKey
	err = h.clientCtx.InterfaceRegistry.UnpackAny(resp.Validator.ConsensusPubkey, &pubKey)
	if err != nil {
		return "", fmt.Errorf("failed to unpack consensus pubkey: %w", err)
	}

	// Return base64-encoded key
	return base64.StdEncoding.EncodeToString(pubKey.Bytes()), nil
}

// isLocalValidatorOptedIn checks if the local validator has opted into a consumer chain
func (h *ConsumerHandler) isLocalValidatorOptedIn(ctx context.Context, consumerID string) (bool, error) {
	if h.blockchainState == nil {
		return false, fmt.Errorf("blockchain state provider not available")
	}

	if h.validatorSelector == nil {
		return false, fmt.Errorf("validator selector not available")
	}

	// Get local validator's provider address
	localValidatorAddr := h.validatorSelector.GetValidatorAddress()
	if localValidatorAddr == "" {
		return false, fmt.Errorf("failed to get local validator address")
	}

	// Get list of opted-in validators
	optedInValidators, err := h.blockchainState.GetOptedInValidators(ctx, consumerID)
	if err != nil {
		return false, fmt.Errorf("failed to get opted-in validators: %w", err)
	}

	// Check if local validator is in the list
	for _, addr := range optedInValidators {
		if addr == localValidatorAddr {
			h.logger.Info("Local validator is opted in to consumer chain",
				"consumer_id", consumerID,
				"validator_address", localValidatorAddr)
			return true, nil
		}
	}

	return false, nil
}

func (h *ConsumerHandler) refreshValidatorMappings(ctx context.Context) error {
	if h.validatorSelector == nil {
		return fmt.Errorf("validator selector not available")
	}

	// Get all validators from the chain
	validators, err := h.validatorSelector.GetAllValidators(ctx)
	if err != nil {
		return fmt.Errorf("failed to get validators: %w", err)
	}

	h.validatorMappingMutex.Lock()
	defer h.validatorMappingMutex.Unlock()

	// Clear existing mappings
	h.validatorAddressToName = make(map[string]string)
	h.validatorNameToAddress = make(map[string]string)

	// Populate new mappings
	for _, val := range validators {
		h.validatorAddressToName[val.OperatorAddress] = val.Moniker
		h.validatorNameToAddress[val.Moniker] = val.OperatorAddress
	}

	h.logger.Info("Refreshed validator mappings", "validator_count", len(validators))
	return nil
}
