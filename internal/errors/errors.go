package errors

import "fmt"

// Wrap wraps an error with a descriptive action message
func Wrap(err error, action string) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("failed to %s: %w", action, err)
}

// Wrapf wraps an error with a formatted action message
func Wrapf(err error, format string, args ...interface{}) error {
	if err == nil {
		return nil
	}
	action := fmt.Sprintf(format, args...)
	return fmt.Errorf("failed to %s: %w", action, err)
}

// Context wraps an error with context and action information
func Context(err error, context, action string) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: failed to %s: %w", context, action, err)
}