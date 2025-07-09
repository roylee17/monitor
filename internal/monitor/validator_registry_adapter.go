package monitor

import (
	"context"

	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/sourcenetwork/ics-operator/internal/subnet"
)

// ValidatorRegistryAdapter adapts ValidatorRegistry to subnet.ValidatorRegistryInterface
type ValidatorRegistryAdapter struct {
	registry *ValidatorRegistry
}

// NewValidatorRegistryAdapter creates a new adapter
func NewValidatorRegistryAdapter(registry *ValidatorRegistry) *ValidatorRegistryAdapter {
	return &ValidatorRegistryAdapter{
		registry: registry,
	}
}

// GetValidatorEndpoints implements subnet.ValidatorRegistryInterface
func (a *ValidatorRegistryAdapter) GetValidatorEndpoints(ctx context.Context, stakingClient stakingtypes.QueryClient) (map[string]subnet.ValidatorEndpointInfo, error) {
	endpoints, err := a.registry.GetValidatorEndpoints(ctx, stakingClient)
	if err != nil {
		return nil, err
	}

	// Convert to subnet.ValidatorEndpointInfo
	result := make(map[string]subnet.ValidatorEndpointInfo)
	for moniker, endpoint := range endpoints {
		result[moniker] = subnet.ValidatorEndpointInfo{
			Moniker: endpoint.Moniker,
			Address: endpoint.Address,
		}
	}

	return result, nil
}
