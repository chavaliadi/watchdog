package state

import (
	"fmt"
	"time"

	"github.com/chavaliadi/watchdog/internal/checker"
)

// State represents the operational health status of a monitor.
type State string

const (
	StateUnknown   State = "UNKNOWN"
	StateHealthy   State = "HEALTHY"
	StateUnhealthy State = "UNHEALTHY"
)

// MonitorState represents the remembered state and transition metrics of a monitor.
type MonitorState struct {
	MonitorID         string    `json:"monitor_id"`
	State             State     `json:"state"`
	ConsecutiveFails  int       `json:"consecutive_fails"`
	ConsecutivePasses int       `json:"consecutive_passes"`
	LastTransitionAt  time.Time `json:"last_transition_at"`
	LastCheckedAt     time.Time `json:"last_checked_at"`
	FlapCountWindow   int       `json:"flap_count_window"`
}

// TransitionResult captures the outcome of evaluating a health state transition.
type TransitionResult struct {
	CurrentState State
	NextState    State
	Transitioned bool
}

// Transition computes the next health state given the current state and whether
// the check cycle succeeded (ok == true) or failed (ok == false).
// If current is empty (""), it is treated as StateUnknown.
// If current is an unrecognized state, a non-nil error is returned.
func Transition(current State, ok bool) (TransitionResult, error) {
	if current == "" {
		current = StateUnknown
	}

	var next State
	switch current {
	case StateUnknown:
		if ok {
			next = StateHealthy
		} else {
			next = StateUnhealthy
		}
	case StateHealthy:
		if ok {
			next = StateHealthy
		} else {
			next = StateUnhealthy
		}
	case StateUnhealthy:
		if ok {
			next = StateHealthy
		} else {
			next = StateUnhealthy
		}
	default:
		return TransitionResult{}, fmt.Errorf("invalid or unsupported state: %q", current)
	}

	return TransitionResult{
		CurrentState: current,
		NextState:    next,
		Transitioned: current != next,
	}, nil
}

// TransitionFromCheckResult computes the next health state given the current state
// and the final CheckResult of a completed check cycle.
func TransitionFromCheckResult(current State, res checker.CheckResult) (TransitionResult, error) {
	return Transition(current, res.OK)
}
