package main

import (
	"bufio"
	"os"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/client/keys"
	"github.com/spf13/cobra"
)

// keysCmd represents the keys command
func keysCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "keys",
		Short: "Manage your application's keys",
		Long: `Keyring management commands. These keys may be in any format supported by the
Tendermint crypto library and can be used by light-clients, full nodes, or any other application
that needs to sign with a private key.

The keyring supports the following backends:

    os          Uses the operating system's default credentials store.
    file        Uses encrypted file-based keystore within the app's configuration directory.
                This keyring will request a password each time it is accessed.
    kwallet     Uses KDE Wallet Manager as a credentials management application.
    pass        Uses the pass command line utility to store and retrieve keys.
    test        Stores keys insecurely to disk. It does not prompt for a password to be unlocked
                and it should be used only for testing purposes.

kwallet and pass backends depend on external tools. Refer to their respective documentation for more
information:
    KWallet     https://github.com/KDE/kwallet
    pass        https://www.passwordstore.org/

The pass backend requires GnuPG: https://gnupg.org/
`,
		// Preserve client context for all subcommands
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Get client context from parent
			initClientCtx := client.GetClientContextFromCmd(cmd)
			
			// Initialize codec if not set
			if initClientCtx.Codec == nil || initClientCtx.InterfaceRegistry == nil {
				// Initialize the codec here
				codec, txConfig, interfaceRegistry := makeEncodingConfig()
				initClientCtx = initClientCtx.WithCodec(codec).WithTxConfig(txConfig).WithInterfaceRegistry(interfaceRegistry)
			}
			
			// Initialize input/output if not set
			if initClientCtx.Input == nil {
				// Check if stdin is available
				stat, _ := os.Stdin.Stat()
				if (stat.Mode() & os.ModeCharDevice) == 0 {
					// stdin is a pipe or redirected
					initClientCtx = initClientCtx.WithInput(bufio.NewReader(os.Stdin))
				} else {
					// stdin is a terminal - create a new reader
					initClientCtx = initClientCtx.WithInput(bufio.NewReader(os.Stdin))
				}
			}
			if initClientCtx.Output == nil {
				initClientCtx = initClientCtx.WithOutput(os.Stdout)
			}
			
			// Set output format
			initClientCtx = initClientCtx.WithOutputFormat("text")
			
			// Handle home directory
			homeDir, _ := cmd.Flags().GetString(flags.FlagHome)
			if homeDir != "" {
				initClientCtx = initClientCtx.WithHomeDir(homeDir)
			} else if initClientCtx.HomeDir == "" {
				initClientCtx = initClientCtx.WithHomeDir("/data/.provider")
			}
			
			// Set keyring backend
			keyringBackend, _ := cmd.Flags().GetString(flags.FlagKeyringBackend)
			if keyringBackend != "" {
				initClientCtx = initClientCtx.WithKeyringDir(initClientCtx.HomeDir)
			}
			
			// Set the client context
			if err := client.SetCmdClientContext(cmd, initClientCtx); err != nil {
				return err
			}
			
			return nil
		},
	}

	// Add subcommands - using standard Cosmos SDK key commands
	cmd.AddCommand(
		keys.AddKeyCommand(),
		keys.DeleteKeyCommand(),
		keys.ExportKeyCommand(),
		keys.ImportKeyCommand(),
		keys.ListKeysCmd(),
		keys.ShowKeysCmd(),
		keys.MnemonicKeyCommand(),
		keys.ParseKeyStringCommand(),
		keys.RenameKeyCommand(),
	)

	// Add standard flags
	cmd.PersistentFlags().String(flags.FlagHome, "", "The application home directory")
	cmd.PersistentFlags().String(flags.FlagKeyringBackend, "test", "Select keyring's backend (os|file|kwallet|pass|test)")
	cmd.PersistentFlags().String(flags.FlagKeyringDir, "", "The client Keyring directory; if omitted, the default 'home' directory will be used")

	return cmd
}