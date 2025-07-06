package subnet

import (
	"fmt"
	"strings"
)

// DeploymentError represents errors that occur during deployment
type DeploymentError struct {
	ChainID  string
	Phase    string
	Critical bool
	Errors   []error
}

// NewDeploymentError creates a new deployment error tracker
func NewDeploymentError(chainID, phase string) *DeploymentError {
	return &DeploymentError{
		ChainID: chainID,
		Phase:   phase,
		Errors:  []error{},
	}
}

// AddError adds a non-critical error
func (d *DeploymentError) AddError(err error) {
	if err != nil {
		d.Errors = append(d.Errors, err)
	}
}

// AddCriticalError adds a critical error and marks the deployment as failed
func (d *DeploymentError) AddCriticalError(err error) {
	if err != nil {
		d.Critical = true
		d.Errors = append(d.Errors, err)
	}
}

// HasErrors returns true if any errors were recorded
func (d *DeploymentError) HasErrors() bool {
	return len(d.Errors) > 0
}

// IsCritical returns true if any critical errors occurred
func (d *DeploymentError) IsCritical() bool {
	return d.Critical
}

// Error implements the error interface
func (d *DeploymentError) Error() string {
	if len(d.Errors) == 0 {
		return ""
	}

	var msgs []string
	for _, err := range d.Errors {
		msgs = append(msgs, err.Error())
	}

	prefix := fmt.Sprintf("deployment errors for %s (phase: %s)", d.ChainID, d.Phase)
	if d.Critical {
		prefix = "CRITICAL " + prefix
	}

	return fmt.Sprintf("%s: %s", prefix, strings.Join(msgs, "; "))
}

// Summary returns a summary of the deployment status
func (d *DeploymentError) Summary() string {
	if !d.HasErrors() {
		return fmt.Sprintf("Deployment successful for %s", d.ChainID)
	}

	criticalCount := 0
	if d.Critical {
		for range d.Errors {
			criticalCount++
		}
	}

	return fmt.Sprintf("Deployment for %s completed with %d errors (%d critical)",
		d.ChainID, len(d.Errors), criticalCount)
}