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
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/cosmos/interchain-security-monitor/internal/config"
	"github.com/cosmos/interchain-security-monitor/internal/selector"
	"github.com/cosmos/interchain-security-monitor/internal/subnet"
	"github.com/cosmos/interchain-security-monitor/internal/transaction"
)

// Service provides blockchain monitoring functionality
type Service struct {
	cfg               config.Config
	logger            *slog.Logger
	clientCtx         client.Context
	monitor           EventMonitor
	processor         *EventProcessor
	peerDiscovery     *subnet.PeerDiscovery
	validatorRegistry *ValidatorRegistry
	stakingClient     stakingtypes.QueryClient
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

	subnetManager, err := subnet.NewManager(logger, workDir, subnetdPath, "")
	if err != nil {
		return nil, fmt.Errorf("failed to create subnet manager: %w", err)
	}

	// Create K8s manager (required)
	// Since each validator has its own cluster, namespaces are just chain IDs
	
	consumerImage := cfg.ConsumerImage
	if consumerImage == "" {
		consumerImage = "ics-monitor:latest" // Default image - contains all ICS binaries
	}
	
	k8sManager, err := subnet.NewK8sManager(subnetManager, logger, consumerImage, cfg.FromKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create K8s manager: %w", err)
	}
	
	// Setup peer discovery
	peerDiscovery := subnet.NewPeerDiscovery(logger, cfg.FromKey)
	logger.Info("Peer discovery configured for LoadBalancer-based discovery")
	
	// Query validator endpoints from chain registry
	validatorRegistry := NewValidatorRegistry(logger)
	
	// Create a staking query client
	stakingQueryClient := stakingtypes.NewQueryClient(clientCtx)
	
	endpoints, err := validatorRegistry.GetValidatorEndpoints(context.Background(), stakingQueryClient)
	if err != nil {
		logger.Warn("Failed to query validator endpoints from chain", "error", err)
		// Peer discovery will fail for validators not in the on-chain registry
	} else {
		// Convert to simple moniker->address map for peer discovery
		monikerEndpoints := make(map[string]string)
		for moniker, endpoint := range endpoints {
			monikerEndpoints[moniker] = endpoint.Address
		}
		peerDiscovery.SetValidatorEndpoints(monikerEndpoints)
		logger.Info("Loaded validator endpoints from chain registry", "count", len(monikerEndpoints))
	}
	
	// Multi-cluster mode configuration
	// In multi-cluster setup, each validator runs in its own cluster
	// and peer discovery relies on the on-chain validator registry
	if os.Getenv("MULTI_CLUSTER_MODE") == "true" {
		logger.Info("Multi-cluster mode enabled - using LoadBalancer-based discovery")
	}
	
	k8sManager.SetPeerDiscovery(peerDiscovery)
	
	logger.Info("K8s manager created successfully with peer discovery", 
		"image", consumerImage)

	// Create transaction service - always use CLI-based due to SDK keyring issues
	txService := transaction.NewTxService(clientCtx)
	logger.Info("Using CLI-based transaction service")

	// Create consumer key store
	clientset, err := k8sManager.GetClientset()
	if err != nil {
		return nil, fmt.Errorf("failed to get clientset: %w", err)
	}
	consumerKeyStore := NewConsumerKeyStore(logger, clientset, "provider")

	// Create event processor with K8s-enabled handlers
	consumerHandler := NewConsumerHandlerWithK8s(logger, validatorSelector, subnetManager, k8sManager, txService, rpcClient, clientCtx, cfg.ProviderEndpoints)
	logger.Info("Using Kubernetes-enabled consumer handler", "provider_endpoints", len(cfg.ProviderEndpoints))

	handlers := []EventHandler{
		NewCCVHandler(logger, txService, consumerKeyStore, validatorSelector),
		consumerHandler,
		NewValidatorUpdateHandler(logger, peerDiscovery, validatorRegistry, stakingQueryClient),
	}

	processor := NewEventProcessor(handlers)

	return &Service{
		cfg:               cfg,
		logger:            logger,
		clientCtx:         clientCtx,
		monitor:           monitor,
		processor:         processor,
		peerDiscovery:     peerDiscovery,
		validatorRegistry: validatorRegistry,
		stakingClient:     stakingQueryClient,
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

	// Validator endpoint monitoring is now event-based via ValidatorUpdateHandler
	// No need for polling since we listen to edit_validator events

	s.logger.Info("Starting blockchain monitoring", "rpc_url", s.cfg.RPCURL)
	return s.monitor.Start(ctx, s.processor)
}

// Stop gracefully shuts down the service
func (s *Service) Stop() error {
	s.logger.Info("Stopping monitor service")
	return s.monitor.Stop()
}