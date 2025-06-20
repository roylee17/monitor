package keyring

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/cosmos/cosmos-sdk/crypto/keyring"
)

// KeyringManager handles key operations for monitors and relayers
type KeyringManager struct {
	logger *slog.Logger
	home   string
}

// NewKeyringManager creates a new keyring manager
func NewKeyringManager(logger *slog.Logger, home string) *KeyringManager {
	return &KeyringManager{
		logger: logger,
		home:   home,
	}
}

// ImportValidatorKey imports a validator's key from their keyring
func (km *KeyringManager) ImportValidatorKey(validatorName, validatorHome string) error {
	km.logger.Info("Importing validator key", 
		"validator", validatorName,
		"from", validatorHome,
		"to", km.home)

	// For test keyring, we can simply copy the key files
	srcKeyringDir := filepath.Join(validatorHome, "keyring-test")
	dstKeyringDir := filepath.Join(km.home, "keyring-test")

	// Create destination directory
	if err := os.MkdirAll(dstKeyringDir, 0700); err != nil {
		return fmt.Errorf("failed to create keyring directory: %w", err)
	}

	// Copy key files (for test backend, it's just the .info and .address files)
	files := []string{
		fmt.Sprintf("%s.info", validatorName),
		fmt.Sprintf("%s.address", validatorName),
	}

	for _, file := range files {
		src := filepath.Join(srcKeyringDir, file)
		dst := filepath.Join(dstKeyringDir, file)
		
		if err := km.copyFile(src, dst); err != nil {
			km.logger.Warn("Failed to copy key file", 
				"file", file,
				"error", err)
			// Continue with other files
		}
	}

	return km.verifyKey(validatorName)
}

// RecoverValidatorKey recovers a validator's key from mnemonic
func (km *KeyringManager) RecoverValidatorKey(validatorName, mnemonic string) error {
	km.logger.Info("Recovering validator key from mnemonic", 
		"validator", validatorName,
		"to", km.home)

	kr, err := keyring.New("provider", keyring.BackendTest, km.home, nil, nil)
	if err != nil {
		return fmt.Errorf("failed to create keyring: %w", err)
	}

	// Check if key already exists
	if _, err := kr.Key(validatorName); err == nil {
		km.logger.Info("Key already exists, skipping recovery", "name", validatorName)
		return nil
	}

	// Recover the key from mnemonic
	// Using standard Cosmos HD path: m/44'/118'/0'/0/0
	algo, err := keyring.NewSigningAlgoFromString("secp256k1", []keyring.SignatureAlgo{})
	if err != nil {
		return fmt.Errorf("failed to get signing algorithm: %w", err)
	}
	
	record, err := kr.NewAccount(validatorName, mnemonic, "", "m/44'/118'/0'/0/0", algo)
	if err != nil {
		return fmt.Errorf("failed to recover key from mnemonic: %w", err)
	}
	
	addr, err := record.GetAddress()
	if err != nil {
		return fmt.Errorf("failed to get address: %w", err)
	}
	
	km.logger.Info("Key recovered successfully", "name", validatorName, "address", addr.String())
	return km.verifyKey(validatorName)
}

// CreateRelayerKey creates a new key for Hermes relayer
func (km *KeyringManager) CreateRelayerKey(name, mnemonic string) error {
	km.logger.Info("Creating relayer key", "name", name)

	kr, err := keyring.New("ics", keyring.BackendTest, km.home, nil, nil)
	if err != nil {
		return fmt.Errorf("failed to create keyring: %w", err)
	}

	// Get signing algorithm
	algo, err := keyring.NewSigningAlgoFromString("secp256k1", []keyring.SignatureAlgo{})
	if err != nil {
		return fmt.Errorf("failed to get signing algorithm: %w", err)
	}

	// If mnemonic is provided, recover the key
	if mnemonic != "" {
		_, err = kr.NewAccount(name, mnemonic, "", "m/44'/118'/0'/0/0", algo)
		if err != nil {
			return fmt.Errorf("failed to recover key: %w", err)
		}
	} else {
		// Generate new key
		_, _, err = kr.NewMnemonic(name, keyring.English, "m/44'/118'/0'/0/0", "", algo)
		if err != nil {
			return fmt.Errorf("failed to create new key: %w", err)
		}
	}

	return nil
}

// ExportKeyForHermes exports a key in format suitable for Hermes
func (km *KeyringManager) ExportKeyForHermes(keyName, chainID string) (string, error) {
	kr, err := keyring.New("ics", keyring.BackendTest, km.home, nil, nil)
	if err != nil {
		return "", fmt.Errorf("failed to open keyring: %w", err)
	}

	// Get the key
	record, err := kr.Key(keyName)
	if err != nil {
		return "", fmt.Errorf("failed to get key: %w", err)
	}

	// Export as armor (Hermes expects armored private key)
	armor, err := kr.ExportPrivKeyArmor(keyName, "")
	if err != nil {
		return "", fmt.Errorf("failed to export key: %w", err)
	}

	// Create Hermes key file format
	addr, err := record.GetAddress()
	if err != nil {
		return "", fmt.Errorf("failed to get address: %w", err)
	}

	hermesKey := fmt.Sprintf(`{
  "account": "%s",
  "address": "%s",
  "mnemonic": "%s"
}`, keyName, addr.String(), armor)

	return hermesKey, nil
}

// copyFile copies a file from src to dst
func (km *KeyringManager) copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0600)
}

// verifyKey verifies that a key was imported successfully
func (km *KeyringManager) verifyKey(keyName string) error {
	kr, err := keyring.New("ics", keyring.BackendTest, km.home, nil, nil)
	if err != nil {
		return fmt.Errorf("failed to open keyring: %w", err)
	}

	_, err = kr.Key(keyName)
	if err != nil {
		return fmt.Errorf("key not found after import: %w", err)
	}

	km.logger.Info("Key verified successfully", "name", keyName)
	return nil
}

// SetupMonitorKeyring sets up the keyring for a monitor
// This is a convenient method that handles the common case
func (km *KeyringManager) SetupMonitorKeyring(validatorName string, k8sMode bool) error {
	if k8sMode {
		// In K8s mode, we expect keys to be mounted via secrets
		return km.setupFromK8sSecret(validatorName)
	}
	
	// In local mode, import from validator directory
	validatorHome := filepath.Join("/chain", validatorName)
	return km.ImportValidatorKey(validatorName, validatorHome)
}

// setupFromK8sSecret sets up keyring from Kubernetes secret mount
func (km *KeyringManager) setupFromK8sSecret(keyName string) error {
	secretMount := "/keys"
	
	// Check if secret is mounted
	if _, err := os.Stat(secretMount); os.IsNotExist(err) {
		return fmt.Errorf("keys secret not mounted at %s", secretMount)
	}

	// Copy from secret mount to keyring directory
	srcKeyringDir := filepath.Join(secretMount, "keyring-test")
	dstKeyringDir := filepath.Join(km.home, "keyring-test")

	// Create destination directory
	if err := os.MkdirAll(dstKeyringDir, 0700); err != nil {
		return fmt.Errorf("failed to create keyring directory: %w", err)
	}

	// Copy all files from secret mount
	files, err := os.ReadDir(srcKeyringDir)
	if err != nil {
		return fmt.Errorf("failed to read secret mount: %w", err)
	}

	for _, file := range files {
		if file.IsDir() {
			continue
		}
		
		src := filepath.Join(srcKeyringDir, file.Name())
		dst := filepath.Join(dstKeyringDir, file.Name())
		
		if err := km.copyFile(src, dst); err != nil {
			return fmt.Errorf("failed to copy %s: %w", file.Name(), err)
		}
	}

	return km.verifyKey(keyName)
}