package monitor

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/sourcenetwork/ics-operator/internal/subnet"
)

// ValidatorUpdateHandler handles validator update events including P2P endpoint changes
type ValidatorUpdateHandler struct {
	logger            *slog.Logger
	peerDiscovery     *subnet.PeerDiscovery
	validatorRegistry *ValidatorRegistry
	stakingClient     stakingtypes.QueryClient
	consumerRegistry  *ConsumerRegistry
	chainUpdater      *ConsumerChainUpdater
	autoUpdate        bool // Feature flag to enable/disable automatic updates
}

// NewValidatorUpdateHandler creates a new validator update event handler
func NewValidatorUpdateHandler(logger *slog.Logger, peerDiscovery *subnet.PeerDiscovery, validatorRegistry *ValidatorRegistry, stakingClient stakingtypes.QueryClient) *ValidatorUpdateHandler {
	return &ValidatorUpdateHandler{
		logger:            logger,
		peerDiscovery:     peerDiscovery,
		validatorRegistry: validatorRegistry,
		stakingClient:     stakingClient,
		autoUpdate:        false, // Disabled by default for safety
	}
}

// SetConsumerRegistry sets the consumer registry for tracking validator-consumer mappings
func (h *ValidatorUpdateHandler) SetConsumerRegistry(registry *ConsumerRegistry) {
	h.consumerRegistry = registry
}

// SetChainUpdater sets the consumer chain updater for automatic updates
func (h *ValidatorUpdateHandler) SetChainUpdater(updater *ConsumerChainUpdater) {
	h.chainUpdater = updater
}

// EnableAutoUpdate enables automatic consumer chain updates
func (h *ValidatorUpdateHandler) EnableAutoUpdate(enabled bool) {
	h.autoUpdate = enabled
	if enabled {
		h.logger.Info("Automatic consumer chain updates ENABLED")
	} else {
		h.logger.Info("Automatic consumer chain updates DISABLED")
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

			// Check if automatic updates are enabled
			if h.autoUpdate && h.consumerRegistry != nil && h.chainUpdater != nil {
				h.logger.Info("Triggering automatic consumer chain updates")

				// Build list of updated validators
				var updatedValidators []UpdatedValidator
				for moniker, newAddr := range monikerEndpoints {
					if oldAddr, exists := currentEndpoints[moniker]; exists && oldAddr != newAddr {
						updatedValidators = append(updatedValidators, UpdatedValidator{
							Name:        moniker,
							OldEndpoint: oldAddr,
							NewEndpoint: newAddr,
						})
					}
				}

				// Trigger updates in background to avoid blocking event processing
				go func() {
					if err := h.chainUpdater.UpdateConsumerChainsForValidators(ctx, updatedValidators); err != nil {
						h.logger.Error("Failed to update consumer chains",
							"error", err)
					} else {
						h.logger.Info("Successfully updated consumer chains for validator endpoint changes")
					}
				}()
			} else {
				// Log warning for manual intervention
				h.logger.Warn("Validator endpoint changes detected - consumer chains may need manual update",
					"affected_validators", len(changes),
					"auto_update_enabled", h.autoUpdate,
					"has_consumer_registry", h.consumerRegistry != nil,
					"has_chain_updater", h.chainUpdater != nil,
					"note", "Consumer chains using these validators may need their peer configurations updated")
			}
		} else {
			h.logger.Debug("No changes in validator endpoints after edit_validator event")
		}
	}

	return nil
}
