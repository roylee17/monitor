package subnet

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSortGenesisFields(t *testing.T) {
	// Test map sorting
	t.Run("map fields are sorted", func(t *testing.T) {
		input := map[string]interface{}{
			"z_field": "value1",
			"a_field": "value2",
			"m_field": "value3",
		}
		
		result := sortGenesisFields(input)
		
		// Marshal to JSON to check order
		jsonBytes, err := json.Marshal(result)
		require.NoError(t, err)
		
		expected := `{"a_field":"value2","m_field":"value3","z_field":"value1"}`
		require.Equal(t, expected, string(jsonBytes))
	})
	
	// Test nested map sorting
	t.Run("nested maps are sorted", func(t *testing.T) {
		input := map[string]interface{}{
			"outer_z": map[string]interface{}{
				"inner_z": 1,
				"inner_a": 2,
			},
			"outer_a": "value",
		}
		
		result := sortGenesisFields(input)
		jsonBytes, err := json.Marshal(result)
		require.NoError(t, err)
		
		expected := `{"outer_a":"value","outer_z":{"inner_a":2,"inner_z":1}}`
		require.Equal(t, expected, string(jsonBytes))
	})
	
	// Test validator array sorting
	t.Run("validator arrays are sorted by address", func(t *testing.T) {
		input := map[string]interface{}{
			"validators": []interface{}{
				map[string]interface{}{
					"address": "cosmos1zzz",
					"pub_key": "key1",
					"voting_power": "100",
				},
				map[string]interface{}{
					"address": "cosmos1aaa",
					"pub_key": "key2",
					"voting_power": "200",
				},
			},
		}
		
		result := sortGenesisFields(input)
		resultMap := result.(map[string]interface{})
		validators := resultMap["validators"].([]interface{})
		
		// Check that validators are sorted by address
		val0 := validators[0].(map[string]interface{})
		val1 := validators[1].(map[string]interface{})
		
		require.Equal(t, "cosmos1aaa", val0["address"])
		require.Equal(t, "cosmos1zzz", val1["address"])
	})
	
	// Test account array sorting
	t.Run("account arrays are sorted by address", func(t *testing.T) {
		input := map[string]interface{}{
			"accounts": []interface{}{
				map[string]interface{}{
					"@type": "/cosmos.auth.v1beta1.BaseAccount",
					"address": "cosmos1zzz",
					"pub_key": nil,
				},
				map[string]interface{}{
					"@type": "/cosmos.auth.v1beta1.BaseAccount",
					"address": "cosmos1aaa",
					"pub_key": nil,
				},
			},
		}
		
		result := sortGenesisFields(input)
		resultMap := result.(map[string]interface{})
		accounts := resultMap["accounts"].([]interface{})
		
		// Check that accounts are sorted by address
		acc0 := accounts[0].(map[string]interface{})
		acc1 := accounts[1].(map[string]interface{})
		
		require.Equal(t, "cosmos1aaa", acc0["address"])
		require.Equal(t, "cosmos1zzz", acc1["address"])
	})
	
	// Test deterministic output
	t.Run("output is deterministic", func(t *testing.T) {
		input := map[string]interface{}{
			"field_c": "value1",
			"field_a": "value2",
			"field_b": map[string]interface{}{
				"nested_z": 1,
				"nested_a": 2,
			},
			"validators": []interface{}{
				map[string]interface{}{
					"address": "val2",
					"voting_power": "100",
				},
				map[string]interface{}{
					"address": "val1",
					"voting_power": "200",
				},
			},
		}
		
		// Sort multiple times and ensure same output
		result1 := sortGenesisFields(input)
		result2 := sortGenesisFields(input)
		
		json1, err := json.Marshal(result1)
		require.NoError(t, err)
		
		json2, err := json.Marshal(result2)
		require.NoError(t, err)
		
		require.Equal(t, string(json1), string(json2))
	})
}

func TestMarshalSortedJSON(t *testing.T) {
	input := map[string]interface{}{
		"z_field": "value",
		"a_field": []interface{}{
			map[string]interface{}{
				"address": "addr2",
				"amount": "200",
			},
			map[string]interface{}{
				"address": "addr1",
				"amount": "100",
			},
		},
	}
	
	output, err := MarshalSortedJSON(input)
	require.NoError(t, err)
	
	// Output should be formatted and sorted
	expected := `{
  "a_field": [
    {
      "address": "addr1",
      "amount": "100"
    },
    {
      "address": "addr2",
      "amount": "200"
    }
  ],
  "z_field": "value"
}`
	
	require.Equal(t, expected, string(output))
}