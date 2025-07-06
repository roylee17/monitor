package subnet

import (
	"crypto/sha256"
	"encoding/binary"
)

// Gateway port constants
const (
	// GatewayBaseNodePort is the base NodePort for consumer chain gateways
	GatewayBaseNodePort = 30100
)

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