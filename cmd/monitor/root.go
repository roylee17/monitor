package main

import (
	"fmt"

	rpcclient "github.com/cometbft/cometbft/rpc/client/http"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/sourcenetwork/ics-operator/internal/config"
	"github.com/sourcenetwork/ics-operator/internal/monitor"
	"github.com/sourcenetwork/ics-operator/internal/selector"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "monitor",
	Short: "A CLI tool to monitor Interchain Security events",
	Long: `monitor is a command-line interface that monitors CCV module events
and provides validator information using the Cosmos SDK and WebSocket subscriptions.`,
	Run: func(cmd *cobra.Command, args []string) {
		cmd.Help()
	},
}

var cfgFile string

func init() {
	// Initialize SDK config
	config.InitSDKConfig()

	// Initialize Viper
	cobra.OnInitialize(initConfig)

	// Add standard SDK flags
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.monitor.yaml)")

	// SDK flags will be added to individual commands as needed

	// Add subcommands with improved SDK integration
	rootCmd.AddCommand(newStartCmd())
	rootCmd.AddCommand(newSubnetCmd())

	// Add keys management command
	rootCmd.AddCommand(keysCmd())
}

// initConfig reads in config file and ENV variables if set.
func initConfig() {
	if cfgFile != "" {
		// Use config file specified by the user.
		viper.SetConfigFile(cfgFile)
	} else {
		// Search config in home directory with name ".monitor" (without extension).
		viper.AddConfigPath("$HOME")
		viper.SetConfigName(".monitor")
		viper.SetConfigType("yaml")
	}

	viper.AutomaticEnv() // read in environment variables that match

	// If a config file is found, read it in.
	if err := viper.ReadInConfig(); err == nil {
		fmt.Println("Using config file:", viper.ConfigFileUsed())
	}
}

// newStartCmd returns a Cobra command for starting the monitor service
func newStartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start monitoring CCV module events",
		Long: `Monitors Interchain Security CCV module events using WebSocket subscriptions.
Tracks consumer chain creation, validator opt-ins, and consensus key assignments.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Get client context from SDK flags
			clientCtx := client.GetClientContextFromCmd(cmd)

			// Initialize Codec and TxConfig if not present
			if clientCtx.TxConfig == nil || clientCtx.Codec == nil || clientCtx.InterfaceRegistry == nil {
				codec, txConfig, interfaceRegistry := makeEncodingConfig()
				clientCtx = clientCtx.WithCodec(codec).WithTxConfig(txConfig).WithInterfaceRegistry(interfaceRegistry)
			}

			// Handle home directory flag if provided
			homeFlag, _ := cmd.Flags().GetString(flags.FlagHome)
			if homeFlag != "" {
				clientCtx = clientCtx.WithHomeDir(homeFlag)
			}

			// Ensure home directory is set
			if clientCtx.HomeDir == "" {
				// Use default if not set
				clientCtx = clientCtx.WithHomeDir("/data/.provider")
			}

			// Read the --node flag directly from the command flags
			nodeFlag, _ := cmd.Flags().GetString("node")
			rpcURL := nodeFlag
			if rpcURL == "" {
				rpcURL = clientCtx.NodeURI
			}
			if rpcURL == "" {
				rpcURL = config.DefaultRPCURL
			}

			// Update client context with the correct node URI
			clientCtx = clientCtx.WithNodeURI(rpcURL)

			// Create RPC client manually and attach to client context
			rpcClient, err := client.NewClientFromNode(rpcURL)
			if err != nil {
				return fmt.Errorf("failed to create RPC client: %w", err)
			}

			// Update client context with proper RPC client to enable online mode
			clientCtx = clientCtx.WithClient(rpcClient)

			// Get WebSocket URL, default to RPC URL with /websocket path
			wsURL, _ := cmd.Flags().GetString("ws-url")
			// Check if ws-url flag was actually set by the user
			if !cmd.Flags().Changed("ws-url") {
				// Convert RPC URL to WebSocket URL
				wsURL = config.ConvertRPCToWebSocketURL(rpcURL)
			}

			fmt.Printf("Using RPC URL: %s\n", rpcURL)
			fmt.Printf("Using WebSocket URL: %s\n", wsURL)

			// Additional configuration removed - using K8s deployment only

			// Get chain ID from command flags and set in client context
			chainIDFlag, _ := cmd.Flags().GetString(flags.FlagChainID)
			if chainIDFlag != "" {
				clientCtx = clientCtx.WithChainID(chainIDFlag)
			}

			// Get validator key information for transactions
			fromFlag, _ := cmd.Flags().GetString(flags.FlagFrom)
			if fromFlag != "" {
				clientCtx = clientCtx.WithFromName(fromFlag)
			}

			fromKey := clientCtx.FromName
			if fromKey == "" && clientCtx.FromAddress != nil {
				fromKey = clientCtx.FromAddress.String()
			}

			// Get keyring backend from command flags
			keyringBackend, _ := cmd.Flags().GetString(flags.FlagKeyringBackend)
			if keyringBackend == "" {
				keyringBackend = "test" // Default for Docker environment
			}

			// Initialize keyring for SDK operations
			if fromKey != "" && clientCtx.HomeDir != "" {
				updatedCtx, err := monitor.InitializeKeyring(clientCtx, clientCtx.HomeDir+"/.provider", keyringBackend, fromKey)
				if err != nil {
					// Log warning but continue - CLI fallback will be used
					fmt.Printf("Warning: Failed to initialize SDK keyring: %v\n", err)
					fmt.Printf("Monitor will use CLI-based operations as fallback\n")
				} else {
					clientCtx = updatedCtx
					fmt.Printf("Successfully initialized SDK keyring\n")
				}
			}

			fmt.Printf("Using validator key: %s\n", fromKey)
			fmt.Printf("Using keyring backend: %s\n", keyringBackend)
			fmt.Printf("Using home directory: %s\n", clientCtx.HomeDir)
			fmt.Printf("Using chain ID: %s\n", clientCtx.ChainID)

			// Get K8s deployment configuration
			consumerNamespace, _ := cmd.Flags().GetString("consumer-namespace")
			consumerImage, _ := cmd.Flags().GetString("consumer-image")

			// Get provider endpoints for peer discovery
			providerEndpoints, _ := cmd.Flags().GetStringSlice("provider-endpoints")

			// Create configuration with all settings
			cfg := config.Config{
				RPCURL:         rpcURL,
				ChainID:        clientCtx.ChainID,
				WebSocketURL:   wsURL,
				FromKey:        fromKey,
				KeyringBackend: keyringBackend,
				HomeDir:        clientCtx.HomeDir,

				// K8s deployment settings
				ConsumerNamespace: consumerNamespace,
				ConsumerImage:     consumerImage,
				ProviderEndpoints: providerEndpoints,
			}

			// Create monitor service with proper client context
			monitorService, err := monitor.NewService(cfg, clientCtx)
			if err != nil {
				return fmt.Errorf("failed to create monitor service: %w", err)
			}
			return monitorService.StartMonitoring()
		},
	}

	// Add monitor-specific flags
	cmd.Flags().String("ws-url", config.DefaultWebSocketURL, "WebSocket URL for monitoring blockchain events")

	// Add Kubernetes deployment configuration
	cmd.Flags().String("consumer-namespace", "", "Kubernetes namespace for consumer chain deployments (defaults to <validator>-consumer-chains)")
	cmd.Flags().String("consumer-image", "ghcr.io/cosmos/interchain-security:v7.0.1", "Docker image to use for consumer chain deployments")
	cmd.Flags().StringSlice("provider-endpoints", []string{}, "Provider P2P endpoints for peer discovery (e.g., validator-1:26656)")

	// Bind flags to viper
	viper.BindPFlag("ws-url", cmd.Flags().Lookup("ws-url"))
	viper.BindPFlag("consumer-namespace", cmd.Flags().Lookup("consumer-namespace"))
	viper.BindPFlag("consumer-image", cmd.Flags().Lookup("consumer-image"))
	viper.BindPFlag("provider-endpoints", cmd.Flags().Lookup("provider-endpoints"))

	// Add SDK query flags for this command
	flags.AddQueryFlagsToCmd(cmd)

	// Add specific transaction flags needed for opt-in functionality
	cmd.Flags().String(flags.FlagFrom, "", "Name or address of private key with which to sign")
	cmd.Flags().String(flags.FlagKeyringBackend, flags.DefaultKeyringBackend, "Select keyring's backend (os|file|kwallet|pass|test)")
	cmd.Flags().String(flags.FlagChainID, "", "The network chain ID")
	cmd.Flags().String(flags.FlagHome, "", "directory for config and data")

	return cmd
}

// newSubnetCmd returns a Cobra command for subnet workflow testing and management
func newSubnetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "subnet",
		Short: "Subnet management and workflow testing commands",
		Long: `Commands for testing and managing the subnet deployment workflow.
Includes validator selection, lead monitor determination, and subnet lifecycle management.`,
		Run: func(cmd *cobra.Command, args []string) {
			cmd.Help()
		},
	}

	// Add subcommands
	cmd.AddCommand(newValidatorSelectCmd())

	return cmd
}

// newValidatorSelectCmd returns a command to test validator selection
func newValidatorSelectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "select [consumer-id]",
		Short: "Test validator subset selection",
		Long: `Tests the validator selection algorithm including:
- Querying current bonded validators
- Deterministic subset selection based on consumer ID
- Local validator participation detection`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			consumerID := args[0]

			// Get subset ratio flag
			subsetRatio, _ := cmd.Flags().GetFloat64("subset-ratio")

			// Get client context from SDK flags
			clientCtx := client.GetClientContextFromCmd(cmd)

			// Read the --node flag
			nodeFlag, _ := cmd.Flags().GetString("node")
			rpcURL := nodeFlag
			if rpcURL == "" {
				rpcURL = config.DefaultRPCURL
			}

			// Update client context with the correct node URI
			clientCtx = clientCtx.WithNodeURI(rpcURL)

			// Create RPC client
			rpcClient, err := client.NewClientFromNode(rpcURL)
			if err != nil {
				return fmt.Errorf("failed to create RPC client: %w", err)
			}
			clientCtx = clientCtx.WithClient(rpcClient)

			return testValidatorSelection(clientCtx, rpcURL, consumerID, subsetRatio)
		},
	}

	// Add flags
	cmd.Flags().Float64("subset-ratio", 0.66, "Ratio of validators to select for subset (0.0-1.0)")

	// Add SDK query flags
	flags.AddQueryFlagsToCmd(cmd)

	return cmd
}

// testValidatorSelection tests the validator selection functionality
func testValidatorSelection(clientCtx client.Context, rpcURL, consumerID string, subsetRatio float64) error {
	// Create RPC client for validator selector
	rpcClient, err := rpcclient.New(rpcURL, "/websocket")
	if err != nil {
		return fmt.Errorf("failed to create RPC client - blockchain connection required: %w", err)
	}

	// Create validator selector
	selector := selector.NewValidatorSelector(clientCtx, rpcClient, "")

	fmt.Printf("Consumer ID: %s\n", consumerID)
	fmt.Printf("Subset Ratio: %.1f%%\n", subsetRatio*100)
	fmt.Printf("RPC URL: %s\n\n", rpcURL)

	result, err := selector.SelectValidatorSubset(consumerID, subsetRatio)
	if err != nil {
		return fmt.Errorf("failed to select validator subset from blockchain: %w", err)
	}

	// Display results
	fmt.Printf("%s\n", selector.GetValidatorSubsetInfo(result))

	return nil
}
