// Package subnet contains deprecated local deployment functionality.
// DEPRECATED: This package is maintained for compatibility but is non-functional.
// All deployments should use Kubernetes (K8s) deployment instead.
package subnet

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cosmos/interchain-security-monitor/internal/relayer"
)

// Manager handles subnet creation and management for Interchain Security consumer chains
type Manager struct {
	logger        *slog.Logger
	workDir       string
	subnetdPath   string // Path to interchain-security-cd binary
	hermesPath    string // Path to hermes binary
	hermesManager *relayer.HermesManager
}

// NewManager creates a new subnet manager
func NewManager(logger *slog.Logger, workDir, subnetdPath, hermesPath string) (*Manager, error) {
	// Create work directory if it doesn't exist
	if err := os.MkdirAll(workDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create work directory: %w", err)
	}

	hermesManager := relayer.NewHermesManager(workDir, hermesPath)

	return &Manager{
		logger:        logger,
		workDir:       workDir,
		subnetdPath:   subnetdPath,
		hermesPath:    hermesPath,
		hermesManager: hermesManager,
	}, nil
}

// GetWorkDir returns the work directory
func (m *Manager) GetWorkDir() string {
	return m.workDir
}

// InitSubnet initializes a new subnet directory and genesis file
// This creates a deterministic genesis file that will be identical across all monitors
// The genesis timestamp is derived from the chain ID to ensure consistency
func (m *Manager) InitSubnet(chainID string) error {
	m.logger.Info("Initializing subnet", "chain_id", chainID)

	subnetDir := filepath.Join(m.workDir, chainID)
	configDir := filepath.Join(subnetDir, "config")

	// Create subnet directory structure
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create subnet directory: %w", err)
	}

	// Create a basic genesis file with deterministic timestamp
	// Use a fixed offset from Unix epoch based on chain ID hash to ensure
	// all monitors generate identical genesis
	chainIDHash := fnv.New32a()
	chainIDHash.Write([]byte(chainID))
	deterministicOffset := chainIDHash.Sum32()
	
	// Use a base time (e.g., 2025-01-01) plus deterministic offset in seconds
	baseTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	deterministicTime := baseTime.Add(time.Duration(deterministicOffset) * time.Second)
	
	genesis := map[string]interface{}{
		"chain_id":     chainID,
		"genesis_time": deterministicTime.UTC().Format(time.RFC3339),
		"initial_height": "1",
		"app_state": map[string]interface{}{},
	}

	genesisPath := filepath.Join(configDir, "genesis.json")
	genesisData, err := json.MarshalIndent(genesis, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal genesis: %w", err)
	}

	if err := os.WriteFile(genesisPath, genesisData, 0644); err != nil {
		return fmt.Errorf("failed to write genesis file: %w", err)
	}

	m.logger.Info("Subnet initialized", "chain_id", chainID, "path", subnetDir)
	return nil
}

// ApplyCCVGenesisPatch applies the CCV genesis patch to the subnet genesis
func (m *Manager) ApplyCCVGenesisPatch(chainID string, ccvPatch map[string]interface{}) error {
	m.logger.Info("Applying CCV genesis patch", "chain_id", chainID)

	subnetDir := filepath.Join(m.workDir, chainID)
	genesisPath := filepath.Join(subnetDir, "config", "genesis.json")

	// Read existing genesis
	genesisData, err := os.ReadFile(genesisPath)
	if err != nil {
		return fmt.Errorf("failed to read genesis file: %w", err)
	}

	var genesis map[string]interface{}
	if err := json.Unmarshal(genesisData, &genesis); err != nil {
		return fmt.Errorf("failed to unmarshal genesis: %w", err)
	}

	// Apply CCV patch
	// The patch may contain both app_state updates and top-level fields like genesis_time
	for key, value := range ccvPatch {
		if key == "app_state" {
			// Merge app_state deeply
			if appState, ok := genesis["app_state"].(map[string]interface{}); ok {
				if patchAppState, ok := value.(map[string]interface{}); ok {
					for k, v := range patchAppState {
						appState[k] = v
					}
					genesis["app_state"] = appState
				}
			}
		} else {
			// Apply top-level fields directly (like genesis_time)
			genesis[key] = value
		}
	}

	// Write updated genesis
	updatedData, err := json.MarshalIndent(genesis, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal updated genesis: %w", err)
	}

	if err := os.WriteFile(genesisPath, updatedData, 0644); err != nil {
		return fmt.Errorf("failed to write updated genesis: %w", err)
	}

	// Save CCV patch to file for K8s deployment to use
	ccvPatchPath := filepath.Join(subnetDir, "ccv-patch.json")
	ccvPatchData, err := json.MarshalIndent(ccvPatch, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal CCV patch: %w", err)
	}

	if err := os.WriteFile(ccvPatchPath, ccvPatchData, 0644); err != nil {
		return fmt.Errorf("failed to write CCV patch file: %w", err)
	}

	m.logger.Info("CCV genesis patch applied", "chain_id", chainID)
	return nil
}

// StartSubnet starts the subnet daemon
// DEPRECATED: This method is non-functional. Use K8s deployment instead.
func (m *Manager) StartSubnet(chainID string) error {
	m.logger.Info("Starting subnet", "chain_id", chainID)

	subnetDir := filepath.Join(m.workDir, chainID)
	
	// Calculate ports for this consumer chain
	ports, err := CalculatePorts(chainID)
	if err != nil {
		return fmt.Errorf("failed to calculate ports: %w", err)
	}

	// Create log directory
	logDir := filepath.Join(subnetDir, "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("failed to create log directory: %w", err)
	}

	logFile := filepath.Join(logDir, "subnet.log")
	log, err := os.Create(logFile)
	if err != nil {
		return fmt.Errorf("failed to create log file: %w", err)
	}
	defer log.Close()

	// Build the command with all necessary flags
	args := []string{
		"start",
		"--home", subnetDir,
		"--chain-id", chainID,
		"--rpc.laddr", fmt.Sprintf("tcp://0.0.0.0:%d", ports.RPC),
		"--p2p.laddr", fmt.Sprintf("tcp://0.0.0.0:%d", ports.P2P),
		"--grpc.address", fmt.Sprintf("0.0.0.0:%d", ports.GRPC),
		"--grpc-web.address", fmt.Sprintf("0.0.0.0:%d", ports.GRPCWeb),
		"--log_level", "info",
	}

	cmd := exec.Command(m.subnetdPath, args...)
	cmd.Stdout = log
	cmd.Stderr = log

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start subnet daemon: %w", err)
	}

	// Save process info for later management
	pidFile := filepath.Join(subnetDir, "daemon.pid")
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d", cmd.Process.Pid)), 0644); err != nil {
		m.logger.Warn("Failed to save PID file", "error", err)
	}

	m.logger.Info("Subnet daemon started", 
		"chain_id", chainID, 
		"pid", cmd.Process.Pid,
		"rpc_port", ports.RPC,
		"p2p_port", ports.P2P,
		"log_file", logFile)
	
	// Give the daemon a moment to start
	time.Sleep(2 * time.Second)
	
	return nil
}

// StartHermes starts the Hermes relayer for the subnet
func (m *Manager) StartHermes(chainID string) error {
	m.logger.Info("Starting Hermes relayer", "chain_id", chainID)

	// Get consumer chain RPC endpoint (using deterministic port allocation)
	consumerPorts, err := CalculatePorts(chainID)
	if err != nil {
		return fmt.Errorf("failed to calculate consumer ports: %w", err)
	}
	consumerRPC := fmt.Sprintf("http://%s:%d", chainID, consumerPorts.RPC)

	// Provider chain RPC (assuming standard setup)
	providerRPC := "http://provider1:26657"

	// Generate Hermes configuration
	configPath, err := m.hermesManager.GenerateConfig(chainID, chainID, providerRPC, consumerRPC)
	if err != nil {
		return fmt.Errorf("failed to generate Hermes config: %w", err)
	}
	m.logger.Info("Generated Hermes configuration", "config_path", configPath)

	// For now, log what would be done
	// In a complete implementation, this would:
	// 1. Start Hermes process/container
	// 2. Create IBC clients
	// 3. Establish connection
	// 4. Create CCV channel
	m.logger.Info("Hermes configuration ready", 
		"chain_id", chainID,
		"config_path", configPath,
		"next_steps", "Manual IBC setup required")

	// TODO: Implement actual Hermes process management
	// This depends on deployment type (K8s vs local)
	
	return nil
}

// StopSubnet stops a running subnet
// DEPRECATED: This method is non-functional. Use K8s deployment instead.
func (m *Manager) StopSubnet(chainID string) error {
	m.logger.Info("Stopping subnet", "chain_id", chainID)

	subnetDir := filepath.Join(m.workDir, chainID)
	pidFile := filepath.Join(subnetDir, "daemon.pid")

	// Read PID from file
	pidData, err := os.ReadFile(pidFile)
	if err != nil {
		if os.IsNotExist(err) {
			m.logger.Warn("PID file not found, subnet may not be running", "chain_id", chainID)
			return nil
		}
		return fmt.Errorf("failed to read PID file: %w", err)
	}

	var pid int
	if _, err := fmt.Sscanf(string(pidData), "%d", &pid); err != nil {
		return fmt.Errorf("failed to parse PID: %w", err)
	}

	// Find the process
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("failed to find process: %w", err)
	}

	// Send interrupt signal for graceful shutdown
	if err := process.Signal(os.Interrupt); err != nil {
		// Process might already be dead
		m.logger.Warn("Failed to send interrupt signal", "error", err, "pid", pid)
		// Clean up PID file anyway
		os.Remove(pidFile)
		return nil
	}

	// Wait a bit for graceful shutdown
	time.Sleep(5 * time.Second)

	// Check if process is still running and force kill if needed
	if err := process.Signal(os.Signal(nil)); err == nil {
		m.logger.Warn("Process still running after interrupt, sending kill signal", "pid", pid)
		if err := process.Kill(); err != nil {
			m.logger.Error("Failed to kill process", "error", err, "pid", pid)
		}
	}

	// Clean up PID file
	os.Remove(pidFile)

	m.logger.Info("Subnet stopped", "chain_id", chainID, "pid", pid)
	return nil
}

// CleanupSubnet removes all local files for a subnet
func (m *Manager) CleanupSubnet(chainID string) error {
	m.logger.Info("Cleaning up subnet", "chain_id", chainID)
	
	subnetDir := filepath.Join(m.workDir, chainID)
	if err := os.RemoveAll(subnetDir); err != nil {
		return fmt.Errorf("failed to remove subnet directory: %w", err)
	}
	
	m.logger.Info("Subnet cleaned up successfully", "chain_id", chainID, "path", subnetDir)
	return nil
}

// CalculateHashes calculates SHA256 hashes of key files for integrity verification
func (m *Manager) CalculateHashes() (string, string, error) {
	// Calculate hash of subnet daemon binary
	subnetdHash, err := m.calculateFileHash(m.subnetdPath)
	if err != nil {
		m.logger.Warn("Failed to calculate subnetd hash", "error", err)
		subnetdHash = "unknown"
	}

	// For genesis, we'll use a placeholder as it's chain-specific
	genesisHash := "chain-specific"

	return subnetdHash, genesisHash, nil
}

// calculateFileHash calculates SHA256 hash of a file
func (m *Manager) calculateFileHash(filepath string) (string, error) {
	file, err := os.Open(filepath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// NetworkInfo contains network configuration for a subnet
type NetworkInfo struct {
	P2PAddress string   `json:"p2p_address"`
	RPCAddress string   `json:"rpc_address"`
	Peers      []string `json:"peers"`
}

// GetNetworkInfo retrieves network information for a subnet
func (m *Manager) GetNetworkInfo(chainID string) (NetworkInfo, error) {
	// TODO: Query the running subnet for its network information
	// This would typically involve RPC calls to the subnet node

	return NetworkInfo{
		P2PAddress: "tcp://localhost:26656",
		RPCAddress: "tcp://localhost:26657",
		Peers:      []string{},
	}, nil
}

// JoinSubnet joins an existing subnet as a follower node
// DEPRECATED: This method is non-functional. Use K8s deployment instead.
func (m *Manager) JoinSubnet(chainID string, genesisPath string, peers []string) error {
	m.logger.Info("Joining subnet", "chain_id", chainID, "peers", strings.Join(peers, ","))

	subnetDir := filepath.Join(m.workDir, chainID)
	configDir := filepath.Join(subnetDir, "config")

	// Create subnet directory structure
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create subnet directory: %w", err)
	}

	// Copy genesis file
	destGenesis := filepath.Join(configDir, "genesis.json")
	if err := m.copyFile(genesisPath, destGenesis); err != nil {
		return fmt.Errorf("failed to copy genesis file: %w", err)
	}

	// Configure peers
	// TODO: Update config.toml with persistent peers

	m.logger.Info("Subnet joined successfully", "chain_id", chainID)
	return nil
}

// copyFile copies a file from source to destination
func (m *Manager) copyFile(src, dst string) error {
	input, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, input, 0644)
}