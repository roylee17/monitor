package constants

import "time"

// Time-related constants used throughout the application
const (
	// Polling intervals
	DefaultPollInterval   = 5 * time.Second
	LongPollInterval      = 30 * time.Second
	HealthCheckInterval   = 30 * time.Second
	StatusCheckInterval   = 30 * time.Second
	
	// Timeouts
	DefaultTimeout        = 2 * time.Minute
	DeploymentTimeout     = 5 * time.Minute
	ShortTimeout          = 30 * time.Second
	
	// Retry configuration
	DefaultRetryAttempts  = 3
	DefaultRetryDelay     = 2 * time.Second
	MaxRetryDelay         = 30 * time.Second
	
	// Consumer chain specific
	SpawnTimeBuffer       = 30 * time.Second  // Buffer before spawn time to start monitoring
	PostSpawnGracePeriod  = 5 * time.Minute   // Grace period after spawn time
)