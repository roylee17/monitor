package constants

// Port range constants
const (
	// Valid port range for user applications
	MinUserPort = 1024
	MaxUserPort = 65535
	
	// NodePort range for Kubernetes services
	MinNodePort = 30000
	MaxNodePort = 32767
	
	// Consumer chain port calculation limits
	MaxConsumerOffset = 10000 // Maximum offset to prevent port overflow
)