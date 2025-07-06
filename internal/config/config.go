package config

import (
	"fmt"
	"strings"

	"github.com/cosmos/cosmos-sdk/client"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/spf13/viper"
)

// Default configuration constants
const (
	// Default RPC URL for CometBFT
	DefaultRPCURL = "tcp://localhost:26657"

	// Default WebSocket URL for event monitoring
	DefaultWebSocketURL = "ws://localhost:26657/websocket"

	// Default chain ID for provider chain
	DefaultChainID = "provider"

	// Default bech32 prefixes for Cosmos chains
	DefaultAccountPrefix      = "cosmos"
	DefaultValidatorPrefix    = "cosmosvaloper"
	DefaultConsensusPrefix    = "cosmosvalcons"
	DefaultAccountPubPrefix   = "cosmospub"
	DefaultValidatorPubPrefix = "cosmosvaloperpub"
	DefaultConsensusPubPrefix = "cosmosvalconspub"
)

// Config holds configuration for monitoring services
type Config struct {
	ChainID        string
	FromKey        string
	KeyringBackend string
	HomeDir        string
	RPCURL         string
	WebSocketURL   string
	// Kubernetes deployment options
	ConsumerNamespace    string // Kubernetes namespace for consumer chains
	ConsumerImage        string // Docker image for consumer chains
	
	// Provider endpoints for peer discovery
	ProviderEndpoints    []string // List of provider P2P endpoints
	
	// Consumer chain management
	AutoUpdateConsumers  bool   // Enable automatic consumer chain updates on validator endpoint changes
	HybridPeerUpdates    bool   // Enable hybrid RPC+restart peer updates
}

// Validate validates the configuration
func (c Config) Validate() error {
	if c.RPCURL == "" {
		return fmt.Errorf("RPCURL is required")
	}
	return nil
}

// DefaultConfig returns a configuration with sensible defaults
func DefaultConfig() Config {
	// Use node URL for RPC if rpc-url is not set
	rpcURL := viper.GetString("rpc-url")
	if rpcURL == "" {
		rpcURL = viper.GetString("node")
	}
	if rpcURL == "" {
		rpcURL = DefaultRPCURL
	}

	// Use default chain ID if not specified
	chainID := viper.GetString("chain-id")
	if chainID == "" {
		chainID = DefaultChainID
	}

	// Use default WebSocket URL if not specified
	wsURL := viper.GetString("ws-url")
	if wsURL == "" {
		wsURL = ConvertRPCToWebSocketURL(rpcURL)
	}

	return Config{
		ChainID:        chainID,
		FromKey:        viper.GetString("from"),
		KeyringBackend: viper.GetString("keyring-backend"),
		HomeDir:        viper.GetString("home"),
		RPCURL:         rpcURL,
		WebSocketURL:   wsURL,
		// Kubernetes deployment settings
		ConsumerNamespace: viper.GetString("consumer-namespace"),
		ConsumerImage:     viper.GetString("consumer-image"),
		// Consumer chain management
		AutoUpdateConsumers: viper.GetBool("auto-update-consumers"),
		HybridPeerUpdates:   viper.GetBool("hybrid-peer-updates"),
	}
}

// ConvertRPCToWebSocketURL converts an RPC URL to a WebSocket URL
func ConvertRPCToWebSocketURL(rpcURL string) string {
	if strings.HasPrefix(rpcURL, "tcp://") {
		return "ws://" + rpcURL[6:] + "/websocket"
	} else if strings.HasPrefix(rpcURL, "http://") {
		return "ws://" + rpcURL[7:] + "/websocket"
	} else if strings.HasPrefix(rpcURL, "https://") {
		return "wss://" + rpcURL[8:] + "/websocket"
	}
	return DefaultWebSocketURL
}

// InitSDKConfig initializes the SDK configuration with default bech32 prefixes
func InitSDKConfig() {
	sdkConfig := sdk.GetConfig()
	sdkConfig.SetBech32PrefixForAccount(DefaultAccountPrefix, DefaultAccountPubPrefix)
	sdkConfig.SetBech32PrefixForValidator(DefaultValidatorPrefix, DefaultValidatorPubPrefix)
	sdkConfig.SetBech32PrefixForConsensusNode(DefaultConsensusPrefix, DefaultConsensusPubPrefix)
	sdkConfig.Seal()
}

// ToClientContext converts the config to a Cosmos SDK client context
func (c Config) ToClientContext() (client.Context, error) {
	// Note: SDK keyring initialization is disabled due to known issues
	// The monitor uses CLI-based transaction service instead
	
	// Create client context without keyring
	clientCtx := client.Context{}.
		WithNodeURI(c.RPCURL).
		WithChainID(c.ChainID).
		WithHomeDir(c.HomeDir).
		WithFromName(c.FromKey)

	return clientCtx, nil
}
