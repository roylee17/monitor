package monitor

import (
	"fmt"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
)

// InitializeKeyring creates and initializes a keyring for the monitor
func InitializeKeyring(clientCtx client.Context, homeDir, keyringBackend, fromKey string) (client.Context, error) {
	// Ensure we have a codec
	if clientCtx.Codec == nil {
		return clientCtx, fmt.Errorf("codec not initialized in client context")
	}

	// Create keyring with proper configuration
	kr, err := keyring.New(
		"monitor",           // app name
		keyringBackend,      // backend type (test, file, os)
		homeDir,             // keyring directory
		nil,                 // stdin reader (nil for non-interactive)
		clientCtx.Codec,     // codec for encoding/decoding
	)
	if err != nil {
		return clientCtx, fmt.Errorf("failed to create keyring: %w", err)
	}

	// Verify the key exists
	if fromKey != "" {
		_, err = kr.Key(fromKey)
		if err != nil {
			return clientCtx, fmt.Errorf("key '%s' not found in keyring: %w", fromKey, err)
		}
	}

	// Update client context with keyring
	clientCtx = clientCtx.WithKeyring(kr)
	
	return clientCtx, nil
}

// GetValidatorIdentity retrieves the validator's identity from the keyring
func GetValidatorIdentity(clientCtx client.Context, fromKey string) (ValidatorIdentity, error) {
	if clientCtx.Keyring == nil {
		return ValidatorIdentity{}, fmt.Errorf("keyring not initialized")
	}

	keyInfo, err := clientCtx.Keyring.Key(fromKey)
	if err != nil {
		return ValidatorIdentity{}, fmt.Errorf("failed to get key info: %w", err)
	}

	addr, err := keyInfo.GetAddress()
	if err != nil {
		return ValidatorIdentity{}, fmt.Errorf("failed to get address: %w", err)
	}

	return ValidatorIdentity{
		Name:           fromKey,
		AccountAddress: addr.String(),
		KeyType:        keyInfo.GetType().String(),
	}, nil
}

// ValidatorIdentity holds validator identity information
type ValidatorIdentity struct {
	Name           string
	AccountAddress string
	KeyType        string
}

