package subnet

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	
	"github.com/cosmos/interchain-security-monitor/internal/constants"
)

// Gateway port constants
const (
	// GatewayBaseNodePort is the base NodePort for consumer chain gateways
	GatewayBaseNodePort = 30100
)

// CalculateValidatorNodePort calculates a unique NodePort for a specific validator's consumer chain.
// This ensures each validator gets different ports for the same consumer chain.
func CalculateValidatorNodePort(chainID string, validatorName string) (int32, error) {
	// Combine chain ID and validator name for unique hash
	combined := chainID + "-" + validatorName
	hash := sha256.Sum256([]byte(combined))
	
	// Use first 4 bytes as uint32
	hashNum := binary.BigEndian.Uint32(hash[:4])
	
	// Calculate offset within allowed range
	// Use a larger range (30100-30399) for multi-validator setup
	offset := hashNum % 300
	
	port := int32(GatewayBaseNodePort + offset)
	
	// Validate the port is within NodePort range
	if port < int32(constants.MinNodePort) || port > int32(constants.MaxNodePort) {
		return 0, fmt.Errorf("calculated NodePort %d is outside valid range (%d-%d)", port, constants.MinNodePort, constants.MaxNodePort)
	}
	
	return port, nil
}