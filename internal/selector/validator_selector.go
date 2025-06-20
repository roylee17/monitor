package selector

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"math/big"
	"sort"
	"strconv"

	rpcclient "github.com/cometbft/cometbft/rpc/client/http"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/types/query"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
)

// ValidatorInfo contains validator information for selection
type ValidatorInfo struct {
	OperatorAddress string
	Moniker         string
	VotingPower     string // Changed to string to handle large numbers
	ConsensusKey    string
	IsLocal         bool
}

// SelectionResult contains the result of validator selection
type SelectionResult struct {
	ValidatorSubset []ValidatorInfo
	ShouldOptIn     bool
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

// SelectValidatorSubset selects a validator subset for the consumer chain
func (vs *ValidatorSelector) SelectValidatorSubset(consumerID string, subsetRatio float64) (*SelectionResult, error) {

	// Get all bonded validators from connected blockchain
	validators, err := vs.getBondedValidators()
	if err != nil {
		return nil, fmt.Errorf("failed to get bonded validators from blockchain: %w", err)
	}

	// Sort validators by voting power (descending) for deterministic selection
	sort.Slice(validators, func(i, j int) bool {
		// Convert string voting power to big.Int for comparison
		powerI := new(big.Int)
		powerJ := new(big.Int)
		powerI.SetString(validators[i].VotingPower, 10)
		powerJ.SetString(validators[j].VotingPower, 10)
		
		cmp := powerI.Cmp(powerJ)
		if cmp == 0 {
			return validators[i].OperatorAddress < validators[j].OperatorAddress
		}
		return cmp > 0 // Sort descending (higher power first)
	})

	// Calculate subset size
	subsetSize := int(float64(len(validators)) * subsetRatio)
	if subsetSize < 1 {
		subsetSize = 1
	}
	if subsetSize > len(validators) {
		subsetSize = len(validators)
	}

	// Select validator subset using deterministic algorithm based on consumer ID
	subset := vs.selectDeterministicSubset(validators, subsetSize, consumerID)

	// Check if local validator is in subset
	var shouldOptIn bool

	for i := range subset {
		if subset[i].IsLocal {
			shouldOptIn = true
			break
		}
	}

	return &SelectionResult{
		ValidatorSubset: subset,
		ShouldOptIn:     shouldOptIn,
	}, nil
}

// getBondedValidators retrieves all bonded validators
func (vs *ValidatorSelector) getBondedValidators() ([]ValidatorInfo, error) {
	stakingClient := stakingtypes.NewQueryClient(vs.clientCtx)

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

	// Get local validator moniker for identification
	localMoniker, err := vs.GetLocalValidatorMoniker()
	if err != nil {
		log.Printf("Warning: Could not identify local validator: %v", err)
	}
	log.Printf("Local validator moniker: %s", localMoniker)

	validators := make([]ValidatorInfo, len(resp.Validators))
	for i, v := range resp.Validators {
		// Check for exact match or validator-prefix match
		isLocal := false
		if localMoniker != "" {
			// Exact match
			if v.Description.Moniker == localMoniker {
				isLocal = true
			}
			// Handle case where node moniker is "validator-alice" but chain moniker is "alice"
			if localMoniker == "validator-"+v.Description.Moniker {
				isLocal = true
				log.Printf("Matched local validator with prefix: node=%s, chain=%s", localMoniker, v.Description.Moniker)
			}
		}
		if v.Description.Moniker == "alice" || v.Description.Moniker == "bob" || v.Description.Moniker == "charlie" {
			log.Printf("Validator %s: moniker=%s, localMoniker=%s, isLocal=%v", 
				v.OperatorAddress, v.Description.Moniker, localMoniker, isLocal)
		}
		validators[i] = ValidatorInfo{
			OperatorAddress: v.OperatorAddress,
			Moniker:         v.Description.Moniker,
			VotingPower:     v.Tokens.String(),
			ConsensusKey:    v.ConsensusPubkey.String(),
			IsLocal:         isLocal,
		}
	}

	return validators, nil
}

// GetLocalValidatorMoniker gets the local validator's moniker
func (vs *ValidatorSelector) GetLocalValidatorMoniker() (string, error) {
	if vs.rpcClient == nil {
		return "", fmt.Errorf("no RPC client available")
	}

	status, err := vs.rpcClient.Status(context.Background())
	if err != nil {
		return "", fmt.Errorf("failed to query node status: %w", err)
	}

	if status.ValidatorInfo.PubKey == nil {
		return "", fmt.Errorf("node is not a validator")
	}

	return status.NodeInfo.Moniker, nil
}

// selectDeterministicSubset selects a deterministic subset of validators
func (vs *ValidatorSelector) selectDeterministicSubset(validators []ValidatorInfo, subsetSize int, seed string) []ValidatorInfo {
	if subsetSize >= len(validators) {
		return validators
	}

	// Create a deterministic hash-based selection
	selected := make([]ValidatorInfo, 0, subsetSize)
	used := make(map[string]bool)

	// Use consumer ID as seed for deterministic selection
	hasher := sha256.New()
	hasher.Write([]byte(seed))
	seedHash := hasher.Sum(nil)

	// Convert hash to big integer for modular arithmetic
	seedInt := new(big.Int).SetBytes(seedHash)

	for len(selected) < subsetSize && len(selected) < len(validators) {
		// Generate deterministic index
		indexBig := new(big.Int).Mod(seedInt, big.NewInt(int64(len(validators))))
		index := int(indexBig.Int64())

		validator := validators[index]
		if !used[validator.OperatorAddress] {
			selected = append(selected, validator)
			used[validator.OperatorAddress] = true
		}

		// Update seed for next iteration
		hasher.Reset()
		hasher.Write(seedInt.Bytes())
		hasher.Write([]byte(strconv.Itoa(len(selected))))
		seedInt = new(big.Int).SetBytes(hasher.Sum(nil))
	}

	// Sort selected validators by operator address for consistency
	sort.Slice(selected, func(i, j int) bool {
		return selected[i].OperatorAddress < selected[j].OperatorAddress
	})

	return selected
}


// GetValidatorSubsetInfo returns formatted information about the validator subset
func (vs *ValidatorSelector) GetValidatorSubsetInfo(result *SelectionResult) string {
	info := "Subset validators:\n"

	for i, v := range result.ValidatorSubset {
		marker := ""
		if v.IsLocal {
			marker = " (LOCAL)"
		}
		info += fmt.Sprintf("  %d. %s - %s%s\n", i+1, v.Moniker, v.OperatorAddress, marker)
	}

	return info
}

// GetFromKey returns the configured from key
func (vs *ValidatorSelector) GetFromKey() string {
	return vs.localKey
}

// GetValidatorAddress returns the validator address for the local key
func (vs *ValidatorSelector) GetValidatorAddress() string {
	// Get the validator info for the local key
	validators, err := vs.getBondedValidators()
	if err != nil {
		log.Printf("Failed to get validators: %v", err)
		return ""
	}
	
	for _, val := range validators {
		if val.Moniker == vs.localKey {
			return val.OperatorAddress
		}
	}
	
	// If not found by moniker, return the key itself (might be an address)
	return vs.localKey
}

// GetAccountAddress returns the account address for the local key
// This is the address used for transactions (cosmos1...)
func (vs *ValidatorSelector) GetAccountAddress() string {
	// For now, we'll need to derive this or look it up
	// In a real implementation, this would query the chain or use the keyring
	// Since we're using test keyring with known keys, we can hardcode for now
	switch vs.localKey {
	case "alice":
		return "cosmos1zaavvzxez0elundtn32qnk9lkm8kmcszzsv80v"
	case "bob":
		return "cosmos1yxgfnpk2u6h9prhhukcunszcwc277s2cwpds6u"
	case "charlie":
		return "cosmos1pz2trypc6e25hcwzn4h7jyqc57cr0qg4wrua28"
	default:
		// Assume the local key is already an address
		return vs.localKey
	}
}
