package subnet

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

// NetworkInfo contains network connectivity information
type NetworkInfo struct {
	ChainID    string
	Peers      []string
	P2PAddress string
	RPCAddress string
}

// Manager is a minimal base manager for subnet operations
// The actual implementation is in K8sManager for Kubernetes deployments
type Manager struct {
	logger      *slog.Logger
	workDir     string
	subnetdPath string
	homeDirPath string
}

// NewManager creates a new base subnet manager
func NewManager(logger *slog.Logger, workDir, subnetdPath, homeDirPath string) (*Manager, error) {
	return &Manager{
		logger:      logger,
		workDir:     workDir,
		subnetdPath: subnetdPath,
		homeDirPath: homeDirPath,
	}, nil
}

// CalculateHashes calculates hashes for deployment metadata
func (m *Manager) CalculateHashes() (string, string, error) {
	// In K8s deployments, we don't have local files to hash
	// Return placeholder values
	return "k8s-deployment", "k8s-genesis", nil
}

// ApplyCCVGenesisPatch applies CCV patch to genesis
func (m *Manager) ApplyCCVGenesisPatch(chainID string, ccvPatch map[string]interface{}) error {
	genesisPath := filepath.Join(m.workDir, chainID, "config", "genesis.json")
	
	// Read existing genesis
	genesisData, err := os.ReadFile(genesisPath)
	if err != nil {
		return fmt.Errorf("failed to read genesis: %w", err)
	}
	
	var genesis map[string]interface{}
	if err := json.Unmarshal(genesisData, &genesis); err != nil {
		return fmt.Errorf("failed to unmarshal genesis: %w", err)
	}
	
	// Apply CCV patch
	appState, ok := genesis["app_state"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid genesis structure: missing app_state")
	}
	
	for key, value := range ccvPatch {
		appState[key] = value
	}
	
	// Write back with sorted fields for deterministic output
	updatedGenesis, err := MarshalSortedJSON(genesis)
	if err != nil {
		return fmt.Errorf("failed to marshal updated genesis: %w", err)
	}
	
	return os.WriteFile(genesisPath, updatedGenesis, 0644)
}

// CleanupSubnet removes subnet directory
func (m *Manager) CleanupSubnet(chainID string) error {
	subnetDir := filepath.Join(m.workDir, chainID)
	return os.RemoveAll(subnetDir)
}