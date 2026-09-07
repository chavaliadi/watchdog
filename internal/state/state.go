package state

import (
	"time"
)

// State represents the operational status of a monitor.
type State string

const (
	StateUnknown  State = "UNKNOWN"
	StateUp       State = "UP"
	StateDown     State = "DOWN"
	StateDegraded State = "DEGRADED"
	StateFlapping State = "FLAPPING"
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
