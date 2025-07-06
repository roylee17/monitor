package selector

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"math/big"
	"sort"

	rpcclient "github.com/cometbft/cometbft/rpc/client/http"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/types/query"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
)

// ValidatorInfo contains validator information for selection
type ValidatorInfo struct {
	OperatorAddress string
	Moniker         string
	VotingPower     *big.Int // Changed to big.Int for accurate calculations
	ConsensusKey    string
	IsLocal         bool
}

// SelectionResult contains the result of validator selection
type SelectionResult struct {
	ValidatorSubset      []ValidatorInfo
	ShouldOptIn          bool
	TotalVotingPower     *big.Int
	SubsetVotingPower    *big.Int
	VotingPowerThreshold float64
}

// ValidatorSelector handles validator subset selection
type ValidatorSelector struct {
	clientCtx client.Context
	rpcClient *rpcclient.HTTP
	localKey  string
}

// NewValidatorSelector creates a new validator selector
func NewValidatorSelector(clientCtx client.Context, rpcClient *rpcclient.HTTP, localKey string) *ValidatorSelector {
	return &ValidatorSelector{
		clientCtx: clientCtx,
		rpcClient: rpcClient,
		localKey:  localKey,
	}
}

// SelectValidatorSubset determines if the local validator should opt-in based on voting power threshold
// The selection is deterministic - all monitors will make the same decision for who should opt-in
// NOTE: This function only determines WHO should opt-in. The actual opted-in validators are
// queried from the CCV patch after the consumer transitions to LAUNCHED phase.
func (vs *ValidatorSelector) SelectValidatorSubset(consumerID string, votingPowerThreshold float64) (*SelectionResult, error) {
	// Get all bonded validators from connected blockchain
	validators, err := vs.getBondedValidators()
	if err != nil {
		return nil, fmt.Errorf("failed to get bonded validators from blockchain: %w", err)
	}

	// Calculate total voting power
	totalVotingPower := big.NewInt(0)
	for _, v := range validators {
		totalVotingPower.Add(totalVotingPower, v.VotingPower)
	}

	// Calculate the voting power threshold
	thresholdPower := new(big.Float).Mul(
		new(big.Float).SetInt(totalVotingPower),
		big.NewFloat(votingPowerThreshold),
	)
	thresholdPowerInt, _ := thresholdPower.Int(nil)

	// DETERMINISTIC SELECTION: Sort validators by voting power (descending)
	// This ensures all monitors select the same high-power validators first
	sortedValidators := make([]ValidatorInfo, len(validators))
	copy(sortedValidators, validators)
	
	// Sort by voting power descending, with operator address as tiebreaker for determinism
	sort.Slice(sortedValidators, func(i, j int) bool {
		// First compare by voting power (descending)
		cmp := sortedValidators[i].VotingPower.Cmp(sortedValidators[j].VotingPower)
		if cmp > 0 {
			return true // i has more power than j
		} else if cmp < 0 {
			return false // j has more power than i
		}
		// If voting power is equal, use operator address as deterministic tiebreaker
		return sortedValidators[i].OperatorAddress < sortedValidators[j].OperatorAddress
	})

	// Check if local validator should opt-in by iterating through sorted validators
	accumulatedPower := big.NewInt(0)
	var localValidatorSelected bool
	var selectedCount int

	for _, validator := range sortedValidators {
		accumulatedPower.Add(accumulatedPower, validator.VotingPower)
		selectedCount++

		// Check if this is the local validator
		if validator.IsLocal {
			localValidatorSelected = true
		}

		// Check if we've reached the threshold
		// Continue until we exceed the threshold to ensure security
		if accumulatedPower.Cmp(thresholdPowerInt) >= 0 {
			break
		}
	}

	// Log the selection result (using stdlib log for now)
	log.Printf("Validator opt-in decision: consumer_id=%s, threshold=%.2f%%, local_validator_should_opt_in=%v, position=%d/%d",
		consumerID,
		votingPowerThreshold*100,
		localValidatorSelected,
		selectedCount,
		len(validators))

	// Build list of selected validators (for logging/display purposes only)
	// The actual validators who will run the consumer chain come from the CCV patch
	selectedValidators := make([]ValidatorInfo, 0, selectedCount)
	accumulatedPower = big.NewInt(0)
	
	for i, validator := range sortedValidators {
		if i >= selectedCount {
			break
		}
		selectedValidators = append(selectedValidators, validator)
		accumulatedPower.Add(accumulatedPower, validator.VotingPower)
	}

	return &SelectionResult{
		ValidatorSubset:      selectedValidators, // For logging/display only
		ShouldOptIn:          localValidatorSelected,
		TotalVotingPower:     totalVotingPower,
		SubsetVotingPower:    accumulatedPower,
		VotingPowerThreshold: votingPowerThreshold,
	}, nil
}

// SelectValidatorSubsetAtHeight determines if the local validator should opt-in based on voting power at a specific height
// This ensures all monitors see the same validator set by querying at the same block height
func (vs *ValidatorSelector) SelectValidatorSubsetAtHeight(consumerID string, votingPowerThreshold float64, height int64) (*SelectionResult, error) {
	// Get all bonded validators at specific height
	validators, err := vs.getBondedValidatorsAtHeight(height)
	if err != nil {
		return nil, fmt.Errorf("failed to get bonded validators at height %d: %w", height, err)
	}

	// Log that we're querying at specific height
	log.Printf("Selecting validators at height %d for consumer %s", height, consumerID)

	// Rest of the logic is the same as SelectValidatorSubset
	// Calculate total voting power
	totalVotingPower := big.NewInt(0)
	for _, v := range validators {
		totalVotingPower.Add(totalVotingPower, v.VotingPower)
	}

	// Calculate the voting power threshold
	thresholdPower := new(big.Float).Mul(
		new(big.Float).SetInt(totalVotingPower),
		big.NewFloat(votingPowerThreshold),
	)
	thresholdPowerInt, _ := thresholdPower.Int(nil)

	// DETERMINISTIC SELECTION: Sort validators by voting power (descending)
	sortedValidators := make([]ValidatorInfo, len(validators))
	copy(sortedValidators, validators)
	
	// Sort by voting power descending, with operator address as tiebreaker for determinism
	sort.Slice(sortedValidators, func(i, j int) bool {
		// First compare by voting power (descending)
		cmp := sortedValidators[i].VotingPower.Cmp(sortedValidators[j].VotingPower)
		if cmp > 0 {
			return true // i has more power than j
		} else if cmp < 0 {
			return false // j has more power than i
		}
		// If voting power is equal, use operator address as deterministic tiebreaker
		return sortedValidators[i].OperatorAddress < sortedValidators[j].OperatorAddress
	})

	// Check if local validator should opt-in by iterating through sorted validators
	accumulatedPower := big.NewInt(0)
	var localValidatorSelected bool
	var selectedCount int

	for _, validator := range sortedValidators {
		accumulatedPower.Add(accumulatedPower, validator.VotingPower)
		selectedCount++

		// Check if this is the local validator
		if validator.IsLocal {
			localValidatorSelected = true
		}

		// Check if we've reached the threshold
		if accumulatedPower.Cmp(thresholdPowerInt) >= 0 {
			break
		}
	}

	// Log the selection result
	log.Printf("Validator opt-in decision at height %d: consumer_id=%s, threshold=%.2f%%, local_validator_should_opt_in=%v, position=%d/%d",
		height,
		consumerID,
		votingPowerThreshold*100,
		localValidatorSelected,
		selectedCount,
		len(validators))

	// Build list of selected validators
	selectedValidators := make([]ValidatorInfo, 0, selectedCount)
	accumulatedPower = big.NewInt(0)
	
	for i, validator := range sortedValidators {
		if i >= selectedCount {
			break
		}
		selectedValidators = append(selectedValidators, validator)
		accumulatedPower.Add(accumulatedPower, validator.VotingPower)
	}

	return &SelectionResult{
		ValidatorSubset:      selectedValidators, // For logging/display only
		ShouldOptIn:          localValidatorSelected,
		TotalVotingPower:     totalVotingPower,
		SubsetVotingPower:    accumulatedPower,
		VotingPowerThreshold: votingPowerThreshold,
	}, nil
}

// getBondedValidators retrieves all bonded validators
func (vs *ValidatorSelector) getBondedValidators() ([]ValidatorInfo, error) {
	return vs.getBondedValidatorsAtHeight(0) // 0 means latest
}

// getBondedValidatorsAtHeight retrieves all bonded validators at a specific height
func (vs *ValidatorSelector) getBondedValidatorsAtHeight(height int64) ([]ValidatorInfo, error) {
	// Update client context with height if specified
	clientCtx := vs.clientCtx
	if height > 0 {
		clientCtx = clientCtx.WithHeight(height)
	}
	
	stakingClient := stakingtypes.NewQueryClient(clientCtx)

	// Query bonded validators
	resp, err := stakingClient.Validators(context.Background(), &stakingtypes.QueryValidatorsRequest{
		Status: "BOND_STATUS_BONDED",
		Pagination: &query.PageRequest{
			Limit: 1000, // Get all validators
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query validators: %w", err)
	}

	// Unpack interfaces for all validators to properly handle Any types
	for i := range resp.Validators {
		if err := resp.Validators[i].UnpackInterfaces(vs.clientCtx.InterfaceRegistry); err != nil {
			log.Printf("Warning: failed to unpack interfaces for validator %s: %v", 
				resp.Validators[i].Description.Moniker, err)
		}
	}

	// Get our account address from keyring - this is the source of truth
	localAccountAddr, err := vs.getLocalAccountAddress()
	if err != nil {
		return nil, fmt.Errorf("failed to get local account address: %w", err)
	}
	log.Printf("Local account address from keyring: %s (key: %s)", localAccountAddr, vs.localKey)

	validators := make([]ValidatorInfo, len(resp.Validators))
	for i, v := range resp.Validators {
		// Check if this is the local validator by comparing addresses
		isLocal := false
		
		// Convert validator operator address to account address for comparison
		valAddr, err := sdk.ValAddressFromBech32(v.OperatorAddress)
		if err != nil {
			return nil, fmt.Errorf("failed to parse validator address %s: %w", v.OperatorAddress, err)
		}
		accAddr := sdk.AccAddress(valAddr)
		
		// Debug logging
		log.Printf("Comparing addresses for %s: validator acc addr: %s, local acc addr: %s", 
			v.Description.Moniker, accAddr.String(), localAccountAddr)
		
		if accAddr.String() == localAccountAddr {
			isLocal = true
			log.Printf("Identified local validator: %s (address: %s)", v.Description.Moniker, localAccountAddr)
		}

		// Convert tokens to big.Int
		tokens, ok := new(big.Int).SetString(v.Tokens.String(), 10)
		if !ok {
			return nil, fmt.Errorf("failed to parse validator tokens: %s", v.Tokens.String())
		}

		// Extract consensus pubkey properly
		consensusKey := ""
		if pubKey, err := v.ConsPubKey(); err == nil {
			// Get the raw bytes of the public key and encode to base64
			consensusKey = base64.StdEncoding.EncodeToString(pubKey.Bytes())
		} else {
			log.Printf("Warning: failed to extract consensus pubkey for validator %s: %v", v.Description.Moniker, err)
		}

		validators[i] = ValidatorInfo{
			OperatorAddress: v.OperatorAddress,
			Moniker:         v.Description.Moniker,
			VotingPower:     tokens,
			ConsensusKey:    consensusKey,
			IsLocal:         isLocal,
		}
	}

	return validators, nil
}

// getLocalAccountAddress gets the account address from keyring
func (vs *ValidatorSelector) getLocalAccountAddress() (string, error) {
	if vs.clientCtx.Keyring == nil {
		return "", fmt.Errorf("keyring not available in client context")
	}
	
	if vs.localKey == "" {
		return "", fmt.Errorf("local key not configured")
	}
	
	keyInfo, err := vs.clientCtx.Keyring.Key(vs.localKey)
	if err != nil {
		return "", fmt.Errorf("failed to get key info for %s: %w", vs.localKey, err)
	}
	
	addr, err := keyInfo.GetAddress()
	if err != nil {
		return "", fmt.Errorf("failed to get address from key info: %w", err)
	}
	
	return addr.String(), nil
}


// GetValidatorSubsetInfo returns formatted information about the validator subset
func (vs *ValidatorSelector) GetValidatorSubsetInfo(result *SelectionResult) string {
	info := fmt.Sprintf("Validator Selection Summary:\n")
	info += fmt.Sprintf("  Total validators selected: %d\n", len(result.ValidatorSubset))
	info += fmt.Sprintf("  Total voting power: %s\n", result.TotalVotingPower.String())
	info += fmt.Sprintf("  Subset voting power: %s\n", result.SubsetVotingPower.String())
	
	// Calculate actual percentage
	if result.TotalVotingPower.Cmp(big.NewInt(0)) > 0 {
		percentage := new(big.Float).Quo(
			new(big.Float).SetInt(result.SubsetVotingPower),
			new(big.Float).SetInt(result.TotalVotingPower),
		)
		percentageFloat, _ := percentage.Float64()
		info += fmt.Sprintf("  Actual voting power percentage: %.2f%%\n", percentageFloat*100)
	}
	
	info += fmt.Sprintf("  Target threshold: %.2f%%\n", result.VotingPowerThreshold*100)
	info += fmt.Sprintf("  Local validator selected: %v\n\n", result.ShouldOptIn)
	
	info += "Selected validators:\n"
	for i, v := range result.ValidatorSubset {
		marker := ""
		if v.IsLocal {
			marker = " (LOCAL)"
		}
		info += fmt.Sprintf("  %d. %s - Power: %s%s\n", i+1, v.Moniker, v.VotingPower.String(), marker)
	}

	return info
}

// GetFromKey returns the configured from key
func (vs *ValidatorSelector) GetFromKey() string {
	return vs.localKey
}

// GetAllValidators returns all bonded validators
func (vs *ValidatorSelector) GetAllValidators(ctx context.Context) ([]ValidatorInfo, error) {
	return vs.getBondedValidators()
}

// GetValidatorAddress returns the validator address for the local key
func (vs *ValidatorSelector) GetValidatorAddress() string {
	// Get the validator info for the local key
	validators, err := vs.getBondedValidators()
	if err != nil {
		log.Fatalf("Failed to get validators: %v", err)
	}

	for _, val := range validators {
		if val.IsLocal {
			return val.OperatorAddress
		}
	}

	log.Fatalf("Local validator not found in bonded validators set")
	return "" // unreachable
}

// GetAccountAddress returns the account address for the local key
// This is the address used for transactions (cosmos1...)
func (vs *ValidatorSelector) GetAccountAddress() string {
	addr, err := vs.getLocalAccountAddress()
	if err != nil {
		log.Fatalf("Failed to get local account address: %v", err)
	}
	return addr
}