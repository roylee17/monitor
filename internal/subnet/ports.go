package subnet

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"

	"github.com/sourcenetwork/ics-operator/internal/constants"
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

// ReservedPortRange defines a range of reserved ports
type ReservedPortRange struct {
	Start int
	End   int
	Name  string
}

// Common reserved port ranges
var ReservedPorts = []ReservedPortRange{
	{Start: 0, End: 1023, Name: "System ports"},
	{Start: 3000, End: 3000, Name: "Common development servers"},
	{Start: 5432, End: 5432, Name: "PostgreSQL"},
	{Start: 6379, End: 6379, Name: "Redis"},
	{Start: 8080, End: 8080, Name: "HTTP alternate"},
	{Start: 27017, End: 27017, Name: "MongoDB"},
}

// IsPortReserved checks if a port is in a reserved range
func IsPortReserved(port int) (bool, string) {
	for _, reserved := range ReservedPorts {
		if port >= reserved.Start && port <= reserved.End {
			return true, reserved.Name
		}
	}
	return false, ""
}

// ValidatePorts checks if all ports are valid and not in reserved ranges
func ValidatePorts(ports *Ports) error {
	portChecks := []struct {
		port int
		name string
	}{
		{ports.P2P, "P2P"},
		{ports.RPC, "RPC"},
		{ports.GRPC, "gRPC"},
		{ports.GRPCWeb, "gRPC-Web"},
	}

	for _, check := range portChecks {
		if check.port < constants.MinUserPort || check.port > constants.MaxUserPort {
			return fmt.Errorf("%s port %d is out of valid range (%d-%d)", check.name, check.port, constants.MinUserPort, constants.MaxUserPort)
		}

		if reserved, service := IsPortReserved(check.port); reserved {
			return fmt.Errorf("%s port %d conflicts with reserved port for %s", check.name, check.port, service)
		}
	}

	// Check for internal collisions
	portSet := make(map[int]string)
	for _, check := range portChecks {
		if existing, exists := portSet[check.port]; exists {
			return fmt.Errorf("port collision: %s and %s both use port %d", existing, check.name, check.port)
		}
		portSet[check.port] = check.name
	}

	return nil
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

	// Validate the calculated ports
	if err := ValidatePorts(ports); err != nil {
		return nil, fmt.Errorf("calculated ports for chain %s are invalid: %w", chainID, err)
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
