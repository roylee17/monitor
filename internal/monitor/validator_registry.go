package monitor

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
)

// ValidatorRegistry manages validator P2P endpoint information
type ValidatorRegistry struct {
	logger *slog.Logger
}

// NewValidatorRegistry creates a new validator registry
func NewValidatorRegistry(logger *slog.Logger) *ValidatorRegistry {
	return &ValidatorRegistry{
		logger: logger,
	}
}

// ValidatorEndpoint represents a validator's P2P endpoint information
type ValidatorEndpoint struct {
	Moniker string
	Address string // The base address (IP or hostname) without port
}

// GetValidatorEndpoints queries all validators and extracts their P2P endpoints
func (r *ValidatorRegistry) GetValidatorEndpoints(ctx context.Context, stakingClient stakingtypes.QueryClient) (map[string]ValidatorEndpoint, error) {
	// Query all validators
	resp, err := stakingClient.Validators(ctx, &stakingtypes.QueryValidatorsRequest{
		Status: stakingtypes.BondStatusBonded,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query validators: %w", err)
	}

	endpoints := make(map[string]ValidatorEndpoint)

	for _, validator := range resp.Validators {
		// Extract P2P endpoint from validator description
		endpoint := r.extractP2PEndpoint(validator.Description)
		if endpoint == "" {
			r.logger.Warn("Validator missing P2P endpoint in description",
				"moniker", validator.Description.Moniker,
				"operator", validator.OperatorAddress)
			continue
		}

		// Use moniker as the key for easier lookup
		endpoints[validator.Description.Moniker] = ValidatorEndpoint{
			Moniker: validator.Description.Moniker,
			Address: endpoint,
		}

		r.logger.Info("Found validator P2P endpoint",
			"moniker", validator.Description.Moniker,
			"endpoint", endpoint)
	}

	return endpoints, nil
}

// extractP2PEndpoint extracts the P2P endpoint from validator description
// Expected format: "p2p=host:port" or "p2p=host" in the details field
func (r *ValidatorRegistry) extractP2PEndpoint(desc stakingtypes.Description) string {
	// Look for p2p= in details (can be anywhere in the line)
	lines := strings.Split(desc.Details, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// Check if line contains p2p= (not just starts with)
		if idx := strings.Index(line, "p2p="); idx != -1 {
			// Extract everything after p2p=
			endpoint := line[idx+4:] // Skip "p2p="
			endpoint = strings.TrimSpace(endpoint)

			// Remove port if present (we'll use calculated ports)
			if idx := strings.LastIndex(endpoint, ":"); idx != -1 {
				endpoint = endpoint[:idx]
			}

			return endpoint
		}
	}

	// Also check in a JSON-like format
	if strings.Contains(desc.Details, `"p2p":`) {
		// Simple extraction for "p2p":"value" format
		start := strings.Index(desc.Details, `"p2p":"`) + 7
		if start > 6 {
			end := strings.Index(desc.Details[start:], `"`)
			if end > 0 {
				endpoint := desc.Details[start : start+end]

				// Remove port if present
				if idx := strings.LastIndex(endpoint, ":"); idx != -1 {
					endpoint = endpoint[:idx]
				}

				return endpoint
			}
		}
	}

	return ""
}

// GetValidatorEndpoint gets a specific validator's endpoint by consensus address
func (r *ValidatorRegistry) GetValidatorEndpoint(ctx context.Context, stakingClient stakingtypes.QueryClient, consAddr string) (ValidatorEndpoint, error) {
	endpoints, err := r.GetValidatorEndpoints(ctx, stakingClient)
	if err != nil {
		return ValidatorEndpoint{}, err
	}

	endpoint, ok := endpoints[consAddr]
	if !ok {
		return ValidatorEndpoint{}, fmt.Errorf("no endpoint found for validator %s", consAddr)
	}

	return endpoint, nil
}
