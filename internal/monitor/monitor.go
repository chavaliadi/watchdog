package monitor

import (
	"time"
)

// Kind represents the protocol or check type for a monitor.
type Kind string

const (
	KindHTTP   Kind = "http"
	KindTCP    Kind = "tcp"
	KindHealth Kind = "health"
)

// Monitor represents the configuration and rules for an individual target monitor.
type Monitor struct {
	ID                   string        `json:"id"`
	UserID               string        `json:"user_id"`
	Name                 string        `json:"name"`
	Kind                 Kind          `json:"kind"`
	TargetURL            string        `json:"target_url"`
	Method               string        `json:"method"`
	ExpectedStatusRange  string        `json:"expected_status_range"`
	BodyAssertions       []string      `json:"body_assertions,omitempty"`
	LatencyWarnThreshold time.Duration `json:"latency_warn_threshold"`
	LatencyFailThreshold time.Duration `json:"latency_fail_threshold"`
	Interval             time.Duration `json:"interval"`
	Timeout              time.Duration `json:"timeout"`
	FailThreshold        int           `json:"fail_threshold"`
	RecoverThreshold     int           `json:"recover_threshold"`
	Enabled              bool          `json:"enabled"`
	CreatedAt            time.Time     `json:"created_at"`
}
