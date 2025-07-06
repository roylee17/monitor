package monitor

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
)

// ValidatorKeyType represents the type of key found in the initial validator set
type ValidatorKeyType string

const (
	KeyTypeProvider ValidatorKeyType = "provider" // Using provider consensus key
	KeyTypeConsumer ValidatorKeyType = "consumer" // Using assigned consumer key
)

// InitialSetKeyInfo contains information about the key found in initial validator set
type InitialSetKeyInfo struct {
	Found    bool
	KeyType  ValidatorKeyType
	KeyValue string // Base64 encoded public key
}

// ConsumerContext stores context information for a consumer chain
type ConsumerContext struct {
	ChainID                  string
	ConsumerID               string
	ClientID                 string
	CreatedAt                int64                  // Block height when created
	CCVPatch                 map[string]interface{} // CCV genesis patch data
	SpawnTime                time.Time              // When the consumer chain should spawn
	ValidatorSet             []string               // Validators participating in this consumer
	IsLocalValidatorSelected bool                   // Whether the local validator was selected for this consumer
}

// extractPubKeyFromConsensusKey extracts the base64 pubkey from consensus key string.
//
// This helper function handles multiple formats of consensus keys that may be
// returned by the blockchain:
//
// Format 1: JSON format - {"@type":"/cosmos.crypto.ed25519.PubKey","key":"base64key"}
// Format 2: Tendermint format - PubKeyEd25519{hexkey}
// Format 3: Direct base64 string (less common)
//
// Parameters:
//   - consensusKey: The consensus key string in any supported format
//
// Returns:
//   - The base64-encoded public key, or empty string if parsing fails
//
// Note: This function is critical for matching validators between provider
// and consumer chains, as key formats may vary between different sources.
func extractPubKeyFromConsensusKey(consensusKey string) string {
	// Handle different formats of consensus keys
	// Format 1: {"@type":"/cosmos.crypto.ed25519.PubKey","key":"base64key"}
	if strings.Contains(consensusKey, "@type") {
		var keyData map[string]interface{}
		if err := json.Unmarshal([]byte(consensusKey), &keyData); err == nil {
			if key, ok := keyData["key"].(string); ok {
				return key
			}
		}
	}

	// Format 2: PubKeyEd25519{hexkey}
	if strings.HasPrefix(consensusKey, "PubKeyEd25519{") && strings.HasSuffix(consensusKey, "}") {
		hexStr := consensusKey[14 : len(consensusKey)-1]
		if pubKeyBytes, err := hex.DecodeString(hexStr); err == nil {
			return base64.StdEncoding.EncodeToString(pubKeyBytes)
		}
	}

	// Format 3: Direct base64 (less common)
	if _, err := base64.StdEncoding.DecodeString(consensusKey); err == nil {
		return consensusKey
	}

	return ""
}