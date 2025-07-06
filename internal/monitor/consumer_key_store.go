package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"

	"github.com/cosmos/interchain-security-monitor/internal/subnet"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// ConsumerKeyStore manages consumer key assignments
// Although each cluster runs only one validator, we still index by validator name
// for clarity in logs and potential future multi-validator support
type ConsumerKeyStore struct {
	logger    *slog.Logger
	clientset kubernetes.Interface
	namespace string
	mu        sync.RWMutex
	// In-memory cache: consumerID -> validatorName -> keyInfo
	cache map[string]map[string]*subnet.ConsumerKeyInfo
}

// NewConsumerKeyStore creates a new consumer key store
func NewConsumerKeyStore(logger *slog.Logger, clientset kubernetes.Interface, namespace string) *ConsumerKeyStore {
	return &ConsumerKeyStore{
		logger:    logger,
		clientset: clientset,
		namespace: namespace,
		cache:     make(map[string]map[string]*subnet.ConsumerKeyInfo),
	}
}

// StoreConsumerKey stores an assigned consumer key
func (s *ConsumerKeyStore) StoreConsumerKey(ctx context.Context, info *subnet.ConsumerKeyInfo) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Update in-memory cache
	if s.cache[info.ConsumerID] == nil {
		s.cache[info.ConsumerID] = make(map[string]*subnet.ConsumerKeyInfo)
	}
	s.cache[info.ConsumerID][info.ValidatorName] = info

	// Store in Kubernetes ConfigMap for persistence
	configMapName := fmt.Sprintf("consumer-keys-%s", info.ConsumerID)
	
	// Try to get existing ConfigMap
	cm, err := s.clientset.CoreV1().ConfigMaps(s.namespace).Get(ctx, configMapName, metav1.GetOptions{})
	if err != nil {
		if !errors.IsNotFound(err) {
			return fmt.Errorf("failed to get ConfigMap: %w", err)
		}
		// Create new ConfigMap
		cm = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      configMapName,
				Namespace: s.namespace,
				Labels: map[string]string{
					"app.kubernetes.io/component": "consumer-keys",
					"consumer-id":                 info.ConsumerID,
				},
			},
			Data: make(map[string]string),
		}
	}

	// Marshal key info (without private key due to json:"-" tag)
	keyData, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf("failed to marshal key info: %w", err)
	}

	// Update ConfigMap data
	if cm.Data == nil {
		cm.Data = make(map[string]string)
	}
	cm.Data[info.ValidatorName] = string(keyData)

	// Create or update ConfigMap
	if cm.ResourceVersion == "" {
		_, err = s.clientset.CoreV1().ConfigMaps(s.namespace).Create(ctx, cm, metav1.CreateOptions{})
	} else {
		_, err = s.clientset.CoreV1().ConfigMaps(s.namespace).Update(ctx, cm, metav1.UpdateOptions{})
	}

	if err != nil {
		return fmt.Errorf("failed to store consumer key in ConfigMap: %w", err)
	}

	// Store private key JSON in a Secret if provided
	if info.PrivValidatorKeyJSON != "" {
		secretName := fmt.Sprintf("consumer-key-%s-%s", info.ConsumerID, info.ValidatorName)

		// Create or update Secret
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: s.namespace,
				Labels: map[string]string{
					"app.kubernetes.io/component": "consumer-validator-key",
					"consumer-id":                 info.ConsumerID,
					"validator":                   info.ValidatorName,
				},
			},
			Data: map[string][]byte{
				"priv_validator_key.json": []byte(info.PrivValidatorKeyJSON),
			},
		}

		_, err = s.clientset.CoreV1().Secrets(s.namespace).Get(ctx, secretName, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				_, err = s.clientset.CoreV1().Secrets(s.namespace).Create(ctx, secret, metav1.CreateOptions{})
			} else {
				return fmt.Errorf("failed to get secret: %w", err)
			}
		} else {
			_, err = s.clientset.CoreV1().Secrets(s.namespace).Update(ctx, secret, metav1.UpdateOptions{})
		}
		
		if err != nil {
			return fmt.Errorf("failed to store private key in Secret: %w", err)
		}
		
		s.logger.Info("Stored consumer private key in Secret",
			"secret_name", secretName,
			"consumer_id", info.ConsumerID,
			"validator", info.ValidatorName)
	}

	s.logger.Info("Stored consumer key assignment",
		"consumer_id", info.ConsumerID,
		"validator", info.ValidatorName,
		"pubkey", info.ConsumerPubKey)

	return nil
}

// GetConsumerKey retrieves an assigned consumer key
func (s *ConsumerKeyStore) GetConsumerKey(consumerID, validatorName string) (*subnet.ConsumerKeyInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Check cache first
	if keys, ok := s.cache[consumerID]; ok {
		if info, ok := keys[validatorName]; ok {
			// If we have the info but no private key JSON, try to load it from Secret
			if info.PrivValidatorKeyJSON == "" {
				s.mu.RUnlock()
				privKeyJSON, _ := s.loadPrivateKeyJSON(consumerID, validatorName)
				s.mu.RLock()
				if privKeyJSON != "" {
					info.PrivValidatorKeyJSON = privKeyJSON
				}
			}
			return info, nil
		}
	}

	// Try to load from ConfigMap
	configMapName := fmt.Sprintf("consumer-keys-%s", consumerID)
	cm, err := s.clientset.CoreV1().ConfigMaps(s.namespace).Get(context.Background(), configMapName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get consumer keys: %w", err)
	}

	// Look for validator's key
	if keyData, ok := cm.Data[validatorName]; ok {
		var info subnet.ConsumerKeyInfo
		if err := json.Unmarshal([]byte(keyData), &info); err != nil {
			return nil, fmt.Errorf("failed to unmarshal key info: %w", err)
		}
		
		// Try to load private key JSON from Secret
		privKeyJSON, _ := s.loadPrivateKeyJSON(consumerID, validatorName)
		if privKeyJSON != "" {
			info.PrivValidatorKeyJSON = privKeyJSON
		}
		
		// Update cache
		if s.cache[consumerID] == nil {
			s.cache[consumerID] = make(map[string]*subnet.ConsumerKeyInfo)
		}
		s.cache[consumerID][validatorName] = &info
		
		return &info, nil
	}

	return nil, fmt.Errorf("no consumer key found for validator %s on consumer %s", validatorName, consumerID)
}

// GetAllConsumerKeys returns all assigned keys for a consumer chain
func (s *ConsumerKeyStore) GetAllConsumerKeys(consumerID string) (map[string]*subnet.ConsumerKeyInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Try cache first
	if keys, ok := s.cache[consumerID]; ok && len(keys) > 0 {
		return keys, nil
	}

	// Load from ConfigMap
	configMapName := fmt.Sprintf("consumer-keys-%s", consumerID)
	cm, err := s.clientset.CoreV1().ConfigMaps(s.namespace).Get(context.Background(), configMapName, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return make(map[string]*subnet.ConsumerKeyInfo), nil
		}
		return nil, fmt.Errorf("failed to get consumer keys: %w", err)
	}

	result := make(map[string]*subnet.ConsumerKeyInfo)
	for validatorName, keyData := range cm.Data {
		var info subnet.ConsumerKeyInfo
		if err := json.Unmarshal([]byte(keyData), &info); err != nil {
			s.logger.Warn("Failed to unmarshal key info",
				"validator", validatorName,
				"error", err)
			continue
		}
		result[validatorName] = &info
	}

	// Update cache
	s.cache[consumerID] = result

	return result, nil
}

// LoadFromConfigMaps loads all consumer keys from ConfigMaps on startup
func (s *ConsumerKeyStore) LoadFromConfigMaps(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// List all consumer key ConfigMaps
	cms, err := s.clientset.CoreV1().ConfigMaps(s.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/component=consumer-keys",
	})
	if err != nil {
		return fmt.Errorf("failed to list consumer key ConfigMaps: %w", err)
	}

	for _, cm := range cms.Items {
		consumerID := cm.Labels["consumer-id"]
		if consumerID == "" {
			continue
		}

		if s.cache[consumerID] == nil {
			s.cache[consumerID] = make(map[string]*subnet.ConsumerKeyInfo)
		}

		for validatorName, keyData := range cm.Data {
			var info subnet.ConsumerKeyInfo
			if err := json.Unmarshal([]byte(keyData), &info); err != nil {
				s.logger.Warn("Failed to unmarshal key info",
					"consumer_id", consumerID,
					"validator", validatorName,
					"error", err)
				continue
			}
			s.cache[consumerID][validatorName] = &info
		}
	}

	s.logger.Info("Loaded consumer keys from ConfigMaps",
		"consumer_count", len(s.cache))

	return nil
}

// loadPrivateKeyJSON loads the private key JSON from Secret
func (s *ConsumerKeyStore) loadPrivateKeyJSON(consumerID, validatorName string) (string, error) {
	secretName := fmt.Sprintf("consumer-key-%s-%s", consumerID, validatorName)
	
	secret, err := s.clientset.CoreV1().Secrets(s.namespace).Get(context.Background(), secretName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get secret: %w", err)
	}
	
	privKeyData, ok := secret.Data["priv_validator_key.json"]
	if !ok {
		return "", fmt.Errorf("priv_validator_key.json not found in secret")
	}
	
	// Return the entire priv_validator_key.json content
	return string(privKeyData), nil
}