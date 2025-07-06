package constants

import "time"

// Consumer chain configuration constants
const (
	// Time periods for consumer chain configuration
	ConsumerUnbondingPeriod       = 20 * 24 * time.Hour              // 20 days
	ConsumerUnbondingPeriodStr    = "1728000s"                       // 20 days in seconds
	ConsumerCCVTimeoutPeriod      = 28 * 24 * time.Hour              // 28 days
	ConsumerCCVTimeoutPeriodStr   = "2419200s"                       // 28 days in seconds
	ConsumerTransferTimeout       = 30 * time.Minute                 // 30 minutes
	ConsumerTransferTimeoutStr    = "1800s"                          // 30 minutes in seconds
	ConsumerBlocksPerTransmission = 1000                             // Blocks between IBC transmissions
	ConsumerRedistributionFrac    = "0.75"                           // 75% redistribution fraction

	// Token amounts
	ValidatorInitialStake = "30000000000000000000" // 30 * 10^18 stake
	RelayerInitialFunds   = 100000000              // 100 million stake

	// Genesis configuration
	DefaultTrustLevel          = "1/3"
	DefaultMaxClockDrift       = 10 * time.Second
	DefaultMaxClockDriftStr    = "10s"
	DefaultSignedBlocksWindow  = 100
	DefaultMinSignedPerWindow  = "0.500000000000000000" // 50%
	DefaultDowntimeJailDuration = 10 * time.Minute
	DefaultDowntimeJailStr     = "600s"
	DefaultSlashFractionDouble = "0.050000000000000000" // 5%
	DefaultSlashFractionDown   = "0.010000000000000000" // 1%

	// Governance parameters
	DefaultGovMinDeposit        = "10000000" // 10 million stake
	DefaultGovMaxDepositPeriod  = 48 * time.Hour
	DefaultGovMaxDepositStr     = "172800s"  // 48 hours in seconds
	DefaultGovVotingPeriod      = 48 * time.Hour
	DefaultGovVotingPeriodStr   = "172800s"  // 48 hours in seconds
	
	// Provider chain parameters
	ProviderMaxValidators = 100
	ProviderHistoricalEntries = 10000
)

// Chain prefixes
const (
	CosmosAddressPrefix   = "cosmos"
	ConsumerAddressPrefix = "consumer"
)