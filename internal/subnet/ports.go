package subnet

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
)

// Port constants for deterministic allocation
const (
	// Base ports for provider chain
	BaseP2PPort     = 26656
	BaseRPCPort     = 26657
	BaseGRPCPort    = 9090
	BaseGRPCWebPort = 9091

	// Offset for consumer chains
	ConsumerOffset = 100

	// Port spacing between consumer chains
	PortSpacing = 10

	// Maximum consumer chain instances
	MaxConsumers = 1000
)

// Ports represents the network ports for a consumer chain
type Ports struct {
	P2P     int
	RPC     int
	GRPC    int
	GRPCWeb int
}

// CalculatePorts returns deterministic ports for a consumer chain
func CalculatePorts(chainID string) (*Ports, error) {
	// Calculate hash of chain ID
	hash := sha256.Sum256([]byte(chainID))
	
	// Use first 8 bytes as uint64
	hashNum := binary.BigEndian.Uint64(hash[:8])
	
	// Calculate offset (0-999)
	offset := int(hashNum % MaxConsumers)
	
	// Calculate ports with deterministic formula
	// Port = BasePort + ConsumerOffset + (offset * PortSpacing)
	ports := &Ports{
		P2P:     BaseP2PPort + ConsumerOffset + (offset * PortSpacing),
		RPC:     BaseRPCPort + ConsumerOffset + (offset * PortSpacing),
		GRPC:    BaseGRPCPort + ConsumerOffset + (offset * PortSpacing),
		GRPCWeb: BaseGRPCWebPort + ConsumerOffset + (offset * PortSpacing),
	}
	
	return ports, nil
}

// GetProviderPorts returns the standard provider chain ports
func GetProviderPorts() *Ports {
	return &Ports{
		P2P:     BaseP2PPort,
		RPC:     BaseRPCPort,
		GRPC:    BaseGRPCPort,
		GRPCWeb: BaseGRPCWebPort,
	}
}

// FormatP2PAddress formats a P2P address with node ID
func FormatP2PAddress(nodeID, host string, port int) string {
	return fmt.Sprintf("%s@%s:%d", nodeID, host, port)
}

// FormatRPCAddress formats an RPC address
func FormatRPCAddress(host string, port int) string {
	return fmt.Sprintf("http://%s:%d", host, port)
}

// FormatGRPCAddress formats a gRPC address
func FormatGRPCAddress(host string, port int) string {
	return fmt.Sprintf("%s:%d", host, port)
}