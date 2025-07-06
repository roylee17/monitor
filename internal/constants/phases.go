package constants

// Consumer chain phases as defined in ICS
const (
	// PhaseUnspecified indicates an unspecified phase
	PhaseUnspecified = "CONSUMER_PHASE_UNSPECIFIED"
	
	// PhaseRegistered indicates a consumer chain is registered but not yet initialized
	PhaseRegistered = "CONSUMER_PHASE_REGISTERED"
	
	// PhaseInitialized indicates a consumer chain is initialized and waiting for spawn time
	PhaseInitialized = "CONSUMER_PHASE_INITIALIZED"
	
	// PhaseLaunched indicates a consumer chain has reached spawn time and is launched
	PhaseLaunched = "CONSUMER_PHASE_LAUNCHED"
	
	// PhaseStopped indicates a consumer chain has been stopped
	PhaseStopped = "CONSUMER_PHASE_STOPPED"
	
	// PhaseDeleted indicates a consumer chain has been deleted
	PhaseDeleted = "CONSUMER_PHASE_DELETED"
)

// IsActivePhase returns true if the phase indicates an active consumer chain
func IsActivePhase(phase string) bool {
	return phase == PhaseRegistered || phase == PhaseInitialized || phase == PhaseLaunched
}

// IsMonitorablePhase returns true if the phase should be monitored
func IsMonitorablePhase(phase string) bool {
	return phase == PhaseRegistered || phase == PhaseInitialized || phase == PhaseLaunched
}