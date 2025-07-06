package transaction

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/cosmos/cosmos-sdk/client"
)

// TxService handles transaction building and broadcasting
type TxService struct {
	clientCtx client.Context
	homeDir   string
	chainID   string
	nodeURL   string
	fromKey   string
}

// NewTxService creates a new transaction service
func NewTxService(clientCtx client.Context) *TxService {
	// Use the key name as-is since keys are created as "alice", "bob", "charlie"
	fromKey := clientCtx.FromName

	return &TxService{
		clientCtx: clientCtx,
		homeDir:   clientCtx.HomeDir,
		chainID:   clientCtx.ChainID,
		nodeURL:   clientCtx.NodeURI,
		fromKey:   fromKey,
	}
}

// OptIn sends an opt-in transaction for a validator to join a consumer chain
// Uses interchain-security-pd directly to avoid keyring issues
func (ts *TxService) OptIn(ctx context.Context, consumerID string, consumerPubKey string) error {
	if ts.fromKey == "" {
		return fmt.Errorf("no from key specified")
	}

	if ts.homeDir == "" {
		return fmt.Errorf("no home directory specified")
	}

	if ts.nodeURL == "" {
		return fmt.Errorf("no node URL specified")
	}

	if ts.chainID == "" {
		return fmt.Errorf("no chain ID specified")
	}

	// Build the opt-in command
	args := []string{"tx", "provider", "opt-in", consumerID}
	
	// Only add consumer public key if provided
	if consumerPubKey != "" {
		args = append(args, consumerPubKey)
	}
	
	// Add common flags
	args = append(args,
		"--home", ts.homeDir+"/.provider",
		"--chain-id", ts.chainID,
		"--from", ts.fromKey,
		"--keyring-backend", "test",
		"--keyring-dir", ts.homeDir+"/.provider",
		"--node", ts.nodeURL,
		"--gas", "500000",
		"--gas-prices", "0.001stake",
		"-o", "json", "-y")
	
	cmd := exec.CommandContext(ctx, "interchain-security-pd", args...)

	// Debug: log the command being executed
	fmt.Printf("Executing opt-in command: interchain-security-pd %v\n", args)

	// Execute the command
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to execute opt-in command: %w, output: %s", err, string(output))
	}

	// Log the output for debugging
	fmt.Printf("Opt-in transaction output: %s\n", string(output))

	return nil
}

// TxResult represents the result of a transaction
type TxResult struct {
	TxHash string
	Code   uint32
	RawLog string
}

// ExecuteTx executes a generic transaction command
func (ts *TxService) ExecuteTx(ctx context.Context, args []string) (*TxResult, error) {
	if ts.fromKey == "" {
		return nil, fmt.Errorf("no from key specified")
	}

	if ts.homeDir == "" {
		return nil, fmt.Errorf("no home directory specified")
	}

	if ts.nodeURL == "" {
		return nil, fmt.Errorf("no node URL specified")
	}

	if ts.chainID == "" {
		return nil, fmt.Errorf("no chain ID specified")
	}

	// Add common flags
	fullArgs := append(args,
		"--home", ts.homeDir+"/.provider",
		"--chain-id", ts.chainID,
		"--keyring-backend", "test",
		"--keyring-dir", ts.homeDir+"/.provider",
		"--node", ts.nodeURL,
		"-o", "json")
	
	cmd := exec.CommandContext(ctx, "interchain-security-pd", fullArgs...)

	// Execute the command
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to execute transaction: %w, output: %s", err, string(output))
	}

	// Parse the output to get tx hash
	// For now, return a simple result
	return &TxResult{
		TxHash: "pending", // Would parse from JSON output
		Code:   0,
		RawLog: string(output),
	}, nil
}
