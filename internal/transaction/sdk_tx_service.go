// DEPRECATED: This SDK-based transaction service is deprecated due to keyring initialization issues
// in containerized environments. The SDK keyring requires interactive prompts that don't work
// properly in Docker/Kubernetes environments. Use the CLI-based transaction service instead.
package transaction

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/interchain-security-monitor/internal/sdkclient"
)

// SDKTxService handles transactions using the native SDK client
// DEPRECATED: Use CLI-based transaction service instead
type SDKTxService struct {
	sdkClient *sdkclient.Client
	logger    *slog.Logger
}

// NewSDKTxService creates a new SDK-based transaction service
// DEPRECATED: This function is deprecated. Use NewTxService for CLI-based transactions instead.
func NewSDKTxService(clientCtx client.Context, logger *slog.Logger) (*SDKTxService, error) {
	// Create SDK client
	sdkClient, err := sdkclient.NewClient(clientCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to create SDK client: %w", err)
	}

	return &SDKTxService{
		sdkClient: sdkClient,
		logger:    logger,
	}, nil
}

// OptIn sends an opt-in transaction for a validator to join a consumer chain
func (ts *SDKTxService) OptIn(ctx context.Context, consumerID string, consumerPubKey string) error {
	ts.logger.Info("Sending opt-in transaction via SDK",
		"consumer_id", consumerID,
		"consumer_pub_key", consumerPubKey)

	err := ts.sdkClient.OptIn(ctx, consumerID, consumerPubKey)
	if err != nil {
		return fmt.Errorf("failed to opt-in via SDK: %w", err)
	}

	ts.logger.Info("Successfully sent opt-in transaction",
		"consumer_id", consumerID)

	return nil
}

// CreateConsumer creates a new consumer chain
func (ts *SDKTxService) CreateConsumer(ctx context.Context, chainID string) error {
	ts.logger.Info("Creating consumer chain via SDK",
		"chain_id", chainID)

	err := ts.sdkClient.CreateTestConsumer(ctx, chainID)
	if err != nil {
		return fmt.Errorf("failed to create consumer via SDK: %w", err)
	}

	ts.logger.Info("Successfully created consumer chain",
		"chain_id", chainID)

	return nil
}

// AssignConsumerKey assigns a consensus key for a consumer chain
func (ts *SDKTxService) AssignConsumerKey(ctx context.Context, consumerID string, consumerKey string) error {
	ts.logger.Info("Assigning consumer key via SDK",
		"consumer_id", consumerID,
		"consumer_key", consumerKey)

	err := ts.sdkClient.AssignConsumerKey(ctx, consumerID, consumerKey)
	if err != nil {
		return fmt.Errorf("failed to assign consumer key via SDK: %w", err)
	}

	ts.logger.Info("Successfully assigned consumer key",
		"consumer_id", consumerID)

	return nil
}

// QueryConsumerChains queries the list of consumer chains
func (ts *SDKTxService) QueryConsumerChains(ctx context.Context) ([]ConsumerChain, error) {
	res, err := ts.sdkClient.QueryConsumerChains(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to query consumer chains: %w", err)
	}

	var chains []ConsumerChain
	for _, chain := range res.Chains {
		chains = append(chains, ConsumerChain{
			ConsumerID: chain.ConsumerId,
			ChainID:    chain.ChainId,
			Phase:      chain.Phase,
		})
	}

	return chains, nil
}

// ConsumerChain represents a consumer chain
type ConsumerChain struct {
	ConsumerID string
	ChainID    string
	Phase      string
}