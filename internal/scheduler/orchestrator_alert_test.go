package scheduler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chavaliadi/watchdog/internal/alert"
	"github.com/chavaliadi/watchdog/internal/checker"
	"github.com/chavaliadi/watchdog/internal/monitor"
	"github.com/chavaliadi/watchdog/internal/retry"
	"github.com/chavaliadi/watchdog/internal/scheduler"
	"github.com/chavaliadi/watchdog/internal/state"
)

type mockNotifier struct {
	mu     sync.Mutex
	events []alert.AlertEvent
	err    error
}

func (m *mockNotifier) Notify(ctx context.Context, event alert.AlertEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, event)
	return m.err
}

func (m *mockNotifier) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.events)
}

func (m *mockNotifier) lastEvent() alert.AlertEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.events) == 0 {
		return alert.AlertEvent{}
	}
	return m.events[len(m.events)-1]
}

type staticChecker struct {
	res checker.CheckResult
	err error
}

func (s *staticChecker) Check(ctx context.Context, m monitor.Monitor) (checker.CheckResult, error) {
	return s.res, s.err
}

func fastRetryConfig() retry.Config {
	return retry.Config{
		MaxAttempts: 1,
		BaseDelay:   time.Millisecond,
		MaxDelay:    time.Millisecond,
		Sleeper: func(ctx context.Context, d time.Duration) error {
			return nil
		},
		JitterFn: func(d time.Duration) time.Duration {
			return d
		},
	}
}

// 1. UNKNOWN -> HEALTHY does not alert
func TestOrchestrator_Alert_UnknownToHealthy(t *testing.T) {
	notifier := &mockNotifier{}
	chk := &staticChecker{res: checker.CheckResult{OK: true, StatusCode: 200}}
	orch := scheduler.NewOrchestrator(chk, chk, fastRetryConfig()).WithNotifier(notifier)

	m := monitor.Monitor{ID: "mon-1", Kind: monitor.KindHTTP, TargetURL: "http://example.com"}
	res, err := orch.RunCycle(context.Background(), m, state.StateUnknown)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.TransitionResult.NextState != state.StateHealthy {
		t.Fatalf("expected next state HEALTHY, got %v", res.TransitionResult.NextState)
	}
	if notifier.count() != 0 {
		t.Fatalf("expected 0 alerts for UNKNOWN -> HEALTHY, got %d", notifier.count())
	}
}

// 2. UNKNOWN -> UNHEALTHY alerts exactly once
func TestOrchestrator_Alert_UnknownToUnhealthy(t *testing.T) {
	notifier := &mockNotifier{}
	chk := &staticChecker{res: checker.CheckResult{OK: false, StatusCode: 500, ErrorClass: checker.ErrorClassStatus}}
	orch := scheduler.NewOrchestrator(chk, chk, fastRetryConfig()).WithNotifier(notifier)

	m := monitor.Monitor{ID: "mon-1", Name: "Service API", Kind: monitor.KindHTTP, TargetURL: "http://example.com"}
	res, err := orch.RunCycle(context.Background(), m, state.StateUnknown)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.TransitionResult.NextState != state.StateUnhealthy {
		t.Fatalf("expected next state UNHEALTHY, got %v", res.TransitionResult.NextState)
	}
	if notifier.count() != 1 {
		t.Fatalf("expected exactly 1 alert for UNKNOWN -> UNHEALTHY, got %d", notifier.count())
	}

	ev := notifier.lastEvent()
	if ev.Transition.From != state.StateUnknown || ev.Transition.To != state.StateUnhealthy {
		t.Fatalf("unexpected transition info: %+v", ev.Transition)
	}
}

// 3. HEALTHY -> UNHEALTHY alerts exactly once
func TestOrchestrator_Alert_HealthyToUnhealthy(t *testing.T) {
	notifier := &mockNotifier{}
	chk := &staticChecker{res: checker.CheckResult{OK: false, StatusCode: 502, ErrorClass: checker.ErrorClassStatus}}
	orch := scheduler.NewOrchestrator(chk, chk, fastRetryConfig()).WithNotifier(notifier)

	m := monitor.Monitor{ID: "mon-1", Name: "Service API", Kind: monitor.KindHTTP, TargetURL: "http://example.com"}
	res, err := orch.RunCycle(context.Background(), m, state.StateHealthy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.TransitionResult.NextState != state.StateUnhealthy {
		t.Fatalf("expected next state UNHEALTHY, got %v", res.TransitionResult.NextState)
	}
	if notifier.count() != 1 {
		t.Fatalf("expected exactly 1 alert for HEALTHY -> UNHEALTHY, got %d", notifier.count())
	}

	ev := notifier.lastEvent()
	if ev.Transition.From != state.StateHealthy || ev.Transition.To != state.StateUnhealthy {
		t.Fatalf("unexpected transition info: %+v", ev.Transition)
	}
}

// 4. UNHEALTHY -> UNHEALTHY does not alert repeatedly
func TestOrchestrator_Alert_UnhealthyToUnhealthy_NoRepeatedAlert(t *testing.T) {
	notifier := &mockNotifier{}
	chk := &staticChecker{res: checker.CheckResult{OK: false, StatusCode: 500, ErrorClass: checker.ErrorClassStatus}}
	orch := scheduler.NewOrchestrator(chk, chk, fastRetryConfig()).WithNotifier(notifier)

	m := monitor.Monitor{ID: "mon-1", Kind: monitor.KindHTTP, TargetURL: "http://example.com"}

	// First cycle: already UNHEALTHY, stays UNHEALTHY
	res, err := orch.RunCycle(context.Background(), m, state.StateUnhealthy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.TransitionResult.NextState != state.StateUnhealthy {
		t.Fatalf("expected state to stay UNHEALTHY")
	}
	if notifier.count() != 0 {
		t.Fatalf("expected 0 alerts when remaining UNHEALTHY, got %d", notifier.count())
	}

	// Second cycle: still failing
	_, err = orch.RunCycle(context.Background(), m, state.StateUnhealthy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if notifier.count() != 0 {
		t.Fatalf("expected 0 alerts after repeated failure, got %d", notifier.count())
	}
}

// 5. UNHEALTHY -> HEALTHY does not emit an outage alert
func TestOrchestrator_Alert_UnhealthyToHealthy_NoOutageAlert(t *testing.T) {
	notifier := &mockNotifier{}
	chk := &staticChecker{res: checker.CheckResult{OK: true, StatusCode: 200}}
	orch := scheduler.NewOrchestrator(chk, chk, fastRetryConfig()).WithNotifier(notifier)

	m := monitor.Monitor{ID: "mon-1", Kind: monitor.KindHTTP, TargetURL: "http://example.com"}
	res, err := orch.RunCycle(context.Background(), m, state.StateUnhealthy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.TransitionResult.NextState != state.StateHealthy {
		t.Fatalf("expected next state HEALTHY, got %v", res.TransitionResult.NextState)
	}
	if notifier.count() != 0 {
		t.Fatalf("expected 0 outage alerts for UNHEALTHY -> HEALTHY recovery, got %d", notifier.count())
	}
}

// 6. Healthy -> unhealthy with retries still emits exactly one alert
func TestOrchestrator_Alert_HealthyToUnhealthy_WithRetries(t *testing.T) {
	notifier := &mockNotifier{}

	var attempts int
	retryChecker := &callbackChecker{
		fn: func(ctx context.Context, m monitor.Monitor) (checker.CheckResult, error) {
			attempts++
			return checker.CheckResult{
				OK:           false,
				StatusCode:   500,
				ErrorClass:   checker.ErrorClassStatus,
				AttemptCount: attempts,
			}, nil
		},
	}

	retryCfg := retry.Config{
		MaxAttempts: 3,
		BaseDelay:   time.Millisecond,
		MaxDelay:    time.Millisecond,
		Sleeper:     func(ctx context.Context, d time.Duration) error { return nil },
		JitterFn:    func(d time.Duration) time.Duration { return d },
	}

	orch := scheduler.NewOrchestrator(retryChecker, retryChecker, retryCfg).WithNotifier(notifier)

	m := monitor.Monitor{ID: "mon-retry", Kind: monitor.KindHTTP, TargetURL: "http://example.com"}
	res, err := orch.RunCycle(context.Background(), m, state.StateHealthy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.TransitionResult.NextState != state.StateUnhealthy {
		t.Fatalf("expected next state UNHEALTHY, got %v", res.TransitionResult.NextState)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 retry attempts, got %d", attempts)
	}
	if notifier.count() != 1 {
		t.Fatalf("expected exactly 1 alert despite 3 retries, got %d", notifier.count())
	}
	if notifier.lastEvent().Check.AttemptCount != 3 {
		t.Fatalf("expected alert payload attempt_count 3, got %d", notifier.lastEvent().Check.AttemptCount)
	}
}

// 7. Webhook payload contains expected fields
func TestOrchestrator_Alert_WebhookPayloadFields(t *testing.T) {
	var payloadBytes []byte
	var receivedCount atomic.Int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedCount.Add(1)
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(r.Body)
		payloadBytes = buf.Bytes()
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	webhookNotifier := alert.NewWebhookNotifier(alert.WebhookConfig{
		WebhookURL: ts.URL,
		Timeout:    2 * time.Second,
	})

	chk := &staticChecker{res: checker.CheckResult{
		OK:           false,
		StatusCode:   504,
		ErrorClass:   checker.ErrorClassTimeout,
		ErrorDetail:  "gateway timeout after 5000ms",
		AttemptCount: 2,
		Latency:      5000 * time.Millisecond,
	}}

	retryCfg := retry.Config{
		MaxAttempts: 2,
		BaseDelay:   time.Millisecond,
		MaxDelay:    time.Millisecond,
		Sleeper:     func(ctx context.Context, d time.Duration) error { return nil },
		JitterFn:    func(d time.Duration) time.Duration { return d },
	}

	orch := scheduler.NewOrchestrator(chk, chk, retryCfg).WithNotifier(webhookNotifier)

	m := monitor.Monitor{
		ID:        "mon-field-test",
		Name:      "Payment Gateway",
		Kind:      monitor.KindHTTP,
		TargetURL: "https://user:secret@payments.internal/status",
	}

	_, err := orch.RunCycle(context.Background(), m, state.StateHealthy)
	if err != nil {
		t.Fatalf("unexpected RunCycle error: %v", err)
	}

	if receivedCount.Load() != 1 {
		t.Fatalf("expected 1 webhook request, got %d", receivedCount.Load())
	}

	var parsed struct {
		Event   string `json:"event"`
		Monitor struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Kind   string `json:"kind"`
			Target string `json:"target"`
		} `json:"monitor"`
		Transition struct {
			From string `json:"from"`
			To   string `json:"to"`
		} `json:"transition"`
		Check struct {
			OK           bool   `json:"ok"`
			StatusCode   int    `json:"status_code"`
			ErrorClass   string `json:"error_class"`
			ErrorDetail  string `json:"error_detail"`
			AttemptCount int    `json:"attempt_count"`
			LatencyMS    int64  `json:"latency_ms"`
		} `json:"check"`
		Timestamp string `json:"timestamp"`
	}

	if err := json.Unmarshal(payloadBytes, &parsed); err != nil {
		t.Fatalf("failed to parse json payload: %v", err)
	}

	if parsed.Event != "monitor_unhealthy" {
		t.Errorf("expected event monitor_unhealthy, got %s", parsed.Event)
	}
	if parsed.Monitor.ID != "mon-field-test" || parsed.Monitor.Name != "Payment Gateway" {
		t.Errorf("unexpected monitor info: %+v", parsed.Monitor)
	}
	if parsed.Monitor.Target != "https://payments.internal/status" {
		t.Errorf("expected target to be sanitized of user:secret, got %s", parsed.Monitor.Target)
	}
	if parsed.Transition.From != "HEALTHY" || parsed.Transition.To != "UNHEALTHY" {
		t.Errorf("unexpected transition: %+v", parsed.Transition)
	}
	if parsed.Check.OK || parsed.Check.StatusCode != 504 || parsed.Check.ErrorClass != "timeout" {
		t.Errorf("unexpected check details: %+v", parsed.Check)
	}
	if parsed.Check.LatencyMS != 5000 || parsed.Check.AttemptCount != 2 {
		t.Errorf("unexpected check metrics: %+v", parsed.Check)
	}
	if parsed.Timestamp == "" {
		t.Errorf("timestamp was empty")
	}
}

// 8. Missing WATCHDOG_ALERT_WEBHOOK_URL disables alerting
func TestOrchestrator_Alert_EmptyURLDisablesAlerting(t *testing.T) {
	webhookNotifier := alert.NewWebhookNotifier(alert.WebhookConfig{
		WebhookURL: "",
	})

	chk := &staticChecker{res: checker.CheckResult{OK: false, StatusCode: 500}}
	orch := scheduler.NewOrchestrator(chk, chk, fastRetryConfig()).WithNotifier(webhookNotifier)

	m := monitor.Monitor{ID: "mon-empty-url", Kind: monitor.KindHTTP, TargetURL: "http://example.com"}
	res, err := orch.RunCycle(context.Background(), m, state.StateHealthy)
	if err != nil {
		t.Fatalf("unexpected RunCycle error: %v", err)
	}
	if res.TransitionResult.NextState != state.StateUnhealthy {
		t.Fatalf("expected state transition to UNHEALTHY")
	}
}

// 9. Webhook HTTP failure does not fail the monitor cycle
func TestOrchestrator_Alert_HTTPFailureDoesNotFailCycle(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal crash"))
	}))
	defer ts.Close()

	webhookNotifier := alert.NewWebhookNotifier(alert.WebhookConfig{
		WebhookURL: ts.URL,
		Timeout:    time.Second,
	})

	chk := &staticChecker{res: checker.CheckResult{OK: false, StatusCode: 500}}
	orch := scheduler.NewOrchestrator(chk, chk, fastRetryConfig()).WithNotifier(webhookNotifier)

	m := monitor.Monitor{ID: "mon-failing-webhook", Kind: monitor.KindHTTP, TargetURL: "http://example.com"}
	res, err := orch.RunCycle(context.Background(), m, state.StateHealthy)

	// Invariant: webhook failure must NOT fail the monitor cycle!
	if err != nil {
		t.Fatalf("expected RunCycle to succeed despite webhook failure, got: %v", err)
	}
	if res.TransitionResult.NextState != state.StateUnhealthy {
		t.Fatalf("expected next state UNHEALTHY, got %v", res.TransitionResult.NextState)
	}
}

// 10. Webhook timeout does not block the monitor indefinitely
func TestOrchestrator_Alert_TimeoutDoesNotBlockIndefinitely(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	webhookNotifier := alert.NewWebhookNotifier(alert.WebhookConfig{
		WebhookURL: ts.URL,
		Timeout:    40 * time.Millisecond, // Strict timeout
	})

	chk := &staticChecker{res: checker.CheckResult{OK: false, StatusCode: 500}}
	orch := scheduler.NewOrchestrator(chk, chk, fastRetryConfig()).WithNotifier(webhookNotifier)

	m := monitor.Monitor{ID: "mon-slow-webhook", Kind: monitor.KindHTTP, TargetURL: "http://example.com"}

	start := time.Now()
	res, err := orch.RunCycle(context.Background(), m, state.StateHealthy)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("expected cycle to succeed, got: %v", err)
	}
	if res.TransitionResult.NextState != state.StateUnhealthy {
		t.Fatalf("expected state UNHEALTHY")
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("expected alert delivery to time out quickly (~40ms), took %v", elapsed)
	}
}

// 11. Sensitive configuration is not logged
func TestOrchestrator_Alert_SensitiveConfigNotLogged(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))
	slog.SetDefault(logger)

	secretToken := "secret-auth-token-xyz-987"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer ts.Close()

	webhookNotifier := alert.NewWebhookNotifier(alert.WebhookConfig{
		WebhookURL: ts.URL + "/" + secretToken,
		Timeout:    time.Second,
	})

	chk := &staticChecker{res: checker.CheckResult{OK: false, StatusCode: 500}}
	orch := scheduler.NewOrchestrator(chk, chk, fastRetryConfig()).WithNotifier(webhookNotifier)

	m := monitor.Monitor{ID: "mon-sensitive", Kind: monitor.KindHTTP, TargetURL: "http://example.com"}
	_, err := orch.RunCycle(context.Background(), m, state.StateHealthy)
	if err != nil {
		t.Fatalf("unexpected cycle error: %v", err)
	}

	loggedOutput := logBuf.String()
	if strings.Contains(loggedOutput, secretToken) {
		t.Fatalf("secret token was found in logged output: %s", loggedOutput)
	}
}

// 12. Alerting does not introduce a race
func TestOrchestrator_Alert_NoRaceUnderConcurrency(t *testing.T) {
	notifier := &mockNotifier{err: errors.New("simulated intermittent webhook failure")}
	chk := &staticChecker{res: checker.CheckResult{OK: false, StatusCode: 503}}
	orch := scheduler.NewOrchestrator(chk, chk, fastRetryConfig()).WithNotifier(notifier)

	var wg sync.WaitGroup
	concurrency := 20

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			m := monitor.Monitor{
				ID:        "mon-concurrent",
				Kind:      monitor.KindHTTP,
				TargetURL: "http://example.com",
			}
			_, _ = orch.RunCycle(context.Background(), m, state.StateHealthy)
		}(i)
	}

	wg.Wait()
	if notifier.count() != concurrency {
		t.Fatalf("expected %d alert invocations, got %d", concurrency, notifier.count())
	}
}

type callbackChecker struct {
	fn func(ctx context.Context, m monitor.Monitor) (checker.CheckResult, error)
}

func (c *callbackChecker) Check(ctx context.Context, m monitor.Monitor) (checker.CheckResult, error) {
	return c.fn(ctx, m)
}
