package constants

import "time"

// Monitoring and polling intervals
const (
	// WebSocket monitoring
	WebSocketStatusInterval = 30 * time.Second
	WebSocketBlockLogInterval = 10 // Log every N blocks

	// Health monitoring  
	MonitorHealthCheckInterval = 30 * time.Second
	
	// Consumer monitoring
	ConsumerActivePollingInterval = 1 * time.Second   // When actively waiting for launch
	ConsumerNormalPollingInterval = 3 * time.Second   // After initial active period
	ConsumerPollingThreshold      = 30 * time.Second  // When to switch from active to normal
	ConsumerBackgroundInterval    = 30 * time.Second  // Background monitoring interval

	// Retry configurations
	MonitorMaxRetries    = 12
	MonitorRetryDelay    = 5 * time.Second
	MonitorMaxRetryDelay = 30 * time.Second
	
	// Background task retries
	BackgroundTaskMaxRetries = 60  // 5 minutes with 5s intervals
)