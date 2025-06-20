package subnet

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// ProductionConfig holds configuration for production peer discovery
type ProductionConfig struct {
	// Validator identity
	ValidatorName string
	
	// External addresses where this validator's services are reachable
	ProviderExternalAddress  string // e.g., "alice.validators.com:26656"
	ConsumerExternalTemplate string // e.g., "alice-{{consumer-id}}.validators.com:{{port}}"
	
	// Peer discovery method
	DiscoveryMethod string // "registry", "dns", "static", "blockchain"
	
	// Registry configuration (if using registry)
	RegistryType     string // "file", "s3", "redis", "database"
	RegistryEndpoint string // File path, S3 bucket, Redis URL, etc.
	
	// Static peers (if using static discovery)
	StaticPeers map[string]ValidatorEndpoints // map[validator]endpoints
	
	// DNS configuration (if using DNS discovery)
	DNSTemplate string // e.g., "{{consumer-id}}.{{validator}}.validators.com"
	
	// Infrastructure metadata
	Infrastructure InfrastructureInfo
}

// ValidatorEndpoints contains endpoint templates for a validator
type ValidatorEndpoints struct {
	ProviderAddress  string
	ConsumerTemplate string // Template with {{consumer-id}} and {{port}} placeholders
	NodeID           string // Can be empty if discovered dynamically
}

// InfrastructureInfo contains information about validator infrastructure
type InfrastructureInfo struct {
	Provider   string // "aws", "gcp", "azure", "bare-metal", etc.
	Region     string // "us-east-1", "europe-west1", etc.
	Datacenter string // Custom datacenter identifier
}

// GetConsumerP2PAddress returns the external P2P address for a consumer chain
func (pc *ProductionConfig) GetConsumerP2PAddress(consumerID string, port int) string {
	address := pc.ConsumerExternalTemplate
	address = strings.ReplaceAll(address, "{{consumer-id}}", consumerID)
	address = strings.ReplaceAll(address, "{{port}}", fmt.Sprintf("%d", port))
	return address
}

// LoadProductionConfig loads configuration from environment variables
func LoadProductionConfig() *ProductionConfig {
	config := &ProductionConfig{
		ValidatorName:           os.Getenv("VALIDATOR_NAME"),
		ProviderExternalAddress: os.Getenv("PROVIDER_EXTERNAL_ADDRESS"),
		ConsumerExternalTemplate: os.Getenv("CONSUMER_EXTERNAL_TEMPLATE"),
		DiscoveryMethod:         os.Getenv("PEER_DISCOVERY_METHOD"),
		RegistryType:            os.Getenv("PEER_REGISTRY_TYPE"),
		RegistryEndpoint:        os.Getenv("PEER_REGISTRY_ENDPOINT"),
		DNSTemplate:             os.Getenv("PEER_DNS_TEMPLATE"),
		Infrastructure: InfrastructureInfo{
			Provider:   os.Getenv("INFRA_PROVIDER"),
			Region:     os.Getenv("INFRA_REGION"),
			Datacenter: os.Getenv("INFRA_DATACENTER"),
		},
	}
	
	// Set defaults
	if config.DiscoveryMethod == "" {
		config.DiscoveryMethod = "registry"
	}
	if config.RegistryType == "" {
		config.RegistryType = "file"
	}
	if config.ConsumerExternalTemplate == "" {
		// Default template assumes subdomain-based routing
		config.ConsumerExternalTemplate = config.ValidatorName + "-{{consumer-id}}.validators.com:{{port}}"
	}
	
	return config
}

// Example Production Configurations:

// 1. Large Validator with Multiple Regions:
// VALIDATOR_NAME=alice
// PROVIDER_EXTERNAL_ADDRESS=alice.validators.com:26656
// CONSUMER_EXTERNAL_TEMPLATE=alice-{{consumer-id}}.{{region}}.validators.com:{{port}}
// PEER_DISCOVERY_METHOD=registry
// PEER_REGISTRY_TYPE=database
// PEER_REGISTRY_ENDPOINT=postgres://peer-registry.internal:5432/peers
// INFRA_PROVIDER=aws
// INFRA_REGION=us-east-1

// 2. Medium Validator with Static IPs:
// VALIDATOR_NAME=bob
// PROVIDER_EXTERNAL_ADDRESS=203.0.113.10:26656
// CONSUMER_EXTERNAL_TEMPLATE=203.0.113.10:{{port}}
// PEER_DISCOVERY_METHOD=registry
// PEER_REGISTRY_TYPE=redis
// PEER_REGISTRY_ENDPOINT=redis://redis.bob-infra.local:6379
// INFRA_PROVIDER=bare-metal
// INFRA_DATACENTER=dc1

// 3. Small Validator with Simple Setup:
// VALIDATOR_NAME=charlie
// PROVIDER_EXTERNAL_ADDRESS=charlie-validator.example.org:26656
// CONSUMER_EXTERNAL_TEMPLATE=charlie-validator.example.org:{{port}}
// PEER_DISCOVERY_METHOD=dns
// PEER_DNS_TEMPLATE={{consumer-id}}.{{validator}}.validators.zone
// INFRA_PROVIDER=gcp
// INFRA_REGION=europe-west1

// ProductionPeerManager orchestrates peer discovery in production
type ProductionPeerManager struct {
	config           *ProductionConfig
	registry         *PeerRegistry
	nodeDiscovery    *NodeDiscovery
	discoveryService *PeerDiscoveryService
}

// NewProductionPeerManager creates a new production peer manager
func NewProductionPeerManager(config *ProductionConfig) (*ProductionPeerManager, error) {
	// Create persistence based on config
	var persistence PeerPersistence
	var err error
	
	switch config.RegistryType {
	case "file":
		persistence, err = NewFilePeerPersistence(config.RegistryEndpoint)
		if err != nil {
			return nil, fmt.Errorf("failed to create file persistence: %w", err)
		}
	// case "s3":
	//     persistence = NewS3PeerPersistence(config.RegistryEndpoint)
	// case "redis":
	//     persistence = NewRedisPeerPersistence(config.RegistryEndpoint)
	default:
		return nil, fmt.Errorf("unsupported registry type: %s", config.RegistryType)
	}
	
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	registry := NewPeerRegistry(logger, persistence)
	nodeDiscovery := NewNodeDiscovery(logger)
	
	// Determine external addresses
	externalP2P := config.GetConsumerP2PAddress("placeholder", 26656)
	externalRPC := config.GetConsumerP2PAddress("placeholder", 26657)
	
	discoveryService := NewPeerDiscoveryService(
		logger,
		registry,
		nodeDiscovery,
		config.ValidatorName,
		externalP2P,
		externalRPC,
	)
	
	return &ProductionPeerManager{
		config:           config,
		registry:         registry,
		nodeDiscovery:    nodeDiscovery,
		discoveryService: discoveryService,
	}, nil
}