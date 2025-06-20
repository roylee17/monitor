package subnet

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
)

// FilePeerPersistence implements file-based peer persistence
type FilePeerPersistence struct {
	baseDir string
	mu      sync.RWMutex
}

// NewFilePeerPersistence creates a new file-based peer persistence
func NewFilePeerPersistence(baseDir string) (*FilePeerPersistence, error) {
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create peer storage directory: %w", err)
	}
	
	return &FilePeerPersistence{
		baseDir: baseDir,
	}, nil
}

// Save saves peer information to a file
func (fp *FilePeerPersistence) Save(consumerID string, peers map[string]PeerInfo) error {
	fp.mu.Lock()
	defer fp.mu.Unlock()
	
	data, err := json.MarshalIndent(peers, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal peers: %w", err)
	}
	
	filename := filepath.Join(fp.baseDir, fmt.Sprintf("%s-peers.json", consumerID))
	if err := ioutil.WriteFile(filename, data, 0644); err != nil {
		return fmt.Errorf("failed to write peer file: %w", err)
	}
	
	return nil
}

// Load loads peer information from a file
func (fp *FilePeerPersistence) Load(consumerID string) (map[string]PeerInfo, error) {
	fp.mu.RLock()
	defer fp.mu.RUnlock()
	
	filename := filepath.Join(fp.baseDir, fmt.Sprintf("%s-peers.json", consumerID))
	data, err := ioutil.ReadFile(filename)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read peer file: %w", err)
	}
	
	var peers map[string]PeerInfo
	if err := json.Unmarshal(data, &peers); err != nil {
		return nil, fmt.Errorf("failed to unmarshal peers: %w", err)
	}
	
	return peers, nil
}

// List lists all consumer IDs with stored peer information
func (fp *FilePeerPersistence) List() ([]string, error) {
	fp.mu.RLock()
	defer fp.mu.RUnlock()
	
	files, err := ioutil.ReadDir(fp.baseDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read peer directory: %w", err)
	}
	
	var consumerIDs []string
	for _, file := range files {
		if !file.IsDir() && filepath.Ext(file.Name()) == ".json" {
			// Extract consumer ID from filename
			consumerID := file.Name()[:len(file.Name())-len("-peers.json")]
			consumerIDs = append(consumerIDs, consumerID)
		}
	}
	
	return consumerIDs, nil
}

// S3PeerPersistence could implement cloud-based persistence
type S3PeerPersistence struct {
	bucket string
	prefix string
	// s3Client would go here
}

// RedisPeerPersistence could implement Redis-based persistence
type RedisPeerPersistence struct {
	redisURL string
	// redisClient would go here
}

// SharedDatabasePersistence could use a shared PostgreSQL/MySQL database
type SharedDatabasePersistence struct {
	connectionString string
	// db connection would go here
}

// For production environments, validators could choose:
// 1. Shared infrastructure (Redis, Database, S3) for peer discovery
// 2. P2P gossip protocol for peer exchange
// 3. DNS-based discovery (consumer-1.alice.validators.com)
// 4. Blockchain-based registry (store peer info on-chain)