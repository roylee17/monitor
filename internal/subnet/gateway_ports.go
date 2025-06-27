package subnet

import (
	"crypto/sha256"
	"encoding/binary"
)

// Gateway port constants
const (
	// GatewayBaseNodePort is the base NodePort for consumer chain gateways
	GatewayBaseNodePort = 30100
	
	// GatewayMaxPorts is the maximum number of gateway ports (30100-30199)
	GatewayMaxPorts = 100
)

// CalculateGatewayNodePort calculates the gateway NodePort for a consumer chain.
// This MUST match the logic used by TraefikGatewayManager to ensure consistency.
// All validators must calculate the same port for a given chain ID.
func CalculateGatewayNodePort(chainID string) int32 {
	// Use SHA256 for better distribution
	hash := sha256.Sum256([]byte(chainID))
	
	// Use first 4 bytes as uint32
	hashNum := binary.BigEndian.Uint32(hash[:4])
	
	// Calculate offset within allowed range
	offset := hashNum % GatewayMaxPorts
	
	return int32(GatewayBaseNodePort + offset)
}

// GetGatewayPortRange returns the valid port range for consumer gateways
func GetGatewayPortRange() (int32, int32) {
	return GatewayBaseNodePort, GatewayBaseNodePort + GatewayMaxPorts - 1
}

// CalculateValidatorNodePort calculates a unique NodePort for a specific validator's consumer chain.
// This ensures each validator gets different ports for the same consumer chain.
func CalculateValidatorNodePort(chainID string, validatorName string) int32 {
	// Combine chain ID and validator name for unique hash
	combined := chainID + "-" + validatorName
	hash := sha256.Sum256([]byte(combined))
	
	// Use first 4 bytes as uint32
	hashNum := binary.BigEndian.Uint32(hash[:4])
	
	// Calculate offset within allowed range
	// Use a larger range (30100-30399) for multi-validator setup
	offset := hashNum % 300
	
	return int32(GatewayBaseNodePort + offset)
}