package subnet

import (
	"encoding/json"
	"fmt"
	"time"
	"hash/fnv"
	
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	genutiltypes "github.com/cosmos/cosmos-sdk/x/genutil/types"
)

// PreCCVGenesis represents the pre-CCV genesis structure
type PreCCVGenesis struct {
	GenesisTime   time.Time                  `json:"genesis_time"`
	ChainID       string                     `json:"chain_id"`
	InitialHeight string                     `json:"initial_height"`
	AppState      map[string]json.RawMessage `json:"app_state"`
}

// RelayerAccount represents a relayer account to be funded
type RelayerAccount struct {
	Address string
	Coins   sdk.Coins
}

// calculateDeterministicTime generates a deterministic genesis time based on chain ID
func calculateDeterministicTime(chainID string) time.Time {
	// Use a fixed offset from Unix epoch based on chain ID hash to ensure
	// all monitors generate identical genesis
	chainIDHash := fnv.New32a()
	chainIDHash.Write([]byte(chainID))
	deterministicOffset := chainIDHash.Sum32()
	
	// Use a base time (e.g., 2025-01-01) plus deterministic offset in seconds
	baseTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	return baseTime.Add(time.Duration(deterministicOffset) * time.Second)
}

// CreatePreCCVGenesis creates a pre-CCV genesis with funded relayer accounts
func CreatePreCCVGenesis(chainID string, validatorName string, relayerAccounts []RelayerAccount) (*PreCCVGenesis, error) {
	// Use deterministic genesis time based on chain ID
	genesisTime := calculateDeterministicTime(chainID)
	
	// Initialize app state modules
	appState := make(map[string]json.RawMessage)
	
	// Create auth genesis state
	authGenesis := authtypes.DefaultGenesisState()
	
	// Create bank genesis state with funded accounts
	balances := []banktypes.Balance{}
	for _, relayer := range relayerAccounts {
		// Add to balances
		balances = append(balances, banktypes.Balance{
			Address: relayer.Address,
			Coins:   relayer.Coins,
		})
	}
	
	bankGenesis := banktypes.GenesisState{
		Params: banktypes.DefaultParams(),
		Balances: balances,
		Supply: calculateTotalSupply(balances),
		DenomMetadata: []banktypes.Metadata{
			{
				Description: "The native staking token of the consumer chain",
				DenomUnits: []*banktypes.DenomUnit{
					{Denom: "stake", Exponent: 0, Aliases: []string{"ustake"}},
				},
				Base:    "stake",
				Display: "stake",
				Name:    "Stake",
				Symbol:  "STAKE",
			},
		},
	}
	
	// Marshal auth state
	authGenesisJSON, err := json.Marshal(authGenesis)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal auth genesis: %w", err)
	}
	appState[authtypes.ModuleName] = authGenesisJSON
	
	// Marshal bank state
	bankGenesisJSON, err := json.Marshal(bankGenesis)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal bank genesis: %w", err)
	}
	appState[banktypes.ModuleName] = bankGenesisJSON
	
	// Add genutil module (required but can be empty)
	genutilGenesis := genutiltypes.GenesisState{
		GenTxs: []json.RawMessage{},
	}
	genutilGenesisJSON, err := json.Marshal(genutilGenesis)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal genutil genesis: %w", err)
	}
	appState[genutiltypes.ModuleName] = genutilGenesisJSON
	
	// Create the pre-CCV genesis
	genesis := &PreCCVGenesis{
		GenesisTime:   genesisTime,
		ChainID:       chainID,
		InitialHeight: "1",
		AppState:      appState,
	}
	
	return genesis, nil
}

// calculateTotalSupply calculates the total supply from balances
func calculateTotalSupply(balances []banktypes.Balance) sdk.Coins {
	total := sdk.NewCoins()
	for _, balance := range balances {
		total = total.Add(balance.Coins...)
	}
	return total
}

// MergeWithCCVGenesis merges pre-CCV genesis with CCV genesis patch
func MergeWithCCVGenesis(preCCV *PreCCVGenesis, ccvPatch map[string]interface{}) (map[string]interface{}, error) {
	// Convert pre-CCV genesis to map
	preCCVBytes, err := json.Marshal(preCCV)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal pre-CCV genesis: %w", err)
	}
	
	var genesisMap map[string]interface{}
	if err := json.Unmarshal(preCCVBytes, &genesisMap); err != nil {
		return nil, fmt.Errorf("failed to unmarshal pre-CCV genesis: %w", err)
	}
	
	// Extract ccvconsumer module from CCV patch
	if ccvConsumer, ok := ccvPatch["app_state"].(map[string]interface{})["ccvconsumer"]; ok {
		// Add ccvconsumer to app_state
		if appState, ok := genesisMap["app_state"].(map[string]interface{}); ok {
			appState["ccvconsumer"] = ccvConsumer
		}
	}
	
	// Also merge provider info if present
	if provider, ok := ccvPatch["provider"]; ok {
		genesisMap["provider"] = provider
	}
	
	return genesisMap, nil
}

// GenerateRelayerAddress generates a deterministic relayer address for a validator
func GenerateRelayerAddress(validatorName string, chainID string) string {
	// For now, use a simple deterministic approach
	// In production, this should use proper key derivation
	_ = fmt.Sprintf("%s-%s-relayer", validatorName, chainID) // seed for future use
	
	// This is a placeholder - in reality, you'd derive a proper address
	// For testing, we'll use predictable addresses
	switch validatorName {
	case "alice":
		return "consumer1r5v5srda7xfth3hn2s26txvrcrntldjumt8mhl"
	case "bob":
		return "consumer1ay37rp2pc3kjarg7a322vu3sa8j9puah8g0d4"
	case "charlie":
		return "consumer1s9dzsqrrq0jj5gea0exlpfwy9asp5nqjdy3af"
	default:
		// Generate based on validator name
		return fmt.Sprintf("consumer1%s", validatorName[:10])
	}
}