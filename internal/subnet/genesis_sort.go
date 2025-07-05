package subnet

import (
	"encoding/json"
	"sort"
)

// sortGenesisFields recursively sorts all fields in a genesis structure to ensure
// deterministic JSON marshaling. This is critical for ensuring all validators
// produce identical genesis files.
func sortGenesisFields(data interface{}) interface{} {
	switch v := data.(type) {
	case map[string]interface{}:
		// Sort map keys
		sorted := make(map[string]interface{})
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		
		// Recursively sort values
		for _, k := range keys {
			sorted[k] = sortGenesisFields(v[k])
		}
		return sorted
		
	case []interface{}:
		// For arrays, recursively sort each element
		sorted := make([]interface{}, len(v))
		for i, item := range v {
			sorted[i] = sortGenesisFields(item)
		}
		
		// Special handling for specific arrays that need sorting
		if len(sorted) > 0 {
			// Sort validator arrays by address/moniker
			if isValidatorArray(sorted) {
				sortValidatorArray(sorted)
			}
			// Sort account arrays by address
			if isAccountArray(sorted) {
				sortAccountArray(sorted)
			}
			// Sort balance arrays by address
			if isBalanceArray(sorted) {
				sortBalanceArray(sorted)
			}
		}
		
		return sorted
		
	default:
		// Return primitives as-is
		return v
	}
}

// isValidatorArray checks if the array contains validator objects
func isValidatorArray(arr []interface{}) bool {
	if len(arr) == 0 {
		return false
	}
	
	// Check if first element looks like a validator
	if obj, ok := arr[0].(map[string]interface{}); ok {
		_, hasAddress := obj["address"]
		_, hasPubKey := obj["pub_key"]
		_, hasVotingPower := obj["voting_power"]
		return (hasAddress && hasPubKey) || hasVotingPower
	}
	return false
}

// sortValidatorArray sorts validators by address
func sortValidatorArray(arr []interface{}) {
	sort.Slice(arr, func(i, j int) bool {
		vi, _ := arr[i].(map[string]interface{})
		vj, _ := arr[j].(map[string]interface{})
		
		// Try different address fields
		addri := getStringField(vi, "address", "operator_address", "validator_address")
		addrj := getStringField(vj, "address", "operator_address", "validator_address")
		
		return addri < addrj
	})
}

// isAccountArray checks if the array contains account objects
func isAccountArray(arr []interface{}) bool {
	if len(arr) == 0 {
		return false
	}
	
	// Check if first element looks like an account
	if obj, ok := arr[0].(map[string]interface{}); ok {
		_, hasAddress := obj["address"]
		_, hasType := obj["@type"]
		return hasAddress && hasType
	}
	return false
}

// sortAccountArray sorts accounts by address
func sortAccountArray(arr []interface{}) {
	sort.Slice(arr, func(i, j int) bool {
		ai, _ := arr[i].(map[string]interface{})
		aj, _ := arr[j].(map[string]interface{})
		
		addri, _ := ai["address"].(string)
		addrj, _ := aj["address"].(string)
		
		return addri < addrj
	})
}

// isBalanceArray checks if the array contains balance objects
func isBalanceArray(arr []interface{}) bool {
	if len(arr) == 0 {
		return false
	}
	
	// Check if first element looks like a balance entry
	if obj, ok := arr[0].(map[string]interface{}); ok {
		_, hasAddress := obj["address"]
		_, hasCoins := obj["coins"]
		return hasAddress && hasCoins
	}
	return false
}

// sortBalanceArray sorts balances by address
func sortBalanceArray(arr []interface{}) {
	sort.Slice(arr, func(i, j int) bool {
		bi, _ := arr[i].(map[string]interface{})
		bj, _ := arr[j].(map[string]interface{})
		
		addri, _ := bi["address"].(string)
		addrj, _ := bj["address"].(string)
		
		return addri < addrj
	})
}

// getStringField tries multiple field names and returns the first non-empty string
func getStringField(obj map[string]interface{}, fields ...string) string {
	for _, field := range fields {
		if val, ok := obj[field].(string); ok && val != "" {
			return val
		}
	}
	return ""
}

// MarshalSortedJSON marshals data to JSON with sorted fields for deterministic output
func MarshalSortedJSON(data interface{}) ([]byte, error) {
	sorted := sortGenesisFields(data)
	return json.MarshalIndent(sorted, "", "  ")
}