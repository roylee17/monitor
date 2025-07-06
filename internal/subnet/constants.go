package subnet

import "time"

// Network constants
const (
	// Default denomination for tokens
	DefaultDenom = "stake"

	// Default token amounts
	DefaultSupply       = 1000000000000 // 1 trillion
	DefaultRelayerFunds = 100000000     // 100 million

	// Timeout durations
	DefaultDeployTimeout  = 2 * time.Minute
	DefaultPollInterval   = 5 * time.Second
	DefaultTickerInterval = 30 * time.Second

	// Container configuration
	DefaultReplicas      = 1
	DefaultConsumerImage = "ics-monitor:latest"

	// Resource naming patterns
	// Since each validator has its own cluster, no need for validator prefixes
	ConfigMapNameFormat  = "%s-config" // chainID-config
	DeploymentNameFormat = "%s"        // chainID

	// Volume paths
	ConfigVolumePath = "/consumer/.interchain-security-cd"
	DataVolumePath   = "/consumer/data"

	// Command paths
	ConsumerBinaryPath = "/usr/local/bin/interchain-security-cd"

	// Genesis file paths
	PreGenesisFile = "config/priv_validator_state.json"
	GenesisFile    = "config/genesis.json"
	NodeKeyFile    = "config/node_key.json"

	// Default namespace prefix
	DefaultNamespacePrefix = "consumer"

	// Deployment labels
	LabelAppPart     = "app.kubernetes.io/part-of"
	LabelAppName     = "app.kubernetes.io/name"
	LabelAppInstance = "app.kubernetes.io/instance"
	LabelManagedBy   = "app.kubernetes.io/managed-by"
	LabelChainID     = "cosmos.network/chain-id"
	LabelConsumerID  = "cosmos.network/consumer-id"

	// Label values
	LabelPartOfConsumerChains = "consumer-chains"
	LabelManagedByMonitor     = "ics-monitor"

	// Peer discovery
	ServiceDNSFormat = "%s.%s.svc.cluster.local" // service.namespace.svc.cluster.local
	NodeIDPrefix     = "ics-consumer"

	// Genesis time base
	GenesisTimeBaseYear  = 2025
	GenesisTimeBaseMonth = 1
	GenesisTimeBaseDay   = 1
)
