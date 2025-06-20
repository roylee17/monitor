package monitor

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/cometbft/cometbft/crypto/tmhash"
	rpcclient "github.com/cometbft/cometbft/rpc/client/http"
	coretypes "github.com/cometbft/cometbft/rpc/core/types"
	"github.com/cometbft/cometbft/types"
)

// WebSocketMonitor implements EventMonitor using WebSocket subscriptions
type WebSocketMonitor struct {
	rpcURL    string
	rpcClient *rpcclient.HTTP
	logger    *slog.Logger
}

// NewWebSocketMonitor creates a new WebSocket-based event monitor
func NewWebSocketMonitor(wsURL string, logger *slog.Logger) (*WebSocketMonitor, error) {
	// The rpcclient.New expects a TCP URL, not a WebSocket URL
	// Convert ws:// to tcp:// for the client
	baseURL := wsURL
	if strings.HasPrefix(baseURL, "ws://") {
		baseURL = "tcp://" + baseURL[5:]
	} else if strings.HasPrefix(baseURL, "wss://") {
		baseURL = "tcp://" + baseURL[6:]
	}
	
	// Remove any trailing /websocket as rpcclient will add it
	baseURL = strings.TrimSuffix(baseURL, "/websocket")
	
	logger.Info("Creating RPC client", "input_url", wsURL, "tcp_url", baseURL)
	
	rpcClient, err := rpcclient.New(baseURL, "/websocket")
	if err != nil {
		return nil, fmt.Errorf("failed to create RPC client: %w", err)
	}

	return &WebSocketMonitor{
		rpcURL:    baseURL,
		rpcClient: rpcClient,
		logger:    logger,
	}, nil
}

// Start begins monitoring blockchain events
func (m *WebSocketMonitor) Start(ctx context.Context, processor *EventProcessor) error {
	m.logger.Info("Starting RPC client", "url", m.rpcURL)
	if err := m.rpcClient.Start(); err != nil {
		return fmt.Errorf("failed to start RPC client: %w", err)
	}
	defer m.rpcClient.Stop()

	m.logger.Info("RPC client started", "is_running", m.rpcClient.IsRunning())

	// Test connection
	status, err := m.rpcClient.Status(ctx)
	if err != nil {
		m.logger.Error("Failed to get node status", "error", err)
	} else {
		m.logger.Info("Connected to node", 
			"chain_id", status.NodeInfo.Network,
			"latest_block", status.SyncInfo.LatestBlockHeight)
	}

	subscriber := "interchain-security-monitor"

	// Subscribe to transaction events
	txQuery := "tm.event='Tx'"
	m.logger.Info("Subscribing to transaction events", "query", txQuery)
	txCh, err := m.rpcClient.Subscribe(ctx, subscriber, txQuery)
	if err != nil {
		return fmt.Errorf("failed to subscribe to transaction events: %w", err)
	}
	defer m.rpcClient.UnsubscribeAll(ctx, subscriber)

	// Subscribe to new block events for health monitoring
	blockQuery := "tm.event='NewBlock'"
	m.logger.Info("Subscribing to block events", "query", blockQuery)
	blockCh, err := m.rpcClient.Subscribe(ctx, subscriber+"_blocks", blockQuery)
	if err != nil {
		m.logger.Warn("Failed to subscribe to block events", "error", err)
	}

	m.logger.Info("Successfully subscribed to blockchain events")

	// Add periodic status check
	statusTicker := time.NewTicker(30 * time.Second)
	defer statusTicker.Stop()

	eventCount := 0
	blockCount := 0

	for {
		select {
		case event := <-txCh:
			eventCount++
			m.logger.Info("Received transaction event", "count", eventCount)
			if err := m.handleTxEvent(ctx, event, processor); err != nil {
				m.logger.Error("Error handling transaction event", "error", err)
			}

		case blockEvent := <-blockCh:
			blockCount++
			if blockCount%10 == 0 { // Log every 10 blocks
				m.logger.Info("Received block events", "count", blockCount)
			}
			m.handleBlockEvent(blockEvent)

		case <-statusTicker.C:
			m.logger.Info("WebSocket monitor status", 
				"tx_events", eventCount,
				"block_events", blockCount,
				"connected", m.rpcClient.IsRunning())

		case <-ctx.Done():
			m.logger.Info("Context cancelled, shutting down")
			return ctx.Err()
		}
	}
}

// Stop stops the monitor
func (m *WebSocketMonitor) Stop() error {
	if m.rpcClient != nil {
		return m.rpcClient.Stop()
	}
	return nil
}

// handleTxEvent processes transaction events for CCV-related messages
func (m *WebSocketMonitor) handleTxEvent(ctx context.Context, event coretypes.ResultEvent, processor *EventProcessor) error {
	if event.Data == nil {
		return nil
	}

	eventData, ok := event.Data.(types.EventDataTx)
	if !ok {
		return nil
	}

	// Skip failed transactions
	if eventData.Result.Code != 0 {
		return nil
	}

	// Process each event in the transaction
	m.logger.Info("Processing transaction", 
		"height", eventData.Height,
		"num_events", len(eventData.Result.Events))
		
	for _, txEvent := range eventData.Result.Events {
		// Enhanced logging to debug missing events
		attrs := make(map[string]string)
		for _, attr := range txEvent.Attributes {
			attrs[attr.Key] = attr.Value
		}
		
		// Log ALL events to understand what's being emitted
		m.logger.Info("Transaction event detected",
			"type", txEvent.Type,
			"height", eventData.Height,
			"attributes", attrs)

		// Special logging for consumer-related events
		if strings.Contains(txEvent.Type, "consumer") || strings.Contains(txEvent.Type, "update") {
			m.logger.Info("🔍 Consumer-related event found!",
				"type", txEvent.Type,
				"attributes", attrs)
		}

		if m.isRelevantEvent(txEvent.Type) {
			m.logger.Info("Processing relevant event", "type", txEvent.Type)
			txHash := fmt.Sprintf("%X", tmhash.Sum(eventData.Tx))
			event := m.convertToEvent(txEvent, eventData.Height, txHash)
			if err := processor.Process(ctx, event); err != nil {
				return err
			}
		}
	}

	return nil
}

// handleBlockEvent handles new block events for health monitoring
func (m *WebSocketMonitor) handleBlockEvent(event coretypes.ResultEvent) {
	if event.Data == nil {
		return
	}

	if blockData, ok := event.Data.(types.EventDataNewBlock); ok {
		height := blockData.Block.Height
		if height%100 == 0 { // Log every 100 blocks to avoid spam
			m.logger.Debug("New block", "height", height)
		}
	}
}

// isRelevantEvent checks if an event type is relevant for monitoring
func (m *WebSocketMonitor) isRelevantEvent(eventType string) bool {
	// Only monitor events that actually exist and provide value
	relevantEvents := []string{
		"message",           // For transaction messages
		"create_consumer",   // When consumer is created
		"update_consumer",   // When consumer parameters are updated
		"remove_consumer",   // When consumer removal is initiated
		"opt_in",           // Validator opt-in events
		"opt_out",          // Validator opt-out events
	}

	for _, relevant := range relevantEvents {
		if strings.Contains(eventType, relevant) {
			return true
		}
	}

	return false
}

// convertToEvent converts a blockchain event to our internal Event type
func (m *WebSocketMonitor) convertToEvent(txEvent abci.Event, height int64, txHash string) Event {
	attributes := make(map[string]string)
	for _, attr := range txEvent.Attributes {
		attributes[attr.Key] = attr.Value
	}

	return Event{
		Type:       txEvent.Type,
		Attributes: attributes,
		Height:     height,
		TxHash:     txHash,
		Timestamp:  time.Now(),
	}
}
