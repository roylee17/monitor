package monitor

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/kubernetes/fake"
	"log/slog"
)

// TestHybridUpdateEnabled tests that hybrid updates work when enabled
func TestHybridUpdateEnabled(t *testing.T) {

	// Create updater with hybrid enabled
	logger := slog.Default()
	clientset := fake.NewSimpleClientset()
	updater := &ConsumerChainUpdater{
		logger:          logger,
		clientset:       clientset,
		httpClient:      &http.Client{},
		useHybridUpdate: true,
	}

	// Test enabling hybrid update
	updater.EnableHybridUpdate(true)
	assert.True(t, updater.useHybridUpdate)

	// Disable and check
	updater.EnableHybridUpdate(false)
	assert.False(t, updater.useHybridUpdate)
}

// TestCalculatePeersToAdd tests peer calculation logic
func TestCalculatePeersToAdd(t *testing.T) {
	updater := &ConsumerChainUpdater{
		logger: slog.Default(),
	}

	tests := []struct {
		name     string
		current  map[string]string
		newPeers []string
		expected []string
	}{
		{
			name: "add_new_peer",
			current: map[string]string{
				"node1": "1.2.3.4",
			},
			newPeers: []string{
				"node1@host1:26656",
				"node2@host2:26656",
			},
			expected: []string{
				"node2@host2:26656",
			},
		},
		{
			name:    "all_new_peers",
			current: map[string]string{},
			newPeers: []string{
				"node1@host1:26656",
				"node2@host2:26656",
			},
			expected: []string{
				"node1@host1:26656",
				"node2@host2:26656",
			},
		},
		{
			name: "no_new_peers",
			current: map[string]string{
				"node1": "1.2.3.4",
				"node2": "5.6.7.8",
			},
			newPeers: []string{
				"node1@host1:26656",
				"node2@host2:26656",
			},
			expected: []string{},
		},
		{
			name: "invalid_peer_format",
			current: map[string]string{
				"node1": "1.2.3.4",
			},
			newPeers: []string{
				"node1@host1:26656",
				"invalid-peer",
				"@missing-id:26656",
				"node2@host2:26656",
			},
			expected: []string{
				"node2@host2:26656",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := updater.calculatePeersToAdd(tt.current, tt.newPeers)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestGetPodRPCEndpoint tests RPC endpoint extraction
func TestGetPodRPCEndpoint(t *testing.T) {
	ctx := context.Background()
	clientset := fake.NewSimpleClientset()

	updater := &ConsumerChainUpdater{
		logger:    slog.Default(),
		clientset: clientset,
	}

	// Test no pods
	_, err := updater.getPodRPCEndpoint(ctx, "test-namespace")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no validator pods found")
}