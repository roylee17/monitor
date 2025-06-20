package main

import (
	"fmt"
	
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/interchain-security-monitor/internal/config"
	"github.com/cosmos/interchain-security-monitor/internal/sdkclient"
	"github.com/spf13/cobra"
)

// newCreateConsumerCmd returns a command to create a consumer chain using SDK
func newCreateConsumerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create-consumer [chain-id]",
		Short: "Create a consumer chain using the SDK client",
		Long:  `Creates a test consumer chain using the native SDK client instead of the CLI.`,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			chainID := args[0]

			// Get client context from SDK flags
			clientCtx := client.GetClientContextFromCmd(cmd)
			
			// Initialize TxConfig if not present
			if clientCtx.TxConfig == nil {
				codec, txConfig := makeEncodingConfig()
				clientCtx = clientCtx.WithCodec(codec).WithTxConfig(txConfig)
			}

			// Handle home directory flag if provided
			homeFlag, _ := cmd.Flags().GetString(flags.FlagHome)
			if homeFlag != "" {
				clientCtx = clientCtx.WithHomeDir(homeFlag)
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

			// Get keyring backend from command flags
			keyringBackend, _ := cmd.Flags().GetString(flags.FlagKeyringBackend)
			if keyringBackend == "" {
				keyringBackend = "test" // Default for Docker environment
			}

			// Initialize keyring if not already present
			if clientCtx.Keyring == nil {
				keyring, err := client.NewKeyringFromBackend(clientCtx, keyringBackend)
				if err != nil {
					return fmt.Errorf("failed to create keyring: %w", err)
				}
				clientCtx = clientCtx.WithKeyring(keyring)
			}

			fmt.Printf("Creating consumer chain: %s\n", chainID)
			fmt.Printf("Using RPC URL: %s\n", rpcURL)
			fmt.Printf("Using chain ID: %s\n", clientCtx.ChainID)
			fmt.Printf("Using from: %s\n", clientCtx.FromName)

			// Create SDK client
			sdkClient, err := sdkclient.NewClient(clientCtx)
			if err != nil {
				return fmt.Errorf("failed to create SDK client: %w", err)
			}

			// Create test consumer
			ctx := cmd.Context()
			err = sdkClient.CreateTestConsumer(ctx, chainID)
			if err != nil {
				return fmt.Errorf("failed to create consumer: %w", err)
			}

			fmt.Printf("✅ Successfully created consumer chain: %s\n", chainID)
			return nil
		},
	}

	// Add SDK transaction flags
	flags.AddTxFlagsToCmd(cmd)

	return cmd
}

// newQueryConsumersCmd returns a command to query consumer chains using SDK
func newQueryConsumersCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "query-consumers",
		Short: "Query consumer chains using the SDK client",
		Long:  `Queries all consumer chains using the native SDK client.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Get client context from SDK flags
			clientCtx := client.GetClientContextFromCmd(cmd)
			
			// Initialize TxConfig if not present
			if clientCtx.TxConfig == nil {
				codec, txConfig := makeEncodingConfig()
				clientCtx = clientCtx.WithCodec(codec).WithTxConfig(txConfig)
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

			// Create SDK client
			sdkClient, err := sdkclient.NewClient(clientCtx)
			if err != nil {
				return fmt.Errorf("failed to create SDK client: %w", err)
			}

			// Query consumer chains
			ctx := cmd.Context()
			res, err := sdkClient.QueryConsumerChains(ctx)
			if err != nil {
				return fmt.Errorf("failed to query consumer chains: %w", err)
			}

			fmt.Printf("Consumer Chains (%d):\n", len(res.Chains))
			fmt.Printf("===================\n")
			for _, chain := range res.Chains {
				fmt.Printf("Consumer ID: %s\n", chain.ConsumerId)
				fmt.Printf("Chain ID: %s\n", chain.ChainId)
				fmt.Printf("Phase: %s\n", chain.Phase)
				fmt.Printf("---\n")
			}

			return nil
		},
	}

	// Add SDK query flags
	flags.AddQueryFlagsToCmd(cmd)

	return cmd
}