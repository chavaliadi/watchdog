package alert

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chavaliadi/watchdog/internal/checker"
	"github.com/chavaliadi/watchdog/internal/monitor"
	"github.com/chavaliadi/watchdog/internal/state"
)

func TestSanitizeTarget(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "clean URL",
			input:    "https://api.example.com/health",
			expected: "https://api.example.com/health",
		},
		{
			name:     "URL with credentials",
			input:    "https://admin:supersecret@api.example.com/health?query=1",
			expected: "https://api.example.com/health?query=1",
		},
		{
			name:     "empty URL",
			input:    "",
			expected: "",
		},
		{
			name:     "host:port without scheme",
			input:    "api.example.com:8080",
			expected: "api.example.com:8080",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizeTarget(tc.input)
			if got != tc.expected {
				t.Fatalf("expected %q, got %q", tc.expected, got)
			}
		})
	}
}

func TestSanitizeErrorDetail(t *testing.T) {
	t.Run("truncates oversized error string", func(t *testing.T) {
		huge := strings.Repeat("A", 500)
		sanitized := SanitizeErrorDetail(huge)
		if len(sanitized) > maxErrorDetailLen+3 { // max + "..."
			t.Fatalf("expected truncated length <= %d, got %d", maxErrorDetailLen+3, len(sanitized))
		}
		if !strings.HasSuffix(sanitized, "...") {
			t.Fatalf("expected truncation ellipsis suffix")
		}
	})

	t.Run("strips control characters", func(t *testing.T) {
		dirty := "error\nline2\rline3\tline4\x00null"
		sanitized := SanitizeErrorDetail(dirty)
		if strings.ContainsAny(sanitized, "\n\r\t\x00") {
			t.Fatalf("sanitized detail still contains control characters: %q", sanitized)
		}
	})
}

func TestNewAlertEvent_PayloadStructure(t *testing.T) {
	m := monitor.Monitor{
		ID:        "mon-123",
		Name:      "Production API",
		Kind:      monitor.KindHTTP,
		TargetURL: "https://user:pass@service.internal/health",
	}

	res := checker.CheckResult{
		OK:           false,
		StatusCode:   503,
		ErrorClass:   checker.ErrorClassStatus,
		ErrorDetail:  "upstream service unavailable",
		AttemptCount: 3,
		Latency:      125 * time.Millisecond,
	}

	event := NewAlertEvent(m, state.StateHealthy, state.StateUnhealthy, res)

	if event.Event != EventMonitorUnhealthy {
		t.Fatalf("expected event %q, got %q", EventMonitorUnhealthy, event.Event)
	}
	if event.Monitor.ID != "mon-123" || event.Monitor.Name != "Production API" {
		t.Fatalf("unexpected monitor info: %+v", event.Monitor)
	}
	if event.Monitor.Target != "https://service.internal/health" {
		t.Fatalf("target was not sanitized of credentials: %q", event.Monitor.Target)
	}
	if event.Transition.From != state.StateHealthy || event.Transition.To != state.StateUnhealthy {
		t.Fatalf("unexpected transition: %+v", event.Transition)
	}
	if event.Check.OK || event.Check.StatusCode != 503 || event.Check.LatencyMS != 125 {
		t.Fatalf("unexpected check summary: %+v", event.Check)
	}
	if event.Timestamp.IsZero() {
		t.Fatalf("timestamp must not be zero")
	}

	// Verify JSON marshal produces expected fields
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("json marshal failed: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("json unmarshal failed: %v", err)
	}

	if parsed["event"] != "monitor_unhealthy" {
		t.Fatalf("expected event in json")
	}
	monMap := parsed["monitor"].(map[string]interface{})
	if monMap["id"] != "mon-123" || monMap["name"] != "Production API" {
		t.Fatalf("expected monitor map in json: %+v", monMap)
	}
	transMap := parsed["transition"].(map[string]interface{})
	if transMap["from"] != "HEALTHY" || transMap["to"] != "UNHEALTHY" {
		t.Fatalf("expected transition map in json: %+v", transMap)
	}
	checkMap := parsed["check"].(map[string]interface{})
	if checkMap["status_code"] != float64(503) || checkMap["attempt_count"] != float64(3) {
		t.Fatalf("expected check map in json: %+v", checkMap)
	}
}

func TestWebhookNotifier_Success(t *testing.T) {
	var received atomic.Int32
	var lastEvent AlertEvent

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected application/json, got %s", ct)
		}
		if err := json.NewDecoder(r.Body).Decode(&lastEvent); err != nil {
			t.Errorf("failed to decode body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer ts.Close()

	notifier := NewWebhookNotifier(WebhookConfig{
		WebhookURL: ts.URL,
		Timeout:    2 * time.Second,
	})

	event := AlertEvent{
		Event: EventMonitorUnhealthy,
		Monitor: MonitorInfo{
			ID:   "m1",
			Name: "Test Mon",
		},
		Transition: TransitionInfo{
			From: state.StateHealthy,
			To:   state.StateUnhealthy,
		},
	}

	err := notifier.Notify(context.Background(), event)
	if err != nil {
		t.Fatalf("unexpected notify error: %v", err)
	}
	if received.Load() != 1 {
		t.Fatalf("expected 1 request, got %d", received.Load())
	}
	if lastEvent.Monitor.ID != "m1" {
		t.Fatalf("expected monitor m1 received, got %q", lastEvent.Monitor.ID)
	}
}

func TestWebhookNotifier_EmptyURL_Disabled(t *testing.T) {
	notifier := NewWebhookNotifier(WebhookConfig{
		WebhookURL: "",
	})

	if _, ok := notifier.(NoopNotifier); !ok {
		t.Fatalf("expected NoopNotifier when webhook URL is empty")
	}

	err := notifier.Notify(context.Background(), AlertEvent{Event: "test"})
	if err != nil {
		t.Fatalf("expected nil error on disabled notifier: %v", err)
	}
}

func TestWebhookNotifier_HTTPFailure_DoesNotLeakSecret(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal crash"))
	}))
	defer ts.Close()

	// Append a sensitive token to the path to verify it does not leak into the error
	secretPath := ts.URL + "/secret-token-12345"

	notifier := NewWebhookNotifier(WebhookConfig{
		WebhookURL: secretPath,
		Timeout:    2 * time.Second,
	})

	err := notifier.Notify(context.Background(), AlertEvent{Event: "test"})
	if err == nil {
		t.Fatalf("expected error on 500 response, got nil")
	}
	if strings.Contains(err.Error(), "secret-token-12345") {
		t.Fatalf("error message leaked secret webhook URL: %v", err)
	}
}

func TestWebhookNotifier_TimeoutEnforced(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	notifier := NewWebhookNotifier(WebhookConfig{
		WebhookURL: ts.URL,
		Timeout:    50 * time.Millisecond,
	})

	start := time.Now()
	err := notifier.Notify(context.Background(), AlertEvent{Event: "test"})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("expected timeout error, got nil")
	}
	if elapsed > 150*time.Millisecond {
		t.Fatalf("expected notify to time out within ~50ms, took %v", elapsed)
	}
}
