package monitor

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	cosmosed25519 "github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	sdk "github.com/cosmos/cosmos-sdk/types"
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

	// Create the priv_validator_key.json structure
	privKeyBytes := privKey.Bytes()
	pubKeyBytes := pubKey.Bytes()
	
	// Calculate the consensus address (first 20 bytes of SHA256 of pubkey)
	address := sdk.ConsAddress(pubKey.Address())
	
	privValidatorKey := map[string]interface{}{
		"address": strings.ToUpper(hex.EncodeToString(address)),
		"pub_key": map[string]interface{}{
			"type":  "tendermint/PubKeyEd25519",
			"value": base64.StdEncoding.EncodeToString(pubKeyBytes),
		},
		"priv_key": map[string]interface{}{
			"type":  "tendermint/PrivKeyEd25519",
			"value": base64.StdEncoding.EncodeToString(privKeyBytes),
		},
	}
	
	privValidatorKeyJSON, err := json.Marshal(privValidatorKey)
	if err != nil {
		return fmt.Errorf("failed to marshal priv_validator_key: %w", err)
	}

	// Store the key (will be stored in ConfigMap after successful assignment)
	keyInfo := &subnet.ConsumerKeyInfo{
		ValidatorName:        localValidatorMoniker,
		ConsumerID:           consumerID,
		ConsumerPubKey:       pubKey.String(),
		ConsumerAddress:      "", // Will be set later
		ProviderAddress:      submitterAddr,
		AssignmentHeight:     event.Height,
		PrivValidatorKeyJSON: string(privValidatorKeyJSON),
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