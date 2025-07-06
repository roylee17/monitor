package subnet

import (
	"fmt"
)

// ConsumerKeyInfo holds assigned consumer key information
type ConsumerKeyInfo struct {
	ValidatorName        string `json:"validator_name"`
	ConsumerID           string `json:"consumer_id"`
	ConsumerPubKey       string `json:"consumer_pub_key"`
	ConsumerAddress      string `json:"consumer_address"`
	ProviderAddress      string `json:"provider_address"`
	AssignmentHeight     int64  `json:"assignment_height"`
	PrivValidatorKeyJSON string `json:"-"` // Complete priv_validator_key.json content (not serialized)
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

// Validate checks if the configuration is valid
func (c *ConsumerDeploymentConfig) Validate() error {
	if c.ChainID == "" {
		return fmt.Errorf("chain ID is required")
	}
	if c.ConsumerID == "" {
		return fmt.Errorf("consumer ID is required")
	}
	
	// Validate ports if provided
	if c.Ports != nil {
		if err := ValidatePorts(c.Ports); err != nil {
			return fmt.Errorf("invalid port configuration: %w", err)
		}
	}
	
	return nil
}

// SetDefaults applies default values to optional fields
func (c *ConsumerDeploymentConfig) SetDefaults() error {
	if c.Ports == nil {
		// Calculate default ports based on chain ID
		ports, err := CalculatePorts(c.ChainID)
		if err != nil {
			return fmt.Errorf("failed to calculate default ports: %w", err)
		}
		c.Ports = ports
	}
	return nil
}