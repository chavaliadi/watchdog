package checker

import (
	"time"
)

// ErrorClass represents the category of failure encountered during a check.
type ErrorClass string

const (
	ErrorClassNone        ErrorClass = ""
	ErrorClassDNS         ErrorClass = "dns"
	ErrorClassConnRefused ErrorClass = "conn_refused"
	ErrorClassTLS         ErrorClass = "tls"
	ErrorClassTimeout     ErrorClass = "timeout"
	ErrorClassStatus      ErrorClass = "status"
	ErrorClassAssertion   ErrorClass = "assertion"
)

// CheckResult represents the outcome of one completed monitoring cycle.
type CheckResult struct {
	ID           string        `json:"id"`
	MonitorID    string        `json:"monitor_id"`
	CheckedAt    time.Time     `json:"checked_at"`
	OK           bool          `json:"ok"`
	StatusCode   int           `json:"status_code,omitempty"`
	Latency      time.Duration `json:"latency"`
	ErrorClass   ErrorClass    `json:"error_class,omitempty"`
	ErrorDetail  string        `json:"error_detail,omitempty"`
	AttemptCount int           `json:"attempt_count"`
}
