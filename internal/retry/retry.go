package retry

import (
	"context"
	"fmt"
	"time"
)

// Config defines retry behavior configuration
type Config struct {
	MaxAttempts   int
	InitialDelay  time.Duration
	MaxDelay      time.Duration
	Multiplier    float64
	Jitter        bool
}

// DefaultConfig returns a sensible default retry configuration
func DefaultConfig() Config {
	return Config{
		MaxAttempts:  3,
		InitialDelay: 1 * time.Second,
		MaxDelay:     30 * time.Second,
		Multiplier:   2.0,
		Jitter:       true,
	}
}

// Do executes a function with retry logic based on the configuration
func Do(ctx context.Context, cfg Config, fn func() error) error {
	var lastErr error
	delay := cfg.InitialDelay

	for attempt := 0; attempt < cfg.MaxAttempts; attempt++ {
		// Execute the function
		err := fn()
		if err == nil {
			return nil
		}
		
		lastErr = err
		
		// Check if this was the last attempt
		if attempt == cfg.MaxAttempts-1 {
			break
		}
		
		// Check context cancellation
		select {
		case <-ctx.Done():
			return fmt.Errorf("retry cancelled: %w", ctx.Err())
		default:
		}
		
		// Wait before next attempt
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return fmt.Errorf("retry cancelled during delay: %w", ctx.Err())
		}
		
		// Calculate next delay with exponential backoff
		delay = time.Duration(float64(delay) * cfg.Multiplier)
		if delay > cfg.MaxDelay {
			delay = cfg.MaxDelay
		}
	}
	
	return fmt.Errorf("failed after %d attempts: %w", cfg.MaxAttempts, lastErr)
}

// DoWithResult executes a function that returns a value with retry logic
func DoWithResult[T any](ctx context.Context, cfg Config, fn func() (T, error)) (T, error) {
	var zero T
	var lastErr error
	delay := cfg.InitialDelay

	for attempt := 0; attempt < cfg.MaxAttempts; attempt++ {
		// Execute the function
		result, err := fn()
		if err == nil {
			return result, nil
		}
		
		lastErr = err
		
		// Check if this was the last attempt
		if attempt == cfg.MaxAttempts-1 {
			break
		}
		
		// Check context cancellation
		select {
		case <-ctx.Done():
			return zero, fmt.Errorf("retry cancelled: %w", ctx.Err())
		default:
		}
		
		// Wait before next attempt
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return zero, fmt.Errorf("retry cancelled during delay: %w", ctx.Err())
		}
		
		// Calculate next delay
		delay = time.Duration(float64(delay) * cfg.Multiplier)
		if delay > cfg.MaxDelay {
			delay = cfg.MaxDelay
		}
	}
	
	return zero, fmt.Errorf("failed after %d attempts: %w", cfg.MaxAttempts, lastErr)
}