package deployment

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	cosmosed25519 "github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// ConsumerKeyManager handles consumer key generation and assignment
type ConsumerKeyManager struct {
	logger    *slog.Logger
	clientset kubernetes.Interface
}

// NewConsumerKeyManager creates a new consumer key manager
func NewConsumerKeyManager(logger *slog.Logger, clientset kubernetes.Interface) *ConsumerKeyManager {
	return &ConsumerKeyManager{
		logger:    logger,
		clientset: clientset,
	}
}

// GenerateConsumerKey generates a new key pair for a consumer chain validator
func (m *ConsumerKeyManager) GenerateConsumerKey(validatorName, chainID string) (cryptotypes.PrivKey, error) {
	// Generate new ed25519 key pair
	privKey := cosmosed25519.GenPrivKey()
	
	m.logger.Info("Generated consumer key",
		"validator", validatorName,
		"chain_id", chainID,
		"pubkey", privKey.PubKey().String())
	
	return privKey, nil
}

// StoreConsumerKey stores the consumer key as a Kubernetes secret
func (m *ConsumerKeyManager) StoreConsumerKey(ctx context.Context, namespace, validatorName, chainID string, privKey cryptotypes.PrivKey) error {
	secretName := fmt.Sprintf("%s-%s-consumer-key", validatorName, chainID)
	
	// Create validator key JSON structure
	keyJSON := map[string]interface{}{
		"address": privKey.PubKey().Address().String(),
		"pub_key": map[string]interface{}{
			"type":  "tendermint/PubKeyEd25519",
			"value": privKey.PubKey().String(),
		},
		"priv_key": map[string]interface{}{
			"type":  "tendermint/PrivKeyEd25519",
			"value": privKey.String(),
		},
	}
	
	keyData, err := json.MarshalIndent(keyJSON, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal key: %w", err)
	}
	
	// Create Kubernetes secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":       "consumer-key",
				"app.kubernetes.io/instance":   chainID,
				"app.kubernetes.io/component":  "validator",
				"app.kubernetes.io/managed-by": "interchain-security-monitor",
				"validator":                    validatorName,
				"chain-id":                     chainID,
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"priv_validator_key.json": keyData,
		},
	}
	
	_, err = m.clientset.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create secret: %w", err)
	}
	
	m.logger.Info("Stored consumer key as secret",
		"secret", secretName,
		"namespace", namespace)
	
	return nil
}

// GetConsumerKeyInfo returns the public key info for consumer key assignment
func (m *ConsumerKeyManager) GetConsumerKeyInfo(privKey cryptotypes.PrivKey) map[string]string {
	return map[string]string{
		"pubkey_type":  "ed25519",
		"pubkey_value": privKey.PubKey().String(),
		"address":      privKey.PubKey().Address().String(),
	}
}