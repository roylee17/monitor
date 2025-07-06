package transaction

import "context"

// Service defines the interface for transaction operations
type Service interface {
	// OptIn sends an opt-in transaction for a validator to join a consumer chain
	OptIn(ctx context.Context, consumerID string, consumerPubKey string) error
	
	// ExecuteTx executes a generic transaction command
	ExecuteTx(ctx context.Context, args []string) (*TxResult, error)
}