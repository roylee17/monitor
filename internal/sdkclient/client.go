// Package sdkclient provides a native Go SDK client for interacting with Interchain Security
package sdkclient

import (
	"context"
	"fmt"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/tx"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/tx/signing"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	
	providertypes "github.com/cosmos/interchain-security/v7/x/ccv/provider/types"
)

// Client provides native SDK functionality for interchain security operations
type Client struct {
	clientCtx client.Context
	txFactory tx.Factory
}

// NewClient creates a new SDK client
func NewClient(clientCtx client.Context) (*Client, error) {
	// Ensure we have necessary components
	if clientCtx.Client == nil {
		return nil, fmt.Errorf("client context must have RPC client")
	}
	
	// For query-only operations, we don't need keyring
	// For transaction operations, we'll validate later
	
	// Ensure we have TxConfig
	if clientCtx.TxConfig == nil {
		return nil, fmt.Errorf("client context must have TxConfig")
	}
	
	// Ensure we have AccountRetriever
	if clientCtx.AccountRetriever == nil {
		clientCtx = clientCtx.WithAccountRetriever(authtypes.AccountRetriever{})
	}

	// Create transaction factory only if we have keyring
	var txFactory tx.Factory
	if clientCtx.Keyring != nil && clientCtx.ChainID != "" {
		txFactory = tx.Factory{}.
			WithChainID(clientCtx.ChainID).
			WithKeybase(clientCtx.Keyring).
			WithTxConfig(clientCtx.TxConfig).
			WithAccountRetriever(clientCtx.AccountRetriever).
			WithSignMode(signing.SignMode_SIGN_MODE_DIRECT)
	}

	return &Client{
		clientCtx: clientCtx,
		txFactory: txFactory,
	}, nil
}

// OptIn submits an opt-in transaction for a consumer chain
func (c *Client) OptIn(ctx context.Context, consumerID string, consumerPubKey string) error {
	if c.clientCtx.Keyring == nil {
		return fmt.Errorf("keyring is required for transaction operations")
	}
	
	// Get the from address
	fromAddr, fromName, _, err := client.GetFromFields(c.clientCtx, c.clientCtx.Keyring, c.clientCtx.FromName)
	if err != nil {
		return fmt.Errorf("failed to get from address: %w", err)
	}

	// Get validator address from delegator address
	valAddr := sdk.ValAddress(fromAddr)

	// Create the opt-in message
	msg := &providertypes.MsgOptIn{
		ProviderAddr: valAddr.String(),
		ConsumerId:   consumerID,
		ConsumerKey:  consumerPubKey,
		Signer:       fromAddr.String(),
	}

	// Validate the message
	if err := msg.ValidateBasic(); err != nil {
		return fmt.Errorf("message validation failed: %w", err)
	}

	// Sign and broadcast the transaction
	return c.signAndBroadcast(ctx, fromName, msg)
}

// OptOut submits an opt-out transaction for a consumer chain
func (c *Client) OptOut(ctx context.Context, consumerID string) error {
	// Get the from address
	fromAddr, fromName, _, err := client.GetFromFields(c.clientCtx, c.clientCtx.Keyring, c.clientCtx.FromName)
	if err != nil {
		return fmt.Errorf("failed to get from address: %w", err)
	}

	// Get validator address from delegator address
	valAddr := sdk.ValAddress(fromAddr)

	// Create the opt-out message
	msg := &providertypes.MsgOptOut{
		ProviderAddr: valAddr.String(),
		ConsumerId:   consumerID,
		Signer:       fromAddr.String(),
	}

	// Validate the message
	if err := msg.ValidateBasic(); err != nil {
		return fmt.Errorf("message validation failed: %w", err)
	}

	// Sign and broadcast the transaction
	return c.signAndBroadcast(ctx, fromName, msg)
}

// AssignConsumerKey assigns a consensus key for a consumer chain
func (c *Client) AssignConsumerKey(ctx context.Context, consumerID string, consumerKey string) error {
	// Get the from address
	fromAddr, fromName, _, err := client.GetFromFields(c.clientCtx, c.clientCtx.Keyring, c.clientCtx.FromName)
	if err != nil {
		return fmt.Errorf("failed to get from address: %w", err)
	}

	// Get validator address from delegator address
	valAddr := sdk.ValAddress(fromAddr)

	// Create the assign key message
	msg := &providertypes.MsgAssignConsumerKey{
		ProviderAddr: valAddr.String(),
		ConsumerId:   consumerID,
		ConsumerKey:  consumerKey,
		Signer:       fromAddr.String(),
	}

	// Validate the message
	if err := msg.ValidateBasic(); err != nil {
		return fmt.Errorf("message validation failed: %w", err)
	}

	// Sign and broadcast the transaction
	return c.signAndBroadcast(ctx, fromName, msg)
}

// signAndBroadcast signs and broadcasts a transaction
func (c *Client) signAndBroadcast(ctx context.Context, fromName string, msgs ...sdk.Msg) error {
	// Get account info for sequence and account number
	info, err := c.clientCtx.Keyring.Key(fromName)
	if err != nil {
		return fmt.Errorf("failed to get key info: %w", err)
	}

	addr, err := info.GetAddress()
	if err != nil {
		return fmt.Errorf("failed to get address from key info: %w", err)
	}

	// Query account to get account number and sequence
	accountRetriever := authtypes.AccountRetriever{}
	account, err := accountRetriever.GetAccount(c.clientCtx, addr)
	if err != nil {
		return fmt.Errorf("failed to get account: %w", err)
	}

	// Update factory with account info
	c.txFactory = c.txFactory.
		WithAccountNumber(account.GetAccountNumber()).
		WithSequence(account.GetSequence())

	// Estimate gas
	_, adjusted, err := tx.CalculateGas(c.clientCtx, c.txFactory, msgs...)
	if err != nil {
		return fmt.Errorf("failed to calculate gas: %w", err)
	}

	// Set gas
	c.txFactory = c.txFactory.WithGas(adjusted)

	// Build unsigned transaction
	txBuilder, err := c.txFactory.BuildUnsignedTx(msgs...)
	if err != nil {
		return fmt.Errorf("failed to build unsigned tx: %w", err)
	}

	// Sign the transaction
	err = tx.Sign(ctx, c.txFactory, fromName, txBuilder, true)
	if err != nil {
		return fmt.Errorf("failed to sign tx: %w", err)
	}

	// Encode the transaction
	txBytes, err := c.clientCtx.TxConfig.TxEncoder()(txBuilder.GetTx())
	if err != nil {
		return fmt.Errorf("failed to encode tx: %w", err)
	}

	// Broadcast the transaction
	res, err := c.clientCtx.BroadcastTx(txBytes)
	if err != nil {
		return fmt.Errorf("failed to broadcast tx: %w", err)
	}

	// Check response code
	if res.Code != 0 {
		return fmt.Errorf("transaction failed with code %d: %s", res.Code, res.RawLog)
	}

	fmt.Printf("Transaction successful: %s\n", res.TxHash)
	return nil
}

// QueryConsumerChains queries the list of consumer chains
func (c *Client) QueryConsumerChains(ctx context.Context) (*providertypes.QueryConsumerChainsResponse, error) {
	queryClient := providertypes.NewQueryClient(c.clientCtx)
	
	res, err := queryClient.QueryConsumerChains(ctx, &providertypes.QueryConsumerChainsRequest{
		Pagination: nil,
		Phase:      providertypes.CONSUMER_PHASE_UNSPECIFIED,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query consumer chains: %w", err)
	}

	return res, nil
}

// QueryConsumerChain queries a specific consumer chain
func (c *Client) QueryConsumerChain(ctx context.Context, consumerID string) (*providertypes.QueryConsumerChainResponse, error) {
	queryClient := providertypes.NewQueryClient(c.clientCtx)
	
	res, err := queryClient.QueryConsumerChain(ctx, &providertypes.QueryConsumerChainRequest{
		ConsumerId: consumerID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query consumer chain: %w", err)
	}

	return res, nil
}

// QueryOptedInValidators queries validators opted into a consumer chain
func (c *Client) QueryOptedInValidators(ctx context.Context, consumerID string) ([]string, error) {
	queryClient := providertypes.NewQueryClient(c.clientCtx)
	
	res, err := queryClient.QueryConsumerValidators(ctx, &providertypes.QueryConsumerValidatorsRequest{
		ConsumerId: consumerID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query opted-in validators: %w", err)
	}

	// Extract validator addresses
	var addresses []string
	for _, val := range res.Validators {
		addresses = append(addresses, val.ProviderAddress)
	}

	return addresses, nil
}

// GetFromAddress returns the from address from the client context
func (c *Client) GetFromAddress() (sdk.AccAddress, error) {
	if c.clientCtx.FromName == "" {
		return nil, fmt.Errorf("no from name specified")
	}

	info, err := c.clientCtx.Keyring.Key(c.clientCtx.FromName)
	if err != nil {
		return nil, fmt.Errorf("failed to get key info: %w", err)
	}

	addr, err := info.GetAddress()
	if err != nil {
		return nil, fmt.Errorf("failed to get address from key info: %w", err)
	}

	return addr, nil
}