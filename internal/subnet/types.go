package subnet

import (
	"fmt"
	"log/slog"
)

// ConsumerKeyInfo holds assigned consumer key information
type ConsumerKeyInfo struct {
	ValidatorName    string `json:"validator_name"`
	ConsumerID       string `json:"consumer_id"`
	ConsumerPubKey   string `json:"consumer_pub_key"`
	ConsumerAddress  string `json:"consumer_address"`
	ProviderAddress  string `json:"provider_address"`
	AssignmentHeight int64  `json:"assignment_height"`
	PrivateKey       []byte `json:"-"` // Not serialized
}

// ConsumerDeploymentConfig contains all configuration for deploying a consumer chain
type ConsumerDeploymentConfig struct {
	// Required fields
	ChainID    string
	ConsumerID string
	
	// Optional fields with defaults
	ValidatorName string
	Ports         *Ports
	Peers         []string
	CCVPatch      map[string]interface{}
	NodeKeyJSON   string
	ConsumerKey   *ConsumerKeyInfo
	
	// Advanced options
	UseDynamicPeers bool
}

// K8sManagerConfig contains configuration for creating a K8sManager
type K8sManagerConfig struct {
	BaseManager     *Manager
	Logger          *slog.Logger
	NamespacePrefix string
	ConsumerImage   string
	ValidatorName   string
}

// Validate checks if the configuration is valid
func (c *ConsumerDeploymentConfig) Validate() error {
	if c.ChainID == "" {
		return fmt.Errorf("chain ID is required")
	}
	if c.ConsumerID == "" {
		return fmt.Errorf("consumer ID is required")
	}
	return nil
}

// SetDefaults applies default values to optional fields
func (c *ConsumerDeploymentConfig) SetDefaults() {
	if c.Ports == nil {
		// Calculate default ports based on chain ID
		ports, _ := CalculatePorts(c.ChainID)
		c.Ports = ports
	}
}