package subnet

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"time"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	genutiltypes "github.com/cosmos/cosmos-sdk/x/genutil/types"
)

// RelayerAccount represents a relayer account to be funded
type RelayerAccount struct {
	Address string
	Coins   sdk.Coins
}

// PreCCVGenesis represents the pre-CCV genesis structure
type PreCCVGenesis struct {
	GenesisTime   time.Time                  `json:"genesis_time"`
	ChainID       string                     `json:"chain_id"`
	InitialHeight string                     `json:"initial_height"`
	AppState      map[string]json.RawMessage `json:"app_state"`
}

// calculateDeterministicTime generates a deterministic genesis time based on chain ID
func calculateDeterministicTime(chainID string) time.Time {
	chainIDHash := fnv.New32a()
	chainIDHash.Write([]byte(chainID))
	deterministicOffset := chainIDHash.Sum32()
	
	baseTime := time.Date(GenesisTimeBaseYear, GenesisTimeBaseMonth, GenesisTimeBaseDay, 0, 0, 0, 0, time.UTC)
	return baseTime.Add(time.Duration(deterministicOffset) * time.Second)
}

// CreatePreCCVGenesis creates a pre-CCV genesis with funded relayer accounts
func CreatePreCCVGenesis(chainID string, validatorName string, relayerAccounts []RelayerAccount) (*PreCCVGenesis, error) {
	genesisTime := calculateDeterministicTime(chainID)
	
	appState := make(map[string]json.RawMessage)
	
	// Create auth genesis state
	authGenesis := authtypes.NewGenesisState(
		authtypes.DefaultParams(),
		[]authtypes.GenesisAccount{},
	)
	
	// Create bank genesis state with relayer balances
	balances := []banktypes.Balance{}
	for _, relayer := range relayerAccounts {
		balances = append(balances, banktypes.Balance{
			Address: relayer.Address,
			Coins:   relayer.Coins,
		})
	}
	
	// Add default supply
	totalSupply := sdk.NewCoins(sdk.NewCoin(DefaultDenom, math.NewInt(DefaultSupply)))
	for _, balance := range balances {
		totalSupply = totalSupply.Add(balance.Coins...)
	}
	
	bankGenesis := banktypes.NewGenesisState(
		banktypes.DefaultParams(),
		balances,
		totalSupply,
		[]banktypes.Metadata{},
		[]banktypes.SendEnabled{},
	)
	
	// Create genutil genesis state (empty for pre-CCV)
	genutilGenesis := genutiltypes.NewGenesisState([]json.RawMessage{})
	
	// Marshal genesis states
	authGenesisJSON, err := json.Marshal(authGenesis)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal auth genesis: %w", err)
	}
	
	bankGenesisJSON, err := json.Marshal(bankGenesis)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal bank genesis: %w", err)
	}
	
	genutilGenesisJSON, err := json.Marshal(genutilGenesis)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal genutil genesis: %w", err)
	}
	
	// Set app state
	appState["auth"] = authGenesisJSON
	appState["bank"] = bankGenesisJSON
	appState["genutil"] = genutilGenesisJSON
	
	return &PreCCVGenesis{
		GenesisTime:   genesisTime,
		ChainID:       chainID,
		InitialHeight: "1",
		AppState:      appState,
	}, nil
}