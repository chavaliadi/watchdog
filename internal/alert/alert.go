package alert

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/chavaliadi/watchdog/internal/checker"
	"github.com/chavaliadi/watchdog/internal/monitor"
	"github.com/chavaliadi/watchdog/internal/state"
)

// Notifier defines the contract for dispatching alert notifications.
type Notifier interface {
	Notify(ctx context.Context, event AlertEvent) error
}

// NoopNotifier is a no-op implementation of Notifier used when alerting is disabled.
type NoopNotifier struct{}

// Notify performs a no-op and returns nil.
func (NoopNotifier) Notify(ctx context.Context, event AlertEvent) error {
	return nil
}

var _ Notifier = NoopNotifier{}

// MonitorInfo encapsulates sanitized metadata about the monitored target.
type MonitorInfo struct {
	ID     string       `json:"id"`
	Name   string       `json:"name"`
	Kind   monitor.Kind `json:"kind"`
	Target string       `json:"target"`
}

// TransitionInfo captures the state change that triggered the alert.
type TransitionInfo struct {
	From state.State `json:"from"`
	To   state.State `json:"to"`
}

// CheckSummary captures bounded, sanitized operational details of the triggering check.
type CheckSummary struct {
	OK           bool               `json:"ok"`
	StatusCode   int                `json:"status_code,omitempty"`
	ErrorClass   checker.ErrorClass `json:"error_class,omitempty"`
	ErrorDetail  string             `json:"error_detail,omitempty"`
	AttemptCount int                `json:"attempt_count"`
	LatencyMS    int64              `json:"latency_ms"`
}

// AlertEvent represents an immutable operational notification payload.
type AlertEvent struct {
	Event      string         `json:"event"`
	Monitor    MonitorInfo    `json:"monitor"`
	Transition TransitionInfo `json:"transition"`
	Check      CheckSummary   `json:"check"`
	Timestamp  time.Time      `json:"timestamp"`
}

const (
	// EventMonitorUnhealthy indicates that a monitor transitioned into the UNHEALTHY state.
	EventMonitorUnhealthy = "monitor_unhealthy"

	// maxErrorDetailLen restricts the maximum character length for check error descriptions.
	maxErrorDetailLen = 256
)

// NewAlertEvent constructs a sanitized AlertEvent for a transition into UNHEALTHY.
func NewAlertEvent(
	m monitor.Monitor,
	from state.State,
	to state.State,
	res checker.CheckResult,
) AlertEvent {
	return AlertEvent{
		Event: EventMonitorUnhealthy,
		Monitor: MonitorInfo{
			ID:     m.ID,
			Name:   m.Name,
			Kind:   m.Kind,
			Target: SanitizeTarget(m.TargetURL),
		},
		Transition: TransitionInfo{
			From: from,
			To:   to,
		},
		Check: CheckSummary{
			OK:           res.OK,
			StatusCode:   res.StatusCode,
			ErrorClass:   res.ErrorClass,
			ErrorDetail:  SanitizeErrorDetail(res.ErrorDetail),
			AttemptCount: res.AttemptCount,
			LatencyMS:    res.Latency.Milliseconds(),
		},
		Timestamp: time.Now().UTC(),
	}
}

// SanitizeTarget strips sensitive user info (user/password credentials) from target URLs if present.
func SanitizeTarget(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User == nil {
		return raw
	}
	parsed.User = nil
	return parsed.String()
}

// SanitizeErrorDetail truncates error details to a safe, bounded length without control characters.
func SanitizeErrorDetail(detail string) string {
	detail = strings.TrimSpace(detail)
	if len(detail) > maxErrorDetailLen {
		detail = detail[:maxErrorDetailLen] + "..."
	}
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, detail)
}
