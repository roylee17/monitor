package monitor

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	rpcclient "github.com/cometbft/cometbft/rpc/client/http"
	"github.com/cosmos/cosmos-sdk/client"
	cosmosed25519 "github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/query"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/cosmos/interchain-security-monitor/internal/selector"
	"github.com/cosmos/interchain-security-monitor/internal/subnet"
	"github.com/cosmos/interchain-security-monitor/internal/transaction"
)

// CCVHandler handles CCV module events
type CCVHandler struct {
	logger            *slog.Logger
	txService         transaction.Service
	consumerKeyStore  *ConsumerKeyStore
	validatorSelector *selector.ValidatorSelector
}

// NewCCVHandler creates a new CCV event handler
func NewCCVHandler(logger *slog.Logger, txService transaction.Service, consumerKeyStore *ConsumerKeyStore, validatorSelector *selector.ValidatorSelector) *CCVHandler {
	return &CCVHandler{
		logger:            logger,
		txService:         txService,
		consumerKeyStore:  consumerKeyStore,
		validatorSelector: validatorSelector,
	}
}

// CanHandle checks if this handler can process the event
func (h *CCVHandler) CanHandle(event Event) bool {
	// Handle opt_in events directly
	if event.Type == "opt_in" {
		return true
	}
	// Handle message events with CCV actions
	return strings.Contains(event.Type, "message") && h.containsCCVAction(event.Attributes)
}

// HandleEvent processes CCV module events
func (h *CCVHandler) HandleEvent(ctx context.Context, event Event) error {
	// Handle opt_in event type directly
	if event.Type == "opt_in" {
		h.logger.Info("Opt-in event detected",
			"height", event.Height,
			"consumer_id", event.Attributes["consumer_id"],
			"validator", event.Attributes["provider_validator_address"])
		return h.handleOptInEvent(ctx, event)
	}

	// Handle message events
	action := event.Attributes["action"]
	h.logger.Info("CCV Action detected", "action", action, "height", event.Height)

	switch {
	case strings.Contains(action, "MsgCreateConsumer"):
		h.logger.Info("Consumer chain creation detected")
	case strings.Contains(action, "MsgOptIn"):
		h.logger.Info("Validator opt-in detected")
		// The actual opt-in handling is done via the opt_in event above
	case strings.Contains(action, "MsgAssignConsumerKey"):
		h.logger.Info("Consensus key assignment detected")
	}

	return nil
}

// handleOptInEvent processes opt_in events with full consumer information
func (h *CCVHandler) handleOptInEvent(ctx context.Context, event Event) error {
	// Extract all relevant information from the opt_in event
	consumerID := event.Attributes["consumer_id"]
	consumerChainID := event.Attributes["consumer_chain_id"]
	providerValidatorAddr := event.Attributes["provider_validator_address"]
	submitterAddr := event.Attributes["submitter_address"]
	consumerPubKey := event.Attributes["consumer_consensus_pub_key"]

	h.logger.Info("Processing opt-in event",
		"consumer_id", consumerID,
		"chain_id", consumerChainID,
		"provider_validator", providerValidatorAddr,
		"submitter", submitterAddr,
		"has_consumer_key", consumerPubKey != "")

	// Check if this is our validator
	if h.validatorSelector == nil {
		h.logger.Warn("Validator selector not available, skipping key assignment")
		return nil
	}

	localValidatorAddr := h.validatorSelector.GetAccountAddress()
	if localValidatorAddr == "" {
		h.logger.Warn("Local validator address not available")
		return nil
	}

	h.logger.Info("Checking if opt-in is for our validator",
		"submitter", submitterAddr,
		"local_validator", localValidatorAddr,
		"provider_validator", providerValidatorAddr)

	// Compare the submitter address with our validator address
	if submitterAddr != localValidatorAddr {
		h.logger.Debug("Opt-in event for different validator, ignoring",
			"event_validator", submitterAddr,
			"local_validator", localValidatorAddr)
		return nil
	}

	// If a consumer key was already provided in the opt-in, no need to assign a new one
	if consumerPubKey != "" {
		h.logger.Info("Consumer key already provided in opt-in transaction",
			"consumer_id", consumerID,
			"pubkey", consumerPubKey)
		return nil
	}

	h.logger.Info("Our validator opted in without consumer key, triggering assignment",
		"validator", submitterAddr,
		"consumer_id", consumerID)

	// Check if we already have a consumer key assigned
	localValidatorMoniker := h.validatorSelector.GetFromKey()
	if h.consumerKeyStore != nil {
		existingKey, err := h.consumerKeyStore.GetConsumerKey(consumerID, localValidatorMoniker)
		if err == nil && existingKey != nil {
			h.logger.Info("Consumer key already assigned",
				"validator", submitterAddr,
				"consumer_id", consumerID,
				"pubkey", existingKey.ConsumerPubKey)
			return nil
		}
	}

	// Generate a new consumer key
	privKey := cosmosed25519.GenPrivKey()
	pubKey := privKey.PubKey()

	h.logger.Info("Generated new consumer key",
		"validator", submitterAddr,
		"consumer_id", consumerID,
		"pubkey", pubKey.String())

	// Store the key (will be stored in ConfigMap after successful assignment)
	keyInfo := &ConsumerKeyInfo{
		ValidatorName:    localValidatorMoniker,
		ConsumerID:       consumerID,
		ConsumerPubKey:   pubKey.String(),
		ConsumerAddress:  "", // Will be set later
		ProviderAddress:  submitterAddr,
		AssignmentHeight: event.Height,
		PrivateKey:       privKey.Bytes(), // Store the private key
	}

	// Submit the consumer key assignment transaction
	if h.txService != nil {
		// Build the assign consumer key command
		// The CLI expects JSON format: {"@type": "/cosmos.crypto.ed25519.PubKey", "key": "<base64>"}
		pubKeyBase64 := base64.StdEncoding.EncodeToString(pubKey.Bytes())
		pubKeyJSON := fmt.Sprintf(`{"@type":"/cosmos.crypto.ed25519.PubKey","key":"%s"}`, pubKeyBase64)
		cmd := []string{
			"tx", "provider", "assign-consensus-key",
			consumerID,
			pubKeyJSON,
			"--from", h.validatorSelector.GetFromKey(),
			"--yes",
			"--gas", "auto",
			"--gas-adjustment", "1.5",
		}

		h.logger.Info("Submitting consumer key assignment transaction",
			"validator", submitterAddr,
			"consumer_id", consumerID,
			"pubkey", pubKey.String(),
			"pubkey_json", pubKeyJSON)

		// Type assert to get the concrete TxService
		txService, ok := h.txService.(*transaction.TxService)
		if !ok {
			h.logger.Error("Transaction service does not support ExecuteTx")
			return fmt.Errorf("transaction service does not support ExecuteTx")
		}

		result, err := txService.ExecuteTx(ctx, cmd)
		if err != nil {
			h.logger.Error("Failed to assign consumer key",
				"error", err,
				"validator", submitterAddr,
				"consumer_id", consumerID)
			return fmt.Errorf("failed to assign consumer key: %w", err)
		}

		h.logger.Info("Consumer key assignment transaction submitted",
			"tx_hash", result.TxHash,
			"validator", submitterAddr,
			"consumer_id", consumerID)

		// Store the key in ConfigMap after successful tx
		if h.consumerKeyStore != nil {
			if err := h.consumerKeyStore.StoreConsumerKey(ctx, keyInfo); err != nil {
				h.logger.Error("Failed to store consumer key after assignment", "error", err)
			}
		}
	}

	return nil
}


// containsCCVAction checks if the message contains CCV-related actions
func (h *CCVHandler) containsCCVAction(attributes map[string]string) bool {
	action, exists := attributes["action"]
	if !exists {
		return false
	}

	ccvActions := []string{
		"MsgCreateConsumer",
		"MsgOptIn",
		"MsgAssignConsumerKey",
		"/interchain_security.ccv.provider.v1.MsgCreateConsumer",
		"/interchain_security.ccv.provider.v1.MsgOptIn",
		"/interchain_security.ccv.provider.v1.MsgAssignConsumerKey",
	}

	for _, ccvAction := range ccvActions {
		if strings.Contains(action, ccvAction) {
			return true
		}
	}
	return false
}


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
	genesisFetcher    *GenesisFetcher        // Reliable genesis fetching
	healthMonitor     *HealthMonitor         // Consumer chain health monitoring

	// Validator mappings for dynamic lookup
	validatorAddressToName map[string]string        // Maps validator operator address to moniker
	validatorNameToAddress map[string]string        // Maps moniker to validator operator address
	validatorMappingMutex  sync.RWMutex             // Protects validator mappings
	blockchainState        *BlockchainStateProvider // Blockchain-based state provider
	consumerKeyStore       *ConsumerKeyStore        // Manages consumer key assignments

	// Context storage for consumer chain information
	consumerContexts map[string]ConsumerContext // map[chainID]ConsumerContext
	consumerMutex    sync.RWMutex               // Protects consumerContexts

	// Spawn monitoring cancellation
	spawnMonitors   map[string]context.CancelFunc // map[chainID]cancelFunc
	spawnMonitorsMu sync.Mutex                    // Protects spawnMonitors
}

// ConsumerContext stores context information for a consumer chain
type ConsumerContext struct {
	ChainID                  string
	ConsumerID               string
	ClientID                 string
	CreatedAt                int64                  // Block height when created
	CCVPatch                 map[string]interface{} // CCV genesis patch data
	SpawnTime                time.Time              // When the consumer chain should spawn
	ValidatorSet             []string               // Validators participating in this consumer
	IsLocalValidatorSelected bool                   // Whether the local validator was selected for this consumer
}


// NewConsumerHandlerWithK8s creates a new consumer event handler with Kubernetes deployment support
func NewConsumerHandlerWithK8s(logger *slog.Logger, validatorSelector *selector.ValidatorSelector, subnetManager *subnet.Manager, k8sManager *subnet.K8sManager, txService transaction.Service, rpcClient *rpcclient.HTTP, clientCtx client.Context, providerEndpoints []string) *ConsumerHandler {
	// Create blockchain state provider
	blockchainState := NewBlockchainStateProvider(clientCtx, logger)

	// Create consumer key store if we have K8s manager
	var consumerKeyStore *ConsumerKeyStore
	if k8sManager != nil {
		// Get clientset from K8s manager for the key store
		clientset := k8sManager.GetClientset()
		if clientset != nil {
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
		genesisFetcher:         NewGenesisFetcher(logger, clientCtx),
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
		h.logger.Info("🚀 Consumer chain entered LAUNCHED phase!",
			"consumer_id", consumerID,
			"chain_id", chainID)

		// Check if we have context for this chain
		h.consumerMutex.RLock()
		ctx, exists := h.consumerContexts[chainID]
		h.consumerMutex.RUnlock()
		if exists {
			// Note: Direct NodePort services are created during consumer deployment
			// No additional gateway configuration needed with direct TCP exposure

			// Deploy the consumer chain
			if h.k8sManager != nil {
				h.logger.Info("Preparing to deploy consumer chain",
					"chain_id", chainID,
					"consumer_id", consumerID)

				// Get CCV genesis if not already fetched
				if ctx.CCVPatch == nil {
					genesis, err := h.fetchCCVPatch(consumerID)
					if err != nil {
						h.logger.Error("Failed to fetch CCV genesis", "error", err)
						return err
					}
					h.consumerMutex.Lock()
					if ctxToUpdate, ok := h.consumerContexts[chainID]; ok {
						ctxToUpdate.CCVPatch = genesis
						h.consumerContexts[chainID] = ctxToUpdate
						ctx = ctxToUpdate // Update the local ctx variable with the new CCV patch
					}
					h.consumerMutex.Unlock()
				}

				// Check if local validator is in the CCV genesis initial validator set
				isInInitialSet, err := h.isLocalValidatorInInitialSet(ctx.CCVPatch, consumerID)
				if err != nil {
					h.logger.Error("Failed to check if local validator is in initial set", "error", err)
					return err
				}
				
				if !isInInitialSet {
					h.logger.Info("Local validator is NOT in the CCV genesis initial validator set, skipping deployment",
						"chain_id", chainID,
						"consumer_id", consumerID,
						"local_validator", h.validatorSelector.GetFromKey())
					return nil
				}
				
				h.logger.Info("Local validator IS in the CCV genesis initial validator set, proceeding with deployment",
					"chain_id", chainID,
					"consumer_id", consumerID,
					"local_validator", h.validatorSelector.GetFromKey())

				// Update the CCV genesis patch with assigned consumer keys
				if err := h.updateCCVPatchWithAssignedKeys(ctx.CCVPatch, consumerID); err != nil {
					h.logger.Error("Failed to update CCV patch with assigned keys", "error", err)
					// Continue anyway, as this is not critical
				}

				// Check for assigned consumer key
				var consumerKey *ConsumerKeyInfo
				if h.consumerKeyStore != nil && h.validatorSelector != nil {
					// Get local validator name
					localValidatorName := h.validatorSelector.GetFromKey()
					if localValidatorName != "" {
						keyInfo, err := h.consumerKeyStore.GetConsumerKey(consumerID, localValidatorName)
						if err != nil {
							h.logger.Warn("No consumer key found for validator",
								"validator", localValidatorName,
								"consumer_id", consumerID,
								"error", err)
						} else {
							h.logger.Info("Found assigned consumer key",
								"validator", localValidatorName,
								"consumer_id", consumerID,
								"pubkey", keyInfo.ConsumerPubKey)
							consumerKey = keyInfo
						}
					}
				}

				// Deploy with dynamic peer discovery from provider chain
				// IMPORTANT: Use chainID for port calculation to match Hermes configuration
				ports, err := subnet.CalculatePorts(chainID)
				if err != nil {
					h.logger.Error("Failed to calculate ports for consumer chain",
						"error", err,
						"chain_id", chainID)
					return nil
				}

				// Query actual opted-in validators from blockchain
				if h.blockchainState == nil {
					return fmt.Errorf("blockchain state provider not available")
				}

				optedIn, err := h.blockchainState.GetOptedInValidators(context.Background(), consumerID)
				if err != nil {
					return fmt.Errorf("failed to query opted-in validators for consumer %s: %w", consumerID, err)
				}

				// Map provider consensus addresses to validator monikers
				// This is required because:
				// 1. The opted-in query returns consensus addresses (cosmosvalcons...)
				// 2. The Kubernetes infrastructure uses validator monikers for peer discovery
				
				// Option 4 Implementation: Query validator info to get monikers
				// Since SDK doesn't have direct ConsAddr query, we need to query all validators once
				stakingClient := stakingtypes.NewQueryClient(h.clientCtx)
				validatorsResp, err := stakingClient.Validators(context.Background(), &stakingtypes.QueryValidatorsRequest{
					Status: "BOND_STATUS_BONDED",
					Pagination: &query.PageRequest{
						Limit: 1000,
					},
				})
				if err != nil {
					return fmt.Errorf("failed to query validators: %w", err)
				}
				
				// Build consensus address to moniker mapping
				consAddrToMoniker := make(map[string]string)
				monikerCount := make(map[string]int) // Track moniker uniqueness
				
				for _, val := range validatorsResp.Validators {
					// The validator query returns validators with Any-encoded pubkeys
					// We need to unpack them to get the actual consensus address
					if val.ConsensusPubkey == nil {
						h.logger.Error("Validator has nil consensus pubkey",
							"operator", val.OperatorAddress)
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
						return fmt.Errorf("failed to find moniker for consensus address %s", consAddrStr)
					}
					if moniker == "" {
						return fmt.Errorf("validator moniker is empty for consensus address %s", consAddrStr)
					}
					actualOptedInValidators = append(actualOptedInValidators, moniker)
				}
				
				h.logger.Info("Successfully mapped opted-in validators",
					"consumer_id", consumerID,
					"opted_in_count", len(optedIn),
					"mapped_monikers", actualOptedInValidators)

				// Convert consumer key if available
				var subnetKeyInfo *subnet.ConsumerKeyInfo
				if consumerKey != nil {
					subnetKeyInfo = &subnet.ConsumerKeyInfo{
						ValidatorName:    consumerKey.ValidatorName,
						ConsumerID:       consumerKey.ConsumerID,
						ConsumerPubKey:   consumerKey.ConsumerPubKey,
						ConsumerAddress:  consumerKey.ConsumerAddress,
						ProviderAddress:  consumerKey.ProviderAddress,
						AssignmentHeight: consumerKey.AssignmentHeight,
						PrivateKey:       consumerKey.PrivateKey,
					}
				}

				if err := h.k8sManager.DeployConsumerWithDynamicPeersAndKeyAndValidators(
					context.Background(), chainID, consumerID, *ports,
					ctx.CCVPatch, subnetKeyInfo, actualOptedInValidators); err != nil {
					h.logger.Error("Failed to deploy consumer chain", "error", err, "has_key", consumerKey != nil)
					return err
				}

				// Start health monitoring
				go h.healthMonitor.StartMonitoring(chainID)
			}
		} else {
			h.logger.Warn("No context found for consumer chain",
				"chain_id", chainID,
				"consumer_id", consumerID)
		}

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
// 4. The stored keys are used by updateCCVPatchWithAssignedKeys during deployment
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

		keyInfo := &ConsumerKeyInfo{
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

// updateCCVPatchWithAssignedKeys updates the CCV genesis patch to replace validator public keys
// with their assigned consumer keys from the ConsumerKeyStore.
//
// CRITICAL FUNCTION: This function is essential for consumer chains to produce blocks.
// Without properly updating the validator keys, the consumer chain will have an empty
// validator set and cannot produce blocks.
//
// Behavior:
// 1. Retrieves all validators from the provider chain and builds a pubkey->name mapping
// 2. Looks up assigned consumer keys from the ConsumerKeyStore for each validator
// 3. Updates the initial_val_set in the CCV patch with the assigned consumer keys
// 4. Handles multiple key formats: JSON format and PubKeyEd25519{hex} format
//
// Parameters:
//   - ccvPatch: The CCV genesis patch containing initial_val_set to update
//   - consumerID: The consumer chain ID to look up assigned keys for
//
// Returns:
//   - error if the patch structure is invalid or key parsing fails
//
// Key Requirements:
//   - ConsumerKeyStore must be available and contain assigned keys
//   - ValidatorSelector must be available to map pubkeys to validator names
//   - The CCV patch must have the expected structure: app_state.ccvconsumer.provider.initial_val_set
//
// Common Issues:
//   - If validators haven't assigned consumer keys yet, they won't be updated
//   - Pubkey format mismatches can cause validators to not be found
//   - The function continues on errors for individual validators (non-fatal)
func (h *ConsumerHandler) updateCCVPatchWithAssignedKeys(ccvPatch map[string]interface{}, consumerID string) error {
	if h.consumerKeyStore == nil {
		h.logger.Debug("ConsumerKeyStore not available, skipping key update")
		return nil
	}

	// Navigate to the initial validator set
	// The CCV patch structure can vary depending on the query method
	// Try the direct provider structure first (query provider consumer-genesis)
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
	
	if initialValSet == nil || len(initialValSet) == 0 {
		return fmt.Errorf("initial_val_set not found or empty in CCV patch")
	}

	h.logger.Info("Updating CCV patch with assigned consumer keys",
		"consumer_id", consumerID,
		"validator_count", len(initialValSet))

	// Query the provider chain for validator consensus address pairs
	// This gives us the mapping of provider keys to assigned consumer keys
	providerToConsumerKey, err := h.queryValidatorConsensusPairs(consumerID)
	if err != nil {
		h.logger.Error("Failed to query validator consensus pairs", "error", err)
		// Fall back to using stored keys only
		providerToConsumerKey = make(map[string]string)
	}

	h.logger.Info("Queried validator consensus pairs",
		"consumer_id", consumerID,
		"mapping_count", len(providerToConsumerKey))

	// Update each validator in initial_val_set
	updatedCount := 0
	for _, val := range initialValSet {
		validator, ok := val.(map[string]interface{})
		if !ok {
			continue
		}

		// Get the current pubkey from the validator
		pubKeyMap, ok := validator["pub_key"].(map[string]interface{})
		if !ok {
			continue
		}

		// Extract the ed25519 key (this is the provider key)
		currentPubKey, ok := pubKeyMap["ed25519"].(string)
		if !ok {
			continue
		}

		// Check if we have a consumer key mapping for this provider key
		consumerKey, hasMapping := providerToConsumerKey[currentPubKey]
		if hasMapping {
			// Update the validator's public key with the assigned consumer key
			validator["pub_key"] = map[string]interface{}{
				"ed25519": consumerKey,
			}
			updatedCount++
			h.logger.Info("Updated validator with assigned consumer key from consensus pairs",
				"provider_pubkey", currentPubKey,
				"consumer_key", consumerKey,
				"consumer_id", consumerID)
		} else {
			h.logger.Debug("No consumer key mapping found for provider key",
				"provider_pubkey", currentPubKey)
		}
	}

	h.logger.Info("Completed CCV patch update with assigned keys",
		"consumer_id", consumerID,
		"updated_count", updatedCount,
		"total_validators", len(initialValSet))

	return nil
}

// isLocalValidatorInInitialSet checks if the local validator is in the CCV genesis initial validator set
func (h *ConsumerHandler) isLocalValidatorInInitialSet(ccvPatch map[string]interface{}, consumerID string) (bool, error) {
	if h.validatorSelector == nil {
		return false, fmt.Errorf("validator selector not available")
	}
	
	localValidatorName := h.validatorSelector.GetFromKey()
	if localValidatorName == "" {
		return false, fmt.Errorf("local validator name not available")
	}
	
	// Get all possible keys for our validator (both provider and consumer keys)
	var ourKeys []string
	
	// 1. Get our provider consensus public key
	stakingClient := stakingtypes.NewQueryClient(h.clientCtx)
	validatorsResp, err := stakingClient.Validators(context.Background(), &stakingtypes.QueryValidatorsRequest{
		Status: "BOND_STATUS_BONDED",
		Pagination: &query.PageRequest{
			Limit: 1000,
		},
	})
	if err != nil {
		return false, fmt.Errorf("failed to query validators: %w", err)
	}
	
	for _, val := range validatorsResp.Validators {
		if val.Description.Moniker == localValidatorName {
			// Unpack the consensus pubkey
			var pubKey cryptotypes.PubKey
			err := h.clientCtx.InterfaceRegistry.UnpackAny(val.ConsensusPubkey, &pubKey)
			if err != nil {
				return false, fmt.Errorf("failed to unpack consensus pubkey: %w", err)
			}
			
			// Add provider key to our list
			providerKey := base64.StdEncoding.EncodeToString(pubKey.Bytes())
			ourKeys = append(ourKeys, providerKey)
			h.logger.Debug("Our provider consensus key", 
				"validator", localValidatorName,
				"provider_key", providerKey)
			break
		}
	}
	
	// 2. Check if we have an assigned consumer key
	if h.consumerKeyStore != nil {
		keyInfo, err := h.consumerKeyStore.GetConsumerKey(consumerID, localValidatorName)
		if err == nil && keyInfo != nil && keyInfo.ConsumerPubKey != "" {
			// Extract the base64 key from the consumer pubkey JSON
			var pubKeyData map[string]interface{}
			if err := json.Unmarshal([]byte(keyInfo.ConsumerPubKey), &pubKeyData); err == nil {
				if consumerKey, ok := pubKeyData["key"].(string); ok {
					ourKeys = append(ourKeys, consumerKey)
					h.logger.Debug("Our assigned consumer key",
						"validator", localValidatorName,
						"consumer_key", consumerKey)
				}
			}
		}
	}
	
	if len(ourKeys) == 0 {
		return false, fmt.Errorf("could not find any keys for local validator %s", localValidatorName)
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
		return false, fmt.Errorf("initial_val_set not found in CCV patch")
	}
	
	h.logger.Info("Checking if local validator is in initial validator set",
		"local_validator", localValidatorName,
		"our_keys", ourKeys,
		"initial_set_size", len(initialValSet))
	
	// Check if any of our keys are in the initial set
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
		
		// Check against all our possible keys
		for _, ourKey := range ourKeys {
			if ed25519Key == ourKey {
				h.logger.Info("Found local validator in initial validator set",
					"validator", localValidatorName,
					"matching_key", ourKey)
				return true, nil
			}
		}
	}
	
	h.logger.Info("Local validator NOT found in initial validator set",
		"validator", localValidatorName,
		"our_keys", ourKeys,
		"initial_set_validators", len(initialValSet))
	
	return false, nil
}

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

// ValidatorUpdateHandler handles validator update events including P2P endpoint changes
type ValidatorUpdateHandler struct {
	logger            *slog.Logger
	peerDiscovery     *subnet.PeerDiscovery
	validatorRegistry *ValidatorRegistry
	stakingClient     stakingtypes.QueryClient
}

// NewValidatorUpdateHandler creates a new validator update event handler
func NewValidatorUpdateHandler(logger *slog.Logger, peerDiscovery *subnet.PeerDiscovery, validatorRegistry *ValidatorRegistry, stakingClient stakingtypes.QueryClient) *ValidatorUpdateHandler {
	return &ValidatorUpdateHandler{
		logger:            logger,
		peerDiscovery:     peerDiscovery,
		validatorRegistry: validatorRegistry,
		stakingClient:     stakingClient,
	}
}

// CanHandle checks if this handler can process the event
func (h *ValidatorUpdateHandler) CanHandle(event Event) bool {
	// Handle edit_validator events
	if strings.Contains(event.Type, "edit_validator") {
		return true
	}
	// Handle message events with validator edit actions
	if event.Type == "message" {
		action := event.Attributes["action"]
		return strings.Contains(action, "MsgEditValidator") || strings.Contains(action, "edit_validator")
	}
	return false
}

// HandleEvent processes validator update events
func (h *ValidatorUpdateHandler) HandleEvent(ctx context.Context, event Event) error {
	h.logger.Info("Validator update event detected",
		"type", event.Type,
		"height", event.Height,
		"attributes", event.Attributes)

	// Refresh validator endpoints from chain
	if h.validatorRegistry != nil && h.stakingClient != nil {
		endpoints, err := h.validatorRegistry.GetValidatorEndpoints(ctx, h.stakingClient)
		if err != nil {
			h.logger.Warn("Failed to refresh validator endpoints after update", "error", err)
			return nil // Don't fail the event processing
		}

		// Convert to simple moniker->address map for peer discovery
		monikerEndpoints := make(map[string]string)
		var changes []string
		
		// Get current endpoints for comparison
		currentEndpoints := h.peerDiscovery.GetValidatorEndpoints()
		
		for moniker, endpoint := range endpoints {
			newAddr := endpoint.Address
			monikerEndpoints[moniker] = newAddr
			
			// Check if this is a new or updated endpoint
			if oldAddr, exists := currentEndpoints[moniker]; !exists {
				changes = append(changes, fmt.Sprintf("%s: new endpoint %s", moniker, newAddr))
			} else if oldAddr != newAddr {
				changes = append(changes, fmt.Sprintf("%s: %s -> %s", moniker, oldAddr, newAddr))
			}
		}
		
		// Check for removed endpoints
		for moniker := range currentEndpoints {
			if _, exists := monikerEndpoints[moniker]; !exists {
				changes = append(changes, fmt.Sprintf("%s: endpoint removed", moniker))
			}
		}
		
		// Update peer discovery with new endpoints
		h.peerDiscovery.SetValidatorEndpoints(monikerEndpoints)
		
		if len(changes) > 0 {
			h.logger.Info("Validator endpoints updated after edit_validator event",
				"total_endpoints", len(monikerEndpoints),
				"changes", len(changes),
				"details", changes)
		} else {
			h.logger.Debug("No changes in validator endpoints after edit_validator event")
		}
	}

	return nil
}

// queryValidatorConsensusPairs queries the provider chain for validator consensus address pairs
// This returns the mapping between provider validators and their assigned consumer keys
func (h *ConsumerHandler) queryValidatorConsensusPairs(consumerID string) (map[string]string, error) {
	// Get the RPC endpoint from the client
	rpcEndpoint := h.rpcClient.Remote()
	h.logger.Info("Querying validator consensus pairs",
		"consumer_id", consumerID,
		"rpc_endpoint", rpcEndpoint)
	
	// Query the provider chain directly using the chain binary
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	
	cmd := exec.CommandContext(ctx, "interchain-security-pd", "query", "provider", 
		"all-pairs-valconsensus-address", consumerID,
		"--node", rpcEndpoint,
		"--output", "json",
		"--home", "/tmp")
	
	h.logger.Debug("Executing query command for validator pairs",
		"cmd", cmd.String())
	
	output, err := cmd.CombinedOutput()
	if err != nil {
		h.logger.Error("Failed to query validator pairs",
			"error", err,
			"output", string(output))
		return nil, fmt.Errorf("failed to query validator pairs: %w, output: %s", err, string(output))
	}
	
	h.logger.Debug("Successfully queried validator pairs",
		"output_size", len(output))

	// Parse the response
	var pairsResponse struct {
		PairValConAddr []struct {
			ProviderAddress string `json:"provider_address"`
			ConsumerAddress string `json:"consumer_address"`
			ConsumerKey     struct {
				Ed25519 string `json:"ed25519"`
			} `json:"consumer_key"`
		} `json:"pair_val_con_addr"`
	}

	if err := json.Unmarshal(output, &pairsResponse); err != nil {
		return nil, fmt.Errorf("failed to parse validator pairs response: %w", err)
	}

	// Build a map of provider pubkeys to consumer keys
	// We need to map provider pubkeys (from initial_val_set) to consumer keys
	providerPubkeyToConsumerKey := make(map[string]string)
	
	// Query the validators to get their pubkeys and build the mapping
	h.logger.Info("Querying validators for pubkey mapping")
	
	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel2()
	
	validatorsCmd := exec.CommandContext(ctx2, "interchain-security-pd", "query", "staking", "validators",
		"--node", rpcEndpoint,
		"--output", "json",
		"--home", "/tmp")
	
	h.logger.Debug("Executing validators query command",
		"cmd", validatorsCmd.String())
	
	validatorsOutput, err := validatorsCmd.CombinedOutput()
	if err != nil {
		h.logger.Error("Failed to query validators",
			"error", err,
			"output", string(validatorsOutput))
		return nil, fmt.Errorf("failed to query validators: %w, output: %s", err, string(validatorsOutput))
	}

	var validatorsResponse struct {
		Validators []struct {
			OperatorAddress string `json:"operator_address"`
			ConsensusPubkey struct {
				Type  string `json:"type"`
				Value string `json:"value"`
			} `json:"consensus_pubkey"`
		} `json:"validators"`
	}
	
	if err := json.Unmarshal(validatorsOutput, &validatorsResponse); err != nil {
		return nil, fmt.Errorf("failed to parse validators response: %w", err)
	}
	
	// Create a map of consensus address to provider pubkey
	// We need to convert operator addresses to consensus addresses
	consensusAddrToPubkey := make(map[string]string)
	for _, val := range validatorsResponse.Validators {
		// The consensus pubkey is what we need to match against the initial_val_set
		consensusAddrToPubkey[val.OperatorAddress] = val.ConsensusPubkey.Value
	}
	
	// Build a map from provider consensus address to pubkey
	addrToPubkey := make(map[string]string)
	for _, val := range validatorsResponse.Validators {
		// Convert operator address to consensus address
		// We need to decode the consensus pubkey to get the address
		pubkeyBytes, err := base64.StdEncoding.DecodeString(val.ConsensusPubkey.Value)
		if err != nil {
			h.logger.Warn("Failed to decode validator pubkey", "error", err)
			continue
		}
		
		// Calculate the consensus address from the pubkey
		// This is SHA256(pubkey)[:20]
		hash := sha256.Sum256(pubkeyBytes)
		consensusAddr := sdk.ConsAddress(hash[:20])
		
		// Store the mapping
		addrToPubkey[consensusAddr.String()] = val.ConsensusPubkey.Value
	}
	
	// Now map the provider pubkeys to consumer keys using the pairs response
	for _, pair := range pairsResponse.PairValConAddr {
		if pair.ConsumerKey.Ed25519 != "" && pair.ProviderAddress != "" {
			// Look up the provider pubkey for this consensus address
			if providerPubkey, ok := addrToPubkey[pair.ProviderAddress]; ok {
				providerPubkeyToConsumerKey[providerPubkey] = pair.ConsumerKey.Ed25519
				h.logger.Debug("Mapped provider pubkey to consumer key",
					"provider_addr", pair.ProviderAddress,
					"provider_pubkey", providerPubkey,
					"consumer_key", pair.ConsumerKey.Ed25519)
			}
		}
	}
	
	h.logger.Info("Built provider to consumer key mapping",
		"consumer_id", consumerID,
		"mapping_count", len(providerPubkeyToConsumerKey))
	
	return providerPubkeyToConsumerKey, nil
}

// extractPubKeyFromConsensusKey extracts the base64 pubkey from consensus key string.
//
// This helper function handles multiple formats of consensus keys that may be
// returned by the blockchain:
//
// Format 1: JSON format - {"@type":"/cosmos.crypto.ed25519.PubKey","key":"base64key"}
// Format 2: Tendermint format - PubKeyEd25519{hexkey}
// Format 3: Direct base64 string (less common)
//
// Parameters:
//   - consensusKey: The consensus key string in any supported format
//
// Returns:
//   - The base64-encoded public key, or empty string if parsing fails
//
// Note: This function is critical for matching validators between provider
// and consumer chains, as key formats may vary between different sources.
func extractPubKeyFromConsensusKey(consensusKey string) string {
	// Handle different formats of consensus keys
	// Format 1: {"@type":"/cosmos.crypto.ed25519.PubKey","key":"base64key"}
	if strings.Contains(consensusKey, "@type") {
		var keyData map[string]interface{}
		if err := json.Unmarshal([]byte(consensusKey), &keyData); err == nil {
			if key, ok := keyData["key"].(string); ok {
				return key
			}
		}
	}

	// Format 2: PubKeyEd25519{hexkey}
	if strings.HasPrefix(consensusKey, "PubKeyEd25519{") && strings.HasSuffix(consensusKey, "}") {
		hexStr := consensusKey[14 : len(consensusKey)-1]
		if pubKeyBytes, err := hex.DecodeString(hexStr); err == nil {
			return base64.StdEncoding.EncodeToString(pubKeyBytes)
		}
	}

	// Format 3: Direct base64 (less common)
	if _, err := base64.StdEncoding.DecodeString(consensusKey); err == nil {
		return consensusKey
	}

	return ""
}

// refreshValidatorMappings updates the validator address/name mappings from the chain
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
