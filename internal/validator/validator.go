package validator

import (
	"context"
	"fmt"

	rpcclient "github.com/cometbft/cometbft/rpc/client/http"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/types/query"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/cosmos/interchain-security-monitor/internal/config"
)

// QueryAndPrintValidators queries validators using SDK query clients
func QueryAndPrintValidators(cfg config.Config) error {
	// Create client context for SDK queries
	clientCtx, err := cfg.ToClientContext()
	if err != nil {
		return fmt.Errorf("failed to create client context: %w", err)
	}

	return QueryAndPrintValidatorsWithContext(clientCtx, cfg.RPCURL)
}

// QueryAndPrintValidatorsWithContext queries validators using an existing client context
func QueryAndPrintValidatorsWithContext(clientCtx client.Context, rpcURL string) error {
	// Create staking query client
	stakingClient := stakingtypes.NewQueryClient(clientCtx)

	// Query bonded validators using SDK
	resp, err := stakingClient.Validators(context.Background(), &stakingtypes.QueryValidatorsRequest{
		Status: "BOND_STATUS_BONDED",
		Pagination: &query.PageRequest{
			Limit: 100,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to query validators: %w", err)
	}

	// Try to get the local validator moniker
	localMoniker, err := getLocalValidatorMoniker(rpcURL)
	if err != nil {
		fmt.Printf("Warning: Could not query local validator from %s: %v\n", rpcURL, err)
	}

	fmt.Printf("Bonded Validators (from SDK):\n")
	for _, v := range resp.Validators {
		marker := ""

		// Check if this is the local validator by comparing moniker
		if localMoniker != "" && v.Description.Moniker == localMoniker {
			marker = " <- Local Validator"
		}

		fmt.Printf("  Operator Address: %s\n", v.OperatorAddress)
		fmt.Printf("  Moniker: %s\n", v.Description.Moniker)
		fmt.Printf("  Status: %s\n", v.Status.String())
		fmt.Printf("  Tokens: %s\n", v.Tokens.String())
		fmt.Printf("  Delegator Shares: %s\n", v.DelegatorShares.String())
		fmt.Printf("  Commission Rate: %s%s\n", v.Commission.CommissionRates.Rate.String(), marker)
		fmt.Printf("  ---\n")
	}

	return nil
}

// getLocalValidatorMoniker queries the CometBFT node to get the validator's moniker
func getLocalValidatorMoniker(rpcURL string) (string, error) {
	// Create a client to connect to CometBFT
	localClient, err := rpcclient.New(rpcURL, "/websocket")
	if err != nil {
		return "", fmt.Errorf("failed to create RPC client: %v", err)
	}

	// Query the status to get the node info
	status, err := localClient.Status(context.Background())
	if err != nil {
		return "", fmt.Errorf("failed to query node status: %v", err)
	}

	// Check if this node is a validator
	if status.ValidatorInfo.PubKey == nil {
		return "", fmt.Errorf("node is not a validator")
	}

	return status.NodeInfo.Moniker, nil
}
