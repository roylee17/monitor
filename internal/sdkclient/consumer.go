package sdkclient

import (
	"context"
	"fmt"
	"time"

	"github.com/cosmos/cosmos-sdk/client"
	clienttypes "github.com/cosmos/ibc-go/v10/modules/core/02-client/types"
	providertypes "github.com/cosmos/interchain-security/v7/x/ccv/provider/types"
)

// CreateConsumer creates a new consumer chain
func (c *Client) CreateConsumer(ctx context.Context, chainID string, metadata providertypes.ConsumerMetadata, initParams providertypes.ConsumerInitializationParameters, powerShaping providertypes.PowerShapingParameters) error {
	// Get the from address
	fromAddr, fromName, _, err := client.GetFromFields(c.clientCtx, c.clientCtx.Keyring, c.clientCtx.FromName)
	if err != nil {
		return fmt.Errorf("failed to get from address: %w", err)
	}

	// Create the create-consumer message
	msg := &providertypes.MsgCreateConsumer{
		ChainId:                    chainID,
		Metadata:                   metadata,
		InitializationParameters:   &initParams,
		PowerShapingParameters:     &powerShaping,
		Submitter:                  fromAddr.String(),
	}

	// Validate the message
	if err := msg.ValidateBasic(); err != nil {
		return fmt.Errorf("message validation failed: %w", err)
	}

	// Sign and broadcast the transaction
	return c.signAndBroadcast(ctx, fromName, msg)
}

// CreateTestConsumer creates a test consumer chain with default parameters
func (c *Client) CreateTestConsumer(ctx context.Context, chainID string) error {
	// Default metadata
	metadata := providertypes.ConsumerMetadata{
		Name:        "Test Consumer Chain",
		Description: "Test consumer chain created via SDK",
	}

	// Default initialization parameters
	initParams := providertypes.ConsumerInitializationParameters{
		InitialHeight: clienttypes.Height{
			RevisionNumber: 0,
			RevisionHeight: 1,
		},
		GenesisHash:                    []byte("not_used"),
		BinaryHash:                     []byte("not_used"),
		SpawnTime:                      time.Now().Add(2 * time.Minute),
		UnbondingPeriod:                24 * time.Hour,          // 1 day
		CcvTimeoutPeriod:               72 * time.Hour,          // 3 days
		TransferTimeoutPeriod:          1 * time.Hour,           // 1 hour
		ConsumerRedistributionFraction: "0.75",
	}

	// Default power shaping parameters
	powerShaping := providertypes.PowerShapingParameters{
		Top_N: 0,
	}

	return c.CreateConsumer(ctx, chainID, metadata, initParams, powerShaping)
}