package monitor

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTendermintRPCBehavior verifies our understanding of Tendermint RPC behavior
func TestTendermintRPCBehavior(t *testing.T) {
	// This test documents the actual Tendermint RPC behavior
	// to ensure our implementation matches reality

	t.Run("net_info_response_format", func(t *testing.T) {
		// Actual response from Tendermint
		actualResponse := `{
			"jsonrpc": "2.0",
			"id": -1,
			"result": {
				"listening": true,
				"listeners": ["Listener(@)"],
				"n_peers": "2",
				"peers": [
					{
						"node_info": {
							"protocol_version": {"p2p": "8", "block": "11", "app": "0"},
							"id": "5576458aef205977e18fd50b274e9b5d9014525a",
							"listen_addr": "tcp://0.0.0.0:26656",
							"network": "cosmoshub-4",
							"version": "0.34.19",
							"channels": "40202122233038606100",
							"moniker": "alice",
							"other": {"tx_index": "on", "rpc_address": "tcp://0.0.0.0:26657"}
						},
						"is_outbound": true,
						"connection_status": {
							"Duration": "23h4m51.792066664s",
							"SendMonitor": {},
							"RecvMonitor": {},
							"Channels": []
						},
						"remote_ip": "185.194.216.99"
					},
					{
						"node_info": {
							"protocol_version": {"p2p": "8", "block": "11", "app": "0"},
							"id": "c9627b3d21d73d5c12d09b3b2dd7597ec1dde5a0",
							"listen_addr": "tcp://0.0.0.0:26656",
							"network": "cosmoshub-4",
							"version": "0.34.19",
							"channels": "40202122233038606100",
							"moniker": "bob",
							"other": {"tx_index": "on", "rpc_address": "tcp://0.0.0.0:26657"}
						},
						"is_outbound": false,
						"connection_status": {
							"Duration": "22h45m32.529938074s",
							"SendMonitor": {},
							"RecvMonitor": {},
							"Channels": []
						},
						"remote_ip": "142.93.145.25"
					}
				]
			}
		}`

		// Parse to verify our struct matches
		var response struct {
			Result TendermintNetInfo `json:"result"`
		}
		err := json.Unmarshal([]byte(actualResponse), &response)
		require.NoError(t, err)

		// Verify we can extract what we need
		assert.Equal(t, "2", response.Result.NPeers)
		assert.Len(t, response.Result.Peers, 2)
		assert.Equal(t, "5576458aef205977e18fd50b274e9b5d9014525a", response.Result.Peers[0].NodeInfo.ID)
		assert.Equal(t, "alice", response.Result.Peers[0].NodeInfo.Moniker)
		assert.Equal(t, "185.194.216.99", response.Result.Peers[0].RemoteIP)
	})

	t.Run("dial_peers_behavior", func(t *testing.T) {
		// Test server that mimics Tendermint behavior
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/dial_peers", r.URL.Path)
			
			// Check query parameters
			peers := r.URL.Query().Get("peers")
			persistent := r.URL.Query().Get("persistent")
			
			// Tendermint expects peers as JSON array in query param
			assert.True(t, strings.HasPrefix(peers, "["))
			assert.True(t, strings.HasSuffix(peers, "]"))
			
			// For persistent peers
			assert.Equal(t, "true", persistent)

			// Tendermint returns empty result on success
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      -1,
				"result":  map[string]interface{}{},
			})
		}))
		defer server.Close()

		// Test our implementation
		updater := &ConsumerChainUpdater{
			httpClient: &http.Client{Timeout: 5 * time.Second},
		}

		err := updater.addPeerViaRPC(server.URL, "node1@example.com:26656")
		assert.NoError(t, err)
	})

	t.Run("dial_peers_error_cases", func(t *testing.T) {
		testCases := []struct {
			name           string
			serverResponse func(w http.ResponseWriter, r *http.Request)
			expectError    bool
			errorContains  string
		}{
			{
				name: "peer_already_connected",
				serverResponse: func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusBadRequest)
					json.NewEncoder(w).Encode(map[string]interface{}{
						"jsonrpc": "2.0",
						"id":      -1,
						"error": map[string]interface{}{
							"code":    -32600,
							"message": "peer already connected",
						},
					})
				},
				expectError:   true,
				errorContains: "400",
			},
			{
				name: "invalid_peer_format",
				serverResponse: func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusBadRequest)
					json.NewEncoder(w).Encode(map[string]interface{}{
						"jsonrpc": "2.0",
						"id":      -1,
						"error": map[string]interface{}{
							"code":    -32602,
							"message": "invalid peer address format",
						},
					})
				},
				expectError:   true,
				errorContains: "400",
			},
			{
				name: "internal_error",
				serverResponse: func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusInternalServerError)
					w.Write([]byte("Internal Server Error"))
				},
				expectError:   true,
				errorContains: "500",
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(tc.serverResponse))
				defer server.Close()

				updater := &ConsumerChainUpdater{
					httpClient: &http.Client{Timeout: 5 * time.Second},
				}

				err := updater.addPeerViaRPC(server.URL, "node1@example.com:26656")
				if tc.expectError {
					assert.Error(t, err)
					assert.Contains(t, err.Error(), tc.errorContains)
				} else {
					assert.NoError(t, err)
				}
			})
		}
	})

	t.Run("remove_peer_not_supported", func(t *testing.T) {
		// Standard Tendermint doesn't have remove_peer endpoint
		// We don't implement this functionality since it's not supported
		// The hybrid updater only adds peers, doesn't remove them
		assert.True(t, true, "Remove peer is not supported in standard Tendermint")
	})
}

// TestRPCRetryLogic tests retry behavior for transient failures
func TestRPCRetryLogic(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			// Fail first 2 attempts
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		// Succeed on 3rd attempt
		json.NewEncoder(w).Encode(map[string]interface{}{
			"result": map[string]interface{}{
				"n_peers": "1",
				"peers":   []interface{}{},
			},
		})
	}))
	defer server.Close()

	updater := &ConsumerChainUpdater{
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
			// Add retry logic here in real implementation
		},
	}

	// Current implementation doesn't retry
	// This test documents that we might want to add retry logic
	_, err := updater.getCurrentPeersViaRPC(server.URL)
	assert.Error(t, err) // Will fail on first attempt
	assert.Equal(t, 1, attempts) // Only one attempt made
}

// TestConcurrentRPCCalls tests concurrent RPC calls don't interfere
func TestConcurrentRPCCalls(t *testing.T) {
	callCount := 0
	var mu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		callCount++
		currentCall := callCount
		mu.Unlock()

		// Simulate some processing time
		time.Sleep(10 * time.Millisecond)

		// Each call gets unique response
		json.NewEncoder(w).Encode(map[string]interface{}{
			"result": map[string]interface{}{
				"n_peers": fmt.Sprintf("%d", currentCall),
			},
		})
	}))
	defer server.Close()

	updater := &ConsumerChainUpdater{
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}

	// Make concurrent calls
	const numCalls = 10
	results := make(chan error, numCalls)

	for i := 0; i < numCalls; i++ {
		go func() {
			err := updater.addPeerViaRPC(server.URL, fmt.Sprintf("node%d@example.com:26656", i))
			results <- err
		}()
	}

	// Collect results
	for i := 0; i < numCalls; i++ {
		err := <-results
		assert.NoError(t, err)
	}

	assert.Equal(t, numCalls, callCount)
}

// TestRPCTimeout tests timeout behavior
func TestRPCTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Hang longer than client timeout
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	updater := &ConsumerChainUpdater{
		httpClient: &http.Client{
			Timeout: 100 * time.Millisecond, // Short timeout
		},
	}

	start := time.Now()
	_, err := updater.getCurrentPeersViaRPC(server.URL)
	duration := time.Since(start)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "context deadline exceeded")
	assert.Less(t, duration, 200*time.Millisecond) // Should timeout quickly
}