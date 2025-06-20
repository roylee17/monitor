package monitor

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	rpcclient "github.com/cometbft/cometbft/rpc/client/http"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/interchain-security-monitor/internal/config"
	"github.com/cosmos/interchain-security-monitor/internal/selector"
	"github.com/cosmos/interchain-security-monitor/internal/subnet"
	"github.com/cosmos/interchain-security-monitor/internal/transaction"
)

// Service provides blockchain monitoring functionality
type Service struct {
	cfg       config.Config
	logger    *slog.Logger
	clientCtx client.Context
	monitor   EventMonitor
	processor *EventProcessor
}

// NewService creates a new monitor service
func NewService(cfg config.Config, clientCtx client.Context) (*Service, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	// Update client context with configuration values
	if cfg.FromKey != "" {
		clientCtx = clientCtx.WithFromName(cfg.FromKey)
	}
	if cfg.ChainID != "" {
		clientCtx = clientCtx.WithChainID(cfg.ChainID)
	}
	if cfg.HomeDir != "" {
		clientCtx = clientCtx.WithHomeDir(cfg.HomeDir)
	}

	// Use the client context as-is
	logger.Info("Client context configuration",
		"from_name", clientCtx.FromName,
		"chain_id", clientCtx.ChainID,
		"home_dir", clientCtx.HomeDir)

	// Create WebSocket monitor using the WebSocket URL (not RPC URL)
	monitor, err := NewWebSocketMonitor(cfg.WebSocketURL, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create monitor: %w", err)
	}

	// Create RPC client for validator queries and subnet management
	rpcClient, err := rpcclient.New(cfg.RPCURL, "/websocket")
	if err != nil {
		return nil, fmt.Errorf("failed to create RPC client: %w", err)
	}

	// Create validator selector
	validatorSelector := selector.NewValidatorSelector(clientCtx, rpcClient, cfg.FromKey)

	// Create subnet manager with default paths (K8s deployment only)
	workDir := "."
	subnetdPath := "/usr/local/bin/interchain-security-cd"
	hermesPath := "/usr/local/bin/hermes"

	subnetManager, err := subnet.NewManager(logger, workDir, subnetdPath, hermesPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create subnet manager: %w", err)
	}

	// Create K8s manager (required)
	// Note: consumerNamespace is now a prefix - actual namespaces will be created per consumer chain
	// Format: <prefix>-<chain-id>
	// Example: alice-testchain5-0, bob-consumer-0-1234
	consumerNamespace := cfg.ConsumerNamespace
	if consumerNamespace == "" {
		// Use validator name as namespace prefix
		// In production, each validator would have their own cluster
		if cfg.FromKey != "" {
			consumerNamespace = cfg.FromKey
		} else {
			consumerNamespace = "consumer" // Default namespace prefix
		}
	}
	
	consumerImage := cfg.ConsumerImage
	if consumerImage == "" {
		consumerImage = "ics-monitor:latest" // Default image - contains all ICS binaries
	}
	
	k8sManager, err := subnet.NewK8sManager(subnetManager, logger, consumerNamespace, consumerImage, cfg.FromKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create K8s manager: %w", err)
	}
	
	// Setup deterministic peer discovery
	deterministicPeerDiscovery := subnet.NewDeterministicPeerDiscovery(logger, cfg.FromKey)
	k8sManager.SetDeterministicPeerDiscovery(deterministicPeerDiscovery)
	
	logger.Info("K8s manager created successfully with deterministic peer discovery", 
		"namespace", consumerNamespace, 
		"image", consumerImage)

	// Create transaction service - always use CLI-based due to SDK keyring issues
	txService := transaction.NewTxService(clientCtx)
	logger.Info("Using CLI-based transaction service")

	// Create consumer key store
	consumerKeyStore := NewConsumerKeyStore(logger, k8sManager.GetClientset(), "provider")

	// Create event processor with K8s-enabled handlers
	consumerHandler := NewConsumerHandlerWithK8s(logger, validatorSelector, subnetManager, k8sManager, txService, rpcClient, clientCtx, cfg.ProviderEndpoints)
	logger.Info("Using Kubernetes-enabled consumer handler", "provider_endpoints", len(cfg.ProviderEndpoints))

	handlers := []EventHandler{
		NewDebugHandler(logger), // Add debug handler first to log ALL events
		NewCCVHandler(logger, txService, consumerKeyStore, validatorSelector),
		consumerHandler,
		NewValidatorHandler(logger),
	}

	processor := NewEventProcessor(handlers)

	return &Service{
		cfg:       cfg,
		logger:    logger,
		clientCtx: clientCtx,
		monitor:   monitor,
		processor: processor,
	}, nil
}

// StartMonitoring starts the monitoring service
func (s *Service) StartMonitoring() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle interrupts gracefully
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		s.logger.Info("Shutdown signal received")
		cancel()
	}()

	s.logger.Info("Starting blockchain monitoring", "rpc_url", s.cfg.RPCURL)
	return s.monitor.Start(ctx, s.processor)
}

// Stop gracefully shuts down the service
func (s *Service) Stop() error {
	s.logger.Info("Stopping monitor service")
	return s.monitor.Stop()
}
