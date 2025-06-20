package monitor

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	providertypes "github.com/cosmos/interchain-security/v7/x/ccv/provider/types"
	"github.com/cosmos/cosmos-sdk/client"
)

// ConsumerInfo represents consumer chain information from blockchain
type ConsumerInfo struct {
	ConsumerID   string
	ChainID      string
	Phase        string
	SpawnTime    time.Time
	OwnerAddress string
	ClientID     string
}

// BlockchainStateProvider queries consumer state from the blockchain
type BlockchainStateProvider struct {
	clientCtx     client.Context
	logger        *slog.Logger
	cache         map[string]*ConsumerInfo
	cacheTime     map[string]time.Time
	cacheMutex    sync.RWMutex
	cacheDuration time.Duration
}

// NewBlockchainStateProvider creates a new blockchain-based state provider
func NewBlockchainStateProvider(clientCtx client.Context, logger *slog.Logger) *BlockchainStateProvider {
	return &BlockchainStateProvider{
		clientCtx:     clientCtx,
		logger:        logger,
		cache:         make(map[string]*ConsumerInfo),
		cacheTime:     make(map[string]time.Time),
		cacheDuration: 30 * time.Second, // Match the polling interval for consistency
	}
}

// GetAllConsumers queries all consumer chains from the blockchain
func (b *BlockchainStateProvider) GetAllConsumers(ctx context.Context) ([]*ConsumerInfo, error) {
	b.logger.Debug("Querying all consumer chains from blockchain")

	queryClient := providertypes.NewQueryClient(b.clientCtx)
	
	res, err := queryClient.QueryConsumerChains(ctx, &providertypes.QueryConsumerChainsRequest{
		Pagination: nil,
		Phase:      providertypes.CONSUMER_PHASE_UNSPECIFIED,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query consumer chains: %w", err)
	}

	// Get detailed info for each consumer
	consumers := make([]*ConsumerInfo, 0, len(res.Chains))
	b.logger.Info("Processing consumer chains from query response", "count", len(res.Chains))
	
	for _, chain := range res.Chains {
		b.logger.Info("Getting details for consumer",
			"consumer_id", chain.ConsumerId,
			"chain_id", chain.ChainId,
			"phase", chain.Phase)
			
		info, err := b.GetConsumerInfo(ctx, chain.ConsumerId)
		if err != nil {
			b.logger.Warn("Failed to get consumer details", 
				"consumer_id", chain.ConsumerId, 
				"error", err)
			continue
		}
		consumers = append(consumers, info)
	}

	return consumers, nil
}

// GetConsumerInfo queries detailed information for a specific consumer
func (b *BlockchainStateProvider) GetConsumerInfo(ctx context.Context, consumerID string) (*ConsumerInfo, error) {
	// Check cache first
	b.cacheMutex.RLock()
	if cached, ok := b.cache[consumerID]; ok {
		if time.Since(b.cacheTime[consumerID]) < b.cacheDuration {
			b.cacheMutex.RUnlock()
			b.logger.Debug("Using cached consumer info", "consumer_id", consumerID)
			return cached, nil
		}
	}
	b.cacheMutex.RUnlock()

	// Query blockchain
	b.logger.Debug("Querying consumer info from blockchain", "consumer_id", consumerID)

	queryClient := providertypes.NewQueryClient(b.clientCtx)
	
	res, err := queryClient.QueryConsumerChain(ctx, &providertypes.QueryConsumerChainRequest{
		ConsumerId: consumerID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query consumer chain %s: %w", consumerID, err)
	}

	// Parse spawn time
	var spawnTime time.Time
	if res.InitParams != nil && !res.InitParams.SpawnTime.IsZero() {
		spawnTime = res.InitParams.SpawnTime
	}

	info := &ConsumerInfo{
		ConsumerID:   res.ConsumerId,
		ChainID:      res.ChainId,
		Phase:        res.Phase,
		SpawnTime:    spawnTime,
		OwnerAddress: res.OwnerAddress,
		ClientID:     res.ClientId,
	}

	// Update cache
	b.cacheMutex.Lock()
	b.cache[consumerID] = info
	b.cacheTime[consumerID] = time.Now()
	b.cacheMutex.Unlock()

	return info, nil
}

// GetConsumersByPhase returns all consumers in a specific phase
func (b *BlockchainStateProvider) GetConsumersByPhase(ctx context.Context, phase string) ([]*ConsumerInfo, error) {
	allConsumers, err := b.GetAllConsumers(ctx)
	if err != nil {
		return nil, err
	}

	filtered := make([]*ConsumerInfo, 0)
	for _, consumer := range allConsumers {
		if consumer.Phase == phase {
			filtered = append(filtered, consumer)
		}
	}

	return filtered, nil
}

// GetOptedInValidators queries validators that have opted into a consumer chain
func (b *BlockchainStateProvider) GetOptedInValidators(ctx context.Context, consumerID string) ([]string, error) {
	queryClient := providertypes.NewQueryClient(b.clientCtx)
	
	res, err := queryClient.QueryConsumerChainOptedInValidators(ctx, &providertypes.QueryConsumerChainOptedInValidatorsRequest{
		ConsumerId: consumerID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query opted-in validators: %w", err)
	}

	validators := make([]string, 0, len(res.ValidatorsProviderAddresses))
	for _, addr := range res.ValidatorsProviderAddresses {
		validators = append(validators, addr)
	}

	return validators, nil
}

// GetConsumerGenesis queries the consumer genesis state via RPC
func (b *BlockchainStateProvider) GetConsumerGenesis(ctx context.Context, consumerID string) ([]byte, error) {
	b.logger.Debug("Querying consumer genesis via RPC", "consumer_id", consumerID)
	
	queryClient := providertypes.NewQueryClient(b.clientCtx)
	
	res, err := queryClient.QueryConsumerGenesis(ctx, &providertypes.QueryConsumerGenesisRequest{
		ConsumerId: consumerID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query consumer genesis: %w", err)
	}
	
	// Check if response is not nil
	if res == nil {
		return nil, fmt.Errorf("received nil response")
	}
	
	// Check if codec is available
	if b.clientCtx.Codec == nil {
		return nil, fmt.Errorf("codec not available in client context")
	}
	
	// Marshal the genesis state to JSON (use pointer to genesis state)
	genesisJSON, err := b.clientCtx.Codec.MarshalJSON(&res.GenesisState)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal genesis state: %w", err)
	}
	
	b.logger.Info("Successfully retrieved consumer genesis via RPC", 
		"consumer_id", consumerID,
		"genesis_size", len(genesisJSON))
	
	return genesisJSON, nil
}

// ClearCache clears the internal cache
func (b *BlockchainStateProvider) ClearCache() {
	b.cacheMutex.Lock()
	defer b.cacheMutex.Unlock()

	b.cache = make(map[string]*ConsumerInfo)
	b.cacheTime = make(map[string]time.Time)
}

// StartPeriodicSync starts a goroutine that periodically syncs with blockchain
func (b *BlockchainStateProvider) StartPeriodicSync(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Clear cache to force fresh queries
				b.ClearCache()
				
				// Pre-fetch all consumers to warm cache
				_, err := b.GetAllConsumers(ctx)
				if err != nil {
					b.logger.Error("Failed to sync consumers", "error", err)
				}
			}
		}
	}()
}

