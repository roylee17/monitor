package monitor

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// CCVGenesisCache caches CCV genesis data to ensure consistency across validators
type CCVGenesisCache struct {
	mu    sync.RWMutex
	cache map[string]*CCVGenesisCacheEntry
}

// CCVGenesisCacheEntry represents a cached CCV genesis entry
type CCVGenesisCacheEntry struct {
	Data      map[string]interface{}
	Hash      string
	Timestamp time.Time
}

// NewCCVGenesisCache creates a new CCV genesis cache
func NewCCVGenesisCache() *CCVGenesisCache {
	return &CCVGenesisCache{
		cache: make(map[string]*CCVGenesisCacheEntry),
	}
}

// Get retrieves CCV genesis data from cache
func (c *CCVGenesisCache) Get(consumerID string) (*CCVGenesisCacheEntry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	entry, exists := c.cache[consumerID]
	return entry, exists
}

// Set stores CCV genesis data in cache
func (c *CCVGenesisCache) Set(consumerID string, data map[string]interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	// Calculate hash of the data
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal data for hashing: %w", err)
	}
	
	hash := sha256.Sum256(jsonData)
	hashStr := hex.EncodeToString(hash[:])
	
	c.cache[consumerID] = &CCVGenesisCacheEntry{
		Data:      data,
		Hash:      hashStr,
		Timestamp: time.Now(),
	}
	
	return nil
}

// Clear removes an entry from the cache
func (c *CCVGenesisCache) Clear(consumerID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	delete(c.cache, consumerID)
}

// NormalizeGenesisTime ensures consistent genesis time across validators
// by rounding to the nearest minute
func NormalizeGenesisTime(genesis map[string]interface{}) error {
	// Extract genesis time
	genesisTime, ok := genesis["genesis_time"].(string)
	if !ok {
		return fmt.Errorf("genesis_time not found")
	}
	
	// Parse the time
	t, err := time.Parse(time.RFC3339Nano, genesisTime)
	if err != nil {
		return fmt.Errorf("failed to parse genesis_time: %w", err)
	}
	
	// Round to the nearest minute to ensure consistency
	rounded := t.Round(time.Minute)
	
	// Update the genesis time
	genesis["genesis_time"] = rounded.Format(time.RFC3339Nano)
	
	return nil
}