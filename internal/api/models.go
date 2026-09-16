package api

import (
	"time"

	"github.com/chavaliadi/watchdog/internal/checker"
	"github.com/chavaliadi/watchdog/internal/monitor"
	"github.com/chavaliadi/watchdog/internal/service"
)

type CreateMonitorJSON struct {
	Name                string       `json:"name"`
	Kind                monitor.Kind `json:"kind"`
	Target              string       `json:"target"`
	Method              string       `json:"method,omitempty"`
	ExpectedStatusRange string       `json:"expected_status_range,omitempty"`
	IntervalMs          *int64       `json:"interval_ms,omitempty"`
	TimeoutMs           *int64       `json:"timeout_ms,omitempty"`
	Enabled             *bool        `json:"enabled,omitempty"`
}

type PatchMonitorJSON struct {
	Name                *string       `json:"name,omitempty"`
	Kind                *monitor.Kind `json:"kind,omitempty"`
	Target              *string       `json:"target,omitempty"`
	Method              *string       `json:"method,omitempty"`
	ExpectedStatusRange *string       `json:"expected_status_range,omitempty"`
	IntervalMs          *int64        `json:"interval_ms,omitempty"`
	TimeoutMs           *int64        `json:"timeout_ms,omitempty"`
	Enabled             *bool         `json:"enabled,omitempty"`
}

type MonitorResponse struct {
	ID                  string       `json:"id"`
	Name                string       `json:"name"`
	Kind                monitor.Kind `json:"kind"`
	Target              string       `json:"target"`
	Method              string       `json:"method,omitempty"`
	ExpectedStatusRange string       `json:"expected_status_range,omitempty"`
	IntervalMs          int64        `json:"interval_ms"`
	TimeoutMs           int64        `json:"timeout_ms"`
	Enabled             bool         `json:"enabled"`
	CreatedAt           string       `json:"created_at"`
	UpdatedAt           string       `json:"updated_at"`
}

type MonitorStatusResponse struct {
	MonitorID string `json:"monitor_id"`
	State     string `json:"state"`
	UpdatedAt string `json:"updated_at"`
}

type CheckResultResponse struct {
	ID           string             `json:"id"`
	MonitorID    string             `json:"monitor_id"`
	OK           bool               `json:"ok"`
	StatusCode   int                `json:"status_code,omitempty"`
	LatencyMs    int64              `json:"latency_ms"`
	ErrorClass   checker.ErrorClass `json:"error_class,omitempty"`
	ErrorDetail  string             `json:"error_detail,omitempty"`
	AttemptCount int                `json:"attempt_count"`
	CheckedAt    string             `json:"checked_at"`
}

func toMonitorResponse(m monitor.Monitor) MonitorResponse {
	return MonitorResponse{
		ID:                  m.ID,
		Name:                m.Name,
		Kind:                m.Kind,
		Target:              m.TargetURL,
		Method:              m.Method,
		ExpectedStatusRange: m.ExpectedStatusRange,
		IntervalMs:          m.Interval.Milliseconds(),
		TimeoutMs:           m.Timeout.Milliseconds(),
		Enabled:             m.Enabled,
		CreatedAt:           m.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:           m.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func toMonitorStatusResponse(st service.MonitorStatus) MonitorStatusResponse {
	return MonitorStatusResponse{
		MonitorID: st.MonitorID,
		State:     string(st.State),
		UpdatedAt: st.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func toCheckResultResponse(r checker.CheckResult) CheckResultResponse {
	return CheckResultResponse{
		ID:           r.ID,
		MonitorID:    r.MonitorID,
		OK:           r.OK,
		StatusCode:   r.StatusCode,
		LatencyMs:    r.Latency.Milliseconds(),
		ErrorClass:   r.ErrorClass,
		ErrorDetail:  r.ErrorDetail,
		AttemptCount: r.AttemptCount,
		CheckedAt:    r.CheckedAt.UTC().Format(time.RFC3339),
	}
}
