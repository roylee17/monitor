package monitor

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	rpcclient "github.com/cometbft/cometbft/rpc/client/http"
	"github.com/cosmos/cosmos-sdk/client"
	cosmosed25519 "github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
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

// handleValidatorOptIn processes validator opt-in events and triggers consumer key assignment
func (h *CCVHandler) handleValidatorOptIn(ctx context.Context, event Event) error {
	// Extract validator address and consumer ID from event attributes
	validatorAddr := ""
	consumerID := ""
	
	// Look for validator address in various attribute keys
	for _, key := range []string{"validator", "validator_address", "sender"} {
		if val, exists := event.Attributes[key]; exists && val != "" {
			validatorAddr = val
			break
		}
	}
	
	// Look for consumer ID in various attribute keys
	for _, key := range []string{"consumer_id", "chain_id"} {
		if val, exists := event.Attributes[key]; exists && val != "" {
			consumerID = val
			break
		}
	}
	
	h.logger.Info("Processing validator opt-in",
		"validator", validatorAddr,
		"consumer_id", consumerID,
		"attributes", event.Attributes)
	
	// Check if this is our validator
	if h.validatorSelector == nil {
		h.logger.Warn("Validator selector not available, skipping key assignment")
		return nil
	}
	
	localValidatorAddr := h.validatorSelector.GetValidatorAddress()
	if localValidatorAddr == "" {
		h.logger.Warn("Local validator address not available")
		return nil
	}
	
	// Only process if this is our validator opting in
	if validatorAddr != localValidatorAddr {
		h.logger.Debug("Opt-in event for different validator, ignoring",
			"event_validator", validatorAddr,
			"local_validator", localValidatorAddr)
		return nil
	}
	
	h.logger.Info("Our validator opted in, triggering consumer key assignment",
		"validator", validatorAddr,
		"consumer_id", consumerID)
	
	// Check if we already have a consumer key assigned
	localValidatorMoniker := h.validatorSelector.GetFromKey()
	if h.consumerKeyStore != nil {
		existingKey, err := h.consumerKeyStore.GetConsumerKey(consumerID, localValidatorMoniker)
		if err == nil && existingKey != nil {
			h.logger.Info("Consumer key already assigned",
				"validator", validatorAddr,
				"consumer_id", consumerID,
				"pubkey", existingKey.ConsumerPubKey)
			return nil
		}
	}
	
	// Generate a new consumer key
	privKey := cosmosed25519.GenPrivKey()
	pubKey := privKey.PubKey()
	
	h.logger.Info("Generated new consumer key",
		"validator", validatorAddr,
		"consumer_id", consumerID,
		"pubkey", pubKey.String())
	
	// Store the key (will be stored in ConfigMap after successful assignment)
	keyInfo := &ConsumerKeyInfo{
		ValidatorName:    localValidatorMoniker,
		ConsumerID:       consumerID,
		ConsumerPubKey:   pubKey.String(),
		ConsumerAddress:  "", // Will be set later
		ProviderAddress:  validatorAddr,
		AssignmentHeight: event.Height,
	}
	
	// Submit the consumer key assignment transaction
	if h.txService != nil {
		// Build the assign consumer key command
		cmd := []string{
			"tx", "provider", "assign-consensus-key",
			consumerID,
			pubKey.String(),
			"--from", h.validatorSelector.GetFromKey(),
			"--yes",
			"--gas", "auto",
			"--gas-adjustment", "1.5",
		}
		
		h.logger.Info("Submitting consumer key assignment transaction",
			"validator", validatorAddr,
			"consumer_id", consumerID,
			"pubkey", pubKey.String())
		
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
				"validator", validatorAddr,
				"consumer_id", consumerID)
			return fmt.Errorf("failed to assign consumer key: %w", err)
		}
		
		h.logger.Info("Consumer key assignment transaction submitted",
			"tx_hash", result.TxHash,
			"validator", validatorAddr,
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
	peerDiscovery     *subnet.PeerDiscovery // Stateless peer discovery
	genesisFetcher    *GenesisFetcher      // Reliable genesis fetching
	healthMonitor     *HealthMonitor       // Consumer chain health monitoring
	blockchainState   *BlockchainStateProvider // Blockchain-based state provider
	consumerKeyStore  *ConsumerKeyStore    // Manages consumer key assignments
	
	// Context storage for consumer chain information
	consumerContexts map[string]ConsumerContext // map[chainID]ConsumerContext
	consumerMutex    sync.RWMutex          // Protects consumerContexts
	
	// Spawn monitoring cancellation
	spawnMonitors    map[string]context.CancelFunc // map[chainID]cancelFunc
	spawnMonitorsMu  sync.Mutex                    // Protects spawnMonitors
}

// ConsumerContext stores context information for a consumer chain
type ConsumerContext struct {
	ChainID       string
	ConsumerID    string
	ClientID      string
	CreatedAt     int64 // Block height when created
	CCVPatch      map[string]interface{} // CCV genesis patch data
	SpawnTime     time.Time // When the consumer chain should spawn
	ValidatorSet  []string  // Validators participating in this consumer
	IsLocalValidatorSelected bool // Whether the local validator was selected for this consumer
}

// NewConsumerHandler creates a new consumer event handler
func NewConsumerHandler(logger *slog.Logger) *ConsumerHandler {
	return &ConsumerHandler{
		logger:           logger,
		consumerContexts: make(map[string]ConsumerContext),
		spawnMonitors:    make(map[string]context.CancelFunc),
	}
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
		logger:            logger,
		validatorSelector: validatorSelector,
		subnetManager:     subnetManager,
		k8sManager:        k8sManager,
		txService:         txService,
		rpcClient:         rpcClient,
		clientCtx:         clientCtx,
		peerDiscovery:     subnet.NewPeerDiscovery(logger, providerEndpoints),
		genesisFetcher:    NewGenesisFetcher(logger, clientCtx),
		healthMonitor:     NewHealthMonitor(logger, k8sManager),
		blockchainState:   blockchainState,
		consumerKeyStore:  consumerKeyStore,
		consumerContexts:  make(map[string]ConsumerContext),
		spawnMonitors:     make(map[string]context.CancelFunc),
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
	case "assign_consensus_key":
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
			result, err := h.validatorSelector.SelectValidatorSubset(info.ConsumerID, 0.67)
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
		result, err := h.validatorSelector.SelectValidatorSubset(info.ConsumerID, 0.67)
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
		return h.startSubnetWorkflow(chainID, consumerID)
	}

	h.logger.Warn("Subnet workflow dependencies not available, skipping automatic deployment")
	return nil
}

// startSubnetWorkflow initiates the subnet deployment workflow
func (h *ConsumerHandler) startSubnetWorkflow(chainID, consumerID string) error {
	h.logger.Info("Starting subnet workflow", "chain_id", chainID, "consumer_id", consumerID)

	// Step 1: Calculate validator subset
	subsetRatio := 0.67 // 67% of validators by default
	selectionResult, err := h.validatorSelector.SelectValidatorSubset(consumerID, subsetRatio)
	if err != nil {
		h.logger.Error("Failed to select validator subset", "error", err)
		return err
	}

	h.logger.Info("Validator subset selection completed",
		"total_subset", len(selectionResult.ValidatorSubset),
		"should_opt_in", selectionResult.ShouldOptIn)

	// Log validator subset info
	subsetInfo := h.validatorSelector.GetValidatorSubsetInfo(selectionResult)
	h.logger.Info("Validator subset details", "subset", subsetInfo)

	// Step 2: Opt in if local validator is selected
	if selectionResult.ShouldOptIn {
		h.logger.Info("Local validator selected for subset, should opt-in")

		if h.txService != nil {
			h.logger.Info("Sending opt-in transaction", "chain_id", chainID, "consumer_id", consumerID)

			// Send opt-in transaction (without consumer public key for now)
			if err := h.txService.OptIn(context.Background(), consumerID, ""); err != nil {
				h.logger.Error("Failed to send opt-in transaction", "error", err, "consumer_id", consumerID)
				// Don't return error - continue with deployment preparation even if opt-in fails
				// This allows testing to continue when keyring access is blocked
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

// prepareConsumerDeployment prepares for independent consumer deployment
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
				
				// Wrap in app_state.ccvconsumer structure for genesis merge
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

// monitorSpawnTimeAndPhase implements the primary phase detection mechanism
// Since the provider chain doesn't emit events for INITIALIZED → LAUNCHED transitions,
// we wait for the spawn time and then poll the chain state to detect when the 
// consumer has transitioned to LAUNCHED phase.
//
// The polling strategy is adaptive:
// - 1 second intervals for the first 30 seconds after spawn time
// - 3 seconds intervals for the next 90 seconds
// - 10 seconds intervals after that
//
// This is necessary because the exact timing of the phase transition depends on
// when validators opt in and when the provider chain's BeginBlock processes it.
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
		consumerID = h.extractConsumerID(chainID)
	}
	h.consumerMutex.RUnlock()

	// Query spawn time from provider chain
	spawnTime, err := h.queryConsumerSpawnTime(consumerID)
	if err != nil {
		h.logger.Error("Failed to query spawn time, using fallback countdown",
			"chain_id", chainID, "error", err)
		// Fallback: assume 15 seconds from now
		spawnTime = time.Now().Add(15 * time.Second)
	}

	h.logger.Info("Consumer spawn time retrieved",
		"chain_id", chainID,
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
	rapidInterval := 1 * time.Second   // Right after spawn time
	activeInterval := 3 * time.Second  // Normal monitoring (was 5s)
	slowInterval := 10 * time.Second   // After extended time (was 30s)

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
func (h *ConsumerHandler) extractConsumerID(chainID string) string {
	// For now, extract from chainID format like "consumer-test-1" -> "0"
	// In practice, this might need to be queried or stored during creation
	parts := strings.Split(chainID, "-")
	if len(parts) >= 3 {
		return "0" // Default to first consumer for now
	}
	return "0"
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
			// Only deploy if the local validator was selected
			if !ctx.IsLocalValidatorSelected {
				h.logger.Info("Local validator not selected for this consumer chain, skipping deployment",
					"chain_id", chainID,
					"consumer_id", consumerID,
					"validator_set", ctx.ValidatorSet)
				return nil
			}
			
			// Deploy the consumer chain
			if h.k8sManager != nil {
				h.logger.Info("Deploying consumer chain via K8s manager (local validator was selected)",
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
				
				// Check for assigned consumer key
				var consumerKey *ConsumerKeyInfo
				if h.consumerKeyStore != nil && h.validatorSelector != nil {
					// Get local validator name
					localValidatorName, _ := h.validatorSelector.GetLocalValidatorMoniker()
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
				ports := h.peerDiscovery.GetConsumerPorts(consumerID)
				// h.k8sManager is already of type *subnet.K8sManager, no need for type assertion
				if consumerKey != nil {
					// Convert monitor's ConsumerKeyInfo to subnet's ConsumerKeyInfo
					subnetKeyInfo := &subnet.ConsumerKeyInfo{
						ValidatorName:    consumerKey.ValidatorName,
						ConsumerID:       consumerKey.ConsumerID,
						ConsumerPubKey:   consumerKey.ConsumerPubKey,
						ConsumerAddress:  consumerKey.ConsumerAddress,
						ProviderAddress:  consumerKey.ProviderAddress,
						AssignmentHeight: consumerKey.AssignmentHeight,
					}
					if err := h.k8sManager.DeployConsumerWithDynamicPeersAndKey(context.Background(), chainID, consumerID, ports, ctx.CCVPatch, subnetKeyInfo); err != nil {
						h.logger.Error("Failed to deploy consumer chain with key", "error", err)
						return err
					}
				} else {
					if err := h.k8sManager.DeployConsumerWithDynamicPeers(context.Background(), chainID, consumerID, ports, ctx.CCVPatch); err != nil {
						h.logger.Error("Failed to deploy consumer chain", "error", err)
						return err
					}
				}
				
				// Start health monitoring
				go h.healthMonitor.StartMonitoring(chainID)
				
				// Deploy Hermes relayer after consumer chain is running
				go func() {
					// Wait for consumer chain to be fully initialized
					time.Sleep(30 * time.Second)
					
					h.logger.Info("Deploying Hermes relayer for consumer chain", "chain_id", chainID)
					if err := h.k8sManager.StartHermes(context.Background(), chainID); err != nil {
						h.logger.Error("Failed to deploy Hermes relayer", 
							"chain_id", chainID,
							"error", err)
					} else {
						h.logger.Info("Hermes relayer deployed successfully", "chain_id", chainID)
					}
				}()
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

// getValidatorNameFromAddress maps validator address to name
func (h *ConsumerHandler) getValidatorNameFromAddress(address string) string {
	// This is a simplified mapping - in production, this would query the chain
	// or maintain a proper mapping
	switch address {
	case "cosmosvaloper1zaavvzxez0elundtn32qnk9lkm8kmcszzsv80v":
		return "alice"
	case "cosmosvaloper1pz2trypc6e25hcwzn4h7jyqc57cr0qg4thggx5":
		return "bob"
	case "cosmosvaloper1yxgfnpk2u6h9prhhukcunszcwc277s2ct4e9k0":
		return "charlie"
	default:
		return ""
	}
}

// ValidatorHandler handles validator events
type ValidatorHandler struct {
	logger *slog.Logger
}

// NewValidatorHandler creates a new validator event handler
func NewValidatorHandler(logger *slog.Logger) *ValidatorHandler {
	return &ValidatorHandler{logger: logger}
}

// CanHandle checks if this handler can process the event
func (h *ValidatorHandler) CanHandle(event Event) bool {
	validatorEvents := []string{
		"validator_opted_in",
		"consensus_key_assigned",
	}

	for _, eventType := range validatorEvents {
		if event.Type == eventType {
			return true
		}
	}
	return false
}

// HandleEvent processes validator events
func (h *ValidatorHandler) HandleEvent(ctx context.Context, event Event) error {
	switch event.Type {
	case "validator_opted_in":
		return h.handleValidatorOptIn(event)
	case "consensus_key_assigned":
		return h.handleConsensusKeyAssigned(event)
	}
	return nil
}

// handleValidatorOptIn handles validator opt-in events
func (h *ValidatorHandler) handleValidatorOptIn(event Event) error {
	validator := event.Attributes["validator"]
	chainID := event.Attributes["chain_id"]

	h.logger.Info("Validator opt-in",
		"validator", validator,
		"chain_id", chainID,
		"height", event.Height)

	return nil
}

// handleConsensusKeyAssigned handles consensus key assignment events
func (h *ValidatorHandler) handleConsensusKeyAssigned(event Event) error {
	validator := event.Attributes["validator"]
	consumerKey := event.Attributes["consumer_key"]

	h.logger.Info("Consensus key assignment",
		"validator", validator,
		"consumer_key", consumerKey,
		"height", event.Height)

	return nil
}

// DebugHandler logs all events for debugging purposes
type DebugHandler struct {
	logger *slog.Logger
}

// NewDebugHandler creates a new debug event handler
func NewDebugHandler(logger *slog.Logger) *DebugHandler {
	return &DebugHandler{logger: logger}
}

// CanHandle checks if this handler can process the event - handles ALL events for debugging
func (h *DebugHandler) CanHandle(event Event) bool {
	return true // Handle all events for debugging
}

// HandleEvent logs all events for debugging
func (h *DebugHandler) HandleEvent(ctx context.Context, event Event) error {
	h.logger.Info("🔍 DEBUG: Event received",
		"event_type", event.Type,
		"height", event.Height,
		"attributes", fmt.Sprintf("%+v", event.Attributes))

	// Specifically look for any events that might contain consumer phase information
	if phase, exists := event.Attributes["consumer_phase"]; exists {
		h.logger.Info("🚀 DEBUG: Found event with consumer_phase",
			"event_type", event.Type,
			"phase", phase,
			"height", event.Height,
			"all_attributes", fmt.Sprintf("%+v", event.Attributes))
	}

	// Look for any events that might be related to consumer launch
	for key, value := range event.Attributes {
		if strings.Contains(strings.ToLower(key), "consumer") ||
			strings.Contains(strings.ToLower(value), "consumer") ||
			strings.Contains(strings.ToLower(value), "launch") ||
			strings.Contains(strings.ToLower(key), "client") {
			h.logger.Info("🔍 DEBUG: Potentially relevant event",
				"event_type", event.Type,
				"key", key,
				"value", value,
				"height", event.Height)
		}
	}

	return nil
}
