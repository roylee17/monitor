package subnet

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/cometbft/cometbft/crypto/ed25519" 
	"github.com/cometbft/cometbft/p2p"
)

// NodeKeyGenerator generates deterministic node keys for consumer chains
type NodeKeyGenerator struct{}

// NewNodeKeyGenerator creates a new node key generator
func NewNodeKeyGenerator() *NodeKeyGenerator {
	return &NodeKeyGenerator{}
}

// GenerateNodeKey generates a deterministic node key for a validator on a specific chain
func (g *NodeKeyGenerator) GenerateNodeKey(validatorName, chainID string) (*p2p.NodeKey, error) {
	// Create a deterministic seed from validator name and chain ID
	seed := fmt.Sprintf("%s-%s-%s", NodeIDPrefix, validatorName, chainID)
	
	// Hash the seed to get 32 bytes for ed25519 private key
	hash := sha256.Sum256([]byte(seed))
	
	// Create ed25519 private key from the hash
	privKey := ed25519.GenPrivKeyFromSecret(hash[:])
	
	// Create node key
	nodeKey := &p2p.NodeKey{
		PrivKey: privKey,
	}
	
	return nodeKey, nil
}

// GetNodeID returns the node ID for a given validator and chain
func (g *NodeKeyGenerator) GetNodeID(validatorName, chainID string) (string, error) {
	nodeKey, err := g.GenerateNodeKey(validatorName, chainID)
	if err != nil {
		return "", err
	}
	
	return string(nodeKey.ID()), nil
}

// GenerateNodeKeyJSON generates the node_key.json content for a validator
func (g *NodeKeyGenerator) GenerateNodeKeyJSON(validatorName, chainID string) ([]byte, error) {
	nodeKey, err := g.GenerateNodeKey(validatorName, chainID)
	if err != nil {
		return nil, err
	}
	
	// Create the JSON structure that CometBFT expects
	type NodeKeyJSON struct {
		PrivKey struct {
			Type  string `json:"type"`
			Value string `json:"value"`
		} `json:"priv_key"`
	}
	
	nodeKeyJSON := NodeKeyJSON{}
	nodeKeyJSON.PrivKey.Type = "tendermint/PrivKeyEd25519"
	nodeKeyJSON.PrivKey.Value = base64.StdEncoding.EncodeToString(nodeKey.PrivKey.Bytes())
	
	return json.MarshalIndent(nodeKeyJSON, "", "  ")
}

// CalculatePeerAddress calculates the full peer address for a validator
func (g *NodeKeyGenerator) CalculatePeerAddress(validatorName, chainID string, host string, port int) (string, error) {
	nodeID, err := g.GetNodeID(validatorName, chainID)
	if err != nil {
		return "", err
	}
	
	return fmt.Sprintf("%s@%s:%d", nodeID, host, port), nil
}