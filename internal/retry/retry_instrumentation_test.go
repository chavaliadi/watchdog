package retry_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/chavaliadi/watchdog/internal/checker"
	"github.com/chavaliadi/watchdog/internal/monitor"
	"github.com/chavaliadi/watchdog/internal/retry"
)

type retryEvent struct {
	kind    string
	outcome string
}

type mockRetryRecorder struct {
	mu      sync.Mutex
	retries []retryEvent
}

func (m *mockRetryRecorder) RecordCheck(kind string, ok bool, errClass string, duration time.Duration) {}
func (m *mockRetryRecorder) RecordRetry(kind string, outcome string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.retries = append(m.retries, retryEvent{kind: kind, outcome: outcome})
}
func (m *mockRetryRecorder) RecordCycleError(stage string) {}
func (m *mockRetryRecorder) RecordDBOperation(op string, ok bool, duration time.Duration) {}
func (m *mockRetryRecorder) RecordHTTPRequest(method, route string, statusCode int, duration time.Duration) {}
func (m *mockRetryRecorder) RecordActiveMonitors(kind string, count int) {}

type scriptedChecker struct {
	mu      sync.Mutex
	results []checker.CheckResult
	errs    []error
	callIdx int
}

func (s *scriptedChecker) Check(ctx context.Context, m monitor.Monitor) (checker.CheckResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.callIdx
	s.callIdx++
	if idx < len(s.errs) && s.errs[idx] != nil {
		return checker.CheckResult{}, s.errs[idx]
	}
	if idx < len(s.results) {
		return s.results[idx], nil
	}
	return checker.CheckResult{OK: false}, nil
}

func noopSleeper(ctx context.Context, d time.Duration) error {
	return nil
}

func zeroJitter(base time.Duration) time.Duration {
	return 0
}

func TestRetryInstrumentation_FirstAttemptSuccess_NoRetryRecorded(t *testing.T) {
	rec := &mockRetryRecorder{}
	chk := &scriptedChecker{
		results: []checker.CheckResult{{OK: true}},
	}
	r := retry.New(chk, retry.Config{
		MaxAttempts: 3,
		Sleeper:     noopSleeper,
		JitterFn:    zeroJitter,
		Recorder:    rec,
	})

	m := monitor.Monitor{Kind: monitor.KindHTTP}
	res, err := r.Check(context.Background(), m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.OK {
		t.Fatalf("expected OK, got false")
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.retries) != 0 {
		t.Fatalf("expected 0 retry recordings on first attempt success, got %d", len(rec.retries))
	}
}

func TestRetryInstrumentation_Recovered_RecordedOnce(t *testing.T) {
	rec := &mockRetryRecorder{}
	chk := &scriptedChecker{
		results: []checker.CheckResult{
			{OK: false}, // attempt 1 fails
			{OK: true},  // attempt 2 succeeds
		},
	}
	r := retry.New(chk, retry.Config{
		MaxAttempts: 3,
		Sleeper:     noopSleeper,
		JitterFn:    zeroJitter,
		Recorder:    rec,
	})

	m := monitor.Monitor{Kind: monitor.KindHTTP}
	res, err := r.Check(context.Background(), m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.OK {
		t.Fatalf("expected OK on attempt 2")
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.retries) != 1 {
		t.Fatalf("expected 1 retry event, got %d", len(rec.retries))
	}
	if rec.retries[0].kind != "http" || rec.retries[0].outcome != "recovered" {
		t.Fatalf("expected ('http', 'recovered'), got %+v", rec.retries[0])
	}
}

func TestRetryInstrumentation_Exhausted_RecordedOnce(t *testing.T) {
	rec := &mockRetryRecorder{}
	chk := &scriptedChecker{
		results: []checker.CheckResult{
			{OK: false}, // attempt 1 fails
			{OK: false}, // attempt 2 fails
			{OK: false}, // attempt 3 fails
		},
	}
	r := retry.New(chk, retry.Config{
		MaxAttempts: 3,
		Sleeper:     noopSleeper,
		JitterFn:    zeroJitter,
		Recorder:    rec,
	})

	m := monitor.Monitor{Kind: monitor.KindTCP}
	res, err := r.Check(context.Background(), m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.OK {
		t.Fatalf("expected failed result")
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.retries) != 1 {
		t.Fatalf("expected 1 retry event, got %d", len(rec.retries))
	}
	if rec.retries[0].kind != "tcp" || rec.retries[0].outcome != "exhausted" {
		t.Fatalf("expected ('tcp', 'exhausted'), got %+v", rec.retries[0])
	}
}

func TestRetryInstrumentation_MaxAttemptsOne_NoRetryRecorded(t *testing.T) {
	rec := &mockRetryRecorder{}
	chk := &scriptedChecker{
		results: []checker.CheckResult{
			{OK: false},
		},
	}
	r := retry.New(chk, retry.Config{
		MaxAttempts: 1,
		Sleeper:     noopSleeper,
		JitterFn:    zeroJitter,
		Recorder:    rec,
	})

	m := monitor.Monitor{Kind: monitor.KindHTTP}
	res, err := r.Check(context.Background(), m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.OK {
		t.Fatalf("expected failed result")
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.retries) != 0 {
		t.Fatalf("expected 0 retry events when maxAttempts=1, got %d", len(rec.retries))
	}
}

func TestRetryInstrumentation_SystemError_NoRetryRecorded(t *testing.T) {
	rec := &mockRetryRecorder{}
	chk := &scriptedChecker{
		errs: []error{errors.New("fatal configuration error")},
	}
	r := retry.New(chk, retry.Config{
		MaxAttempts: 3,
		Sleeper:     noopSleeper,
		JitterFn:    zeroJitter,
		Recorder:    rec,
	})

	m := monitor.Monitor{Kind: monitor.KindHTTP}
	_, err := r.Check(context.Background(), m)
	if err == nil {
		t.Fatalf("expected error")
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.retries) != 0 {
		t.Fatalf("expected 0 retry events on system error, got %d", len(rec.retries))
	}
}
