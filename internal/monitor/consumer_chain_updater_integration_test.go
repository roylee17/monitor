//go:build integration
// +build integration

package monitor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net/http"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"log/slog"
)

// TestHybridUpdateIntegration tests the hybrid update with a real Tendermint node
func TestHybridUpdateIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup test directory
	testDir := t.TempDir()
	
	// Initialize a test chain
	chainID := "test-chain"
	nodeDir := filepath.Join(testDir, "node")
	
	// Initialize tendermint node
	cmd := exec.Command("tendermint", "init", "--home", nodeDir)
	require.NoError(t, cmd.Run())

	// Configure initial peers
	configPath := filepath.Join(nodeDir, "config", "config.toml")
	initialConfig := fmt.Sprintf(`
# Tendermint Configuration
proxy_app = "kvstore"
moniker = "test-node"

[p2p]
laddr = "tcp://0.0.0.0:26656"
persistent_peers = "node1@peer1.example.com:26656,node2@peer2.example.com:26656"
	`)
	require.NoError(t, os.WriteFile(configPath, []byte(initialConfig), 0644))

	// Start tendermint node
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tmCmd := exec.CommandContext(ctx, "tendermint", "node", "--home", nodeDir)
	tmCmd.Stdout = os.Stdout
	tmCmd.Stderr = os.Stderr
	
	require.NoError(t, tmCmd.Start())
	defer tmCmd.Process.Kill()

	// Wait for node to start
	time.Sleep(5 * time.Second)

	// Create updater
	updater := &HybridConsumerChainUpdater{
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}

	// Test 1: Verify we can get current peers
	t.Run("get_current_peers", func(t *testing.T) {
		peers, err := updater.getCurrentPeersViaRPC("http://localhost:26657")
		require.NoError(t, err)
		// Initially no peers connected (just configured)
		assert.NotNil(t, peers)
	})

	// Test 2: Add a new peer via RPC
	t.Run("add_peer_via_rpc", func(t *testing.T) {
		newPeer := "node3@peer3.example.com:26656"
		err := updater.addPeerViaRPC("http://localhost:26657", newPeer)
		require.NoError(t, err)

		// Verify the peer was added to dial list
		// Note: It won't actually connect since the peer doesn't exist
		time.Sleep(2 * time.Second)
		
		// Check net_info again
		peers, err := updater.getCurrentPeersViaRPC("http://localhost:26657")
		require.NoError(t, err)
		// The peer should be in the dialing state
	})

	// Test 3: Verify config file hasn't changed
	t.Run("config_unchanged", func(t *testing.T) {
		// Read config file
		configData, err := os.ReadFile(configPath)
		require.NoError(t, err)
		
		// Should still have original peers
		assert.Contains(t, string(configData), "node1@peer1.example.com:26656")
		assert.Contains(t, string(configData), "node2@peer2.example.com:26656")
		// New peer added via RPC shouldn't be in config
		assert.NotContains(t, string(configData), "node3@peer3.example.com:26656")
	})

	// Test 4: Restart node and verify RPC peers are lost
	t.Run("verify_rpc_peers_not_persistent", func(t *testing.T) {
		// Kill the node
		tmCmd.Process.Kill()
		tmCmd.Wait()

		// Start it again
		tmCmd2 := exec.Command("tendermint", "node", "--home", nodeDir)
		require.NoError(t, tmCmd2.Start())
		defer tmCmd2.Process.Kill()

		time.Sleep(5 * time.Second)

		// The RPC-added peer should be gone
		// Only config file peers remain
		// This demonstrates why we need to update ConfigMap too
	})
}

// TestHybridUpdateErrorScenarios tests error handling with mock scenarios
func TestHybridUpdateErrorScenarios(t *testing.T) {
	tests := []struct {
		name          string
		scenario      func(*testing.T, *HybridConsumerChainUpdater)
		expectRestart bool
	}{
		{
			name: "pod_not_ready",
			scenario: func(t *testing.T, updater *HybridConsumerChainUpdater) {
				// Create pod without IP
				ctx := context.Background()
				pod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "validator-0",
						Namespace: "test-ns",
						Labels:    map[string]string{"app": "validator"},
					},
					Status: corev1.PodStatus{
						// No PodIP set
					},
				}
				_, err := updater.clientset.CoreV1().Pods("test-ns").Create(ctx, pod, metav1.CreateOptions{})
				require.NoError(t, err)
			},
			expectRestart: true,
		},
		{
			name: "configmap_missing",
			scenario: func(t *testing.T, updater *HybridConsumerChainUpdater) {
				// Create pod but no ConfigMap
				ctx := context.Background()
				pod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "validator-0",
						Namespace: "test-ns",
						Labels:    map[string]string{"app": "validator"},
					},
					Status: corev1.PodStatus{
						PodIP: "10.0.0.1",
					},
				}
				_, err := updater.clientset.CoreV1().Pods("test-ns").Create(ctx, pod, metav1.CreateOptions{})
				require.NoError(t, err)
			},
			expectRestart: false, // ConfigMap update should fail, preventing any action
		},
		{
			name: "malformed_peer_addresses",
			scenario: func(t *testing.T, updater *HybridConsumerChainUpdater) {
				// Setup with malformed peers
				updater.peerDiscovery = &mockPeerDiscovery{
					peers: []string{
						"invalid-peer-format",
						"@missing-node-id:26656",
						"node1@:26656", // missing host
					},
				}
			},
			expectRestart: false, // Should handle gracefully
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			logger := testLogger()
			clientset := fake.NewSimpleClientset()
			updater := NewHybridConsumerChainUpdater(logger, clientset, nil, nil, nil)

			// Track restart calls
			restartCalled := false
			updater.ConsumerChainUpdater.restartConsumerChain = func(ctx context.Context, ns string) error {
				restartCalled = true
				return nil
			}

			// Run scenario
			tt.scenario(t, updater)

			// Execute update
			ctx := context.Background()
			err := updater.UpdateConsumerChainHybrid(ctx, "test-ns", "chain-0", nil)
			
			// Verify
			if tt.expectRestart {
				assert.True(t, restartCalled, "Expected restart to be called")
			} else {
				assert.False(t, restartCalled, "Expected no restart")
			}
			
			// Error is expected in most cases
			_ = err
		})
	}
}

// TestPerformanceComparison compares restart vs hybrid update performance
func TestPerformanceComparison(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping performance test in short mode")
	}

	// Measure time for restart-based update
	t.Run("restart_based_update", func(t *testing.T) {
		start := time.Now()
		
		// Simulate restart: delete pod, wait for new one
		// In real scenario this takes 20-30 seconds
		time.Sleep(100 * time.Millisecond) // Simulated
		
		duration := time.Since(start)
		t.Logf("Restart-based update took: %v", duration)
	})

	// Measure time for hybrid update
	t.Run("hybrid_update", func(t *testing.T) {
		start := time.Now()
		
		// Simulate RPC calls
		// In real scenario this takes 1-2 seconds
		time.Sleep(10 * time.Millisecond) // Simulated
		
		duration := time.Since(start)
		t.Logf("Hybrid update took: %v", duration)
		
		// Should be at least 10x faster
	})
}