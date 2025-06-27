package main

import (
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/std"
	"github.com/cosmos/cosmos-sdk/x/auth/tx"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	
	// Import provider types to register interfaces
	providertypes "github.com/cosmos/interchain-security/v7/x/ccv/provider/types"
	
	// Import crypto codec to ensure proper type registration
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	ed25519 "github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	secp256k1 "github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
)

// makeEncodingConfig creates the encoding config for the app
func makeEncodingConfig() (codec.Codec, client.TxConfig, types.InterfaceRegistry) {
	interfaceRegistry := types.NewInterfaceRegistry()
	
	// Register standard SDK interfaces (includes basic crypto)
	std.RegisterInterfaces(interfaceRegistry)
	
	// Explicitly register crypto interfaces to ensure proper unmarshaling
	cryptocodec.RegisterInterfaces(interfaceRegistry)
	
	// Register concrete crypto implementations
	interfaceRegistry.RegisterImplementations((*cryptotypes.PubKey)(nil),
		&ed25519.PubKey{},
		&secp256k1.PubKey{},
	)
	
	// Register staking module interfaces (needed for validator types)
	stakingtypes.RegisterInterfaces(interfaceRegistry)
	
	// Register provider module interfaces
	providertypes.RegisterInterfaces(interfaceRegistry)
	
	// Create marshaler with the properly configured interface registry
	marshaler := codec.NewProtoCodec(interfaceRegistry)
	
	// Create TxConfig
	txConfig := tx.NewTxConfig(marshaler, tx.DefaultSignModes)
	
	// Return codec, txConfig, and interfaceRegistry
	return marshaler, txConfig, interfaceRegistry
}