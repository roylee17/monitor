package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/cosmos/cosmos-sdk/client"
)

// GenesisFetcher handles reliable fetching of consumer genesis
type GenesisFetcher struct {
	logger     *slog.Logger
	clientCtx  client.Context
	maxRetries int
	retryDelay time.Duration
}

// NewGenesisFetcher creates a new genesis fetcher
func NewGenesisFetcher(logger *slog.Logger, clientCtx client.Context) *GenesisFetcher {
	return &GenesisFetcher{
		logger:     logger,
		clientCtx:  clientCtx,
		maxRetries: 5,
		retryDelay: 2 * time.Second,
	}
}

// FetchConsumerGenesis fetches consumer genesis with retry logic
func (gf *GenesisFetcher) FetchConsumerGenesis(consumerID string) (map[string]interface{}, error) {
	gf.logger.Info("Fetching consumer genesis", "consumer_id", consumerID)

	var lastErr error
	for attempt := 0; attempt < gf.maxRetries; attempt++ {
		if attempt > 0 {
			gf.logger.Debug("Retrying genesis fetch",
				"attempt", attempt+1,
				"consumer_id", consumerID,
				"delay", gf.retryDelay)
			time.Sleep(gf.retryDelay)
		}

		// Query consumer genesis via ABCI query
		// For ICS v7, the query path uses the store directly
		queryPath := "store/provider/key"
		// Build the key for consumer genesis: consumerGenesis/consumerId
		key := fmt.Sprintf("consumerGenesis/%s", consumerID)
		reqData := []byte(key)

		resp, err := gf.clientCtx.Client.ABCIQuery(
			context.Background(),
			queryPath,
			reqData,
		)

		if err == nil && resp.Response.Code == 0 && len(resp.Response.Value) > 0 {
			var result map[string]interface{}
			if err := json.Unmarshal(resp.Response.Value, &result); err == nil {
				gf.logger.Info("Successfully fetched consumer genesis",
					"consumer_id", consumerID,
					"attempt", attempt+1)
				return result, nil
			}
		}

		// Log more details about the failure
		if resp.Response.Code != 0 {
			gf.logger.Warn("ABCI query returned error",
				"code", resp.Response.Code,
				"log", resp.Response.Log,
				"path", queryPath)
		}

		lastErr = err
		gf.logger.Warn("Failed to fetch consumer genesis",
			"consumer_id", consumerID,
			"attempt", attempt+1,
			"error", err)
	}

	return nil, fmt.Errorf("failed to fetch consumer genesis after %d attempts: %w", gf.maxRetries, lastErr)
}
