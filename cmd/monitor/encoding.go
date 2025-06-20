package main

import (
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/std"
	"github.com/cosmos/cosmos-sdk/x/auth/tx"
	
	// Import provider types to register interfaces
	providertypes "github.com/cosmos/interchain-security/v7/x/ccv/provider/types"
)

// makeEncodingConfig creates the encoding config for the app
func makeEncodingConfig() (codec.Codec, client.TxConfig) {
	interfaceRegistry := types.NewInterfaceRegistry()
	marshaler := codec.NewProtoCodec(interfaceRegistry)
	
	// Register standard SDK interfaces
	std.RegisterInterfaces(interfaceRegistry)
	
	// Register provider module interfaces
	providertypes.RegisterInterfaces(interfaceRegistry)
	
	// Create TxConfig
	txConfig := tx.NewTxConfig(marshaler, tx.DefaultSignModes)
	
	// Return both codec and txConfig
	return marshaler, txConfig
}