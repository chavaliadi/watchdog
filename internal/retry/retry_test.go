package retry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chavaliadi/watchdog/internal/checker"
	"github.com/chavaliadi/watchdog/internal/monitor"
)

type sequenceChecker struct {
	results []checker.CheckResult
	errs    []error
	callIdx int
	calls   int
}

func (s *sequenceChecker) Check(ctx context.Context, m monitor.Monitor) (checker.CheckResult, error) {
	s.calls++
	idx := s.callIdx
	s.callIdx++

	var res checker.CheckResult
	var err error
	if idx < len(s.results) {
		res = s.results[idx]
	}
	if idx < len(s.errs) {
		err = s.errs[idx]
	}
	return res, err
}

type mockSleeper struct {
	sleepCalls []time.Duration
	sleepErr   error
}

func (m *mockSleeper) Sleep(ctx context.Context, d time.Duration) error {
	m.sleepCalls = append(m.sleepCalls, d)
	if m.sleepErr != nil {
		return m.sleepErr
	}
	return nil
}

func identityJitter(base time.Duration) time.Duration {
	return base
}

func testMonitor() monitor.Monitor {
	return monitor.Monitor{
		ID:        "mon-retry-test",
		Kind:      monitor.KindHTTP,
		TargetURL: "http://example.com",
	}
}

func TestRetrier_InterfaceConformance(t *testing.T) {
	var c checker.Checker = New(&sequenceChecker{}, Config{})
	if c == nil {
		t.Fatal("expected non-nil checker.Checker")
	}
}

func TestRetrier_SuccessFirstAttempt(t *testing.T) {
	mockCheck := &sequenceChecker{
		results: []checker.CheckResult{
			{MonitorID: "mon-retry-test", OK: true, StatusCode: 200},
		},
		errs: []error{nil},
	}
	sleeper := &mockSleeper{}

	r := New(mockCheck, Config{
		Sleeper:  sleeper.Sleep,
		JitterFn: identityJitter,
	})

	res, err := r.Check(context.Background(), testMonitor())
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	if !res.OK {
		t.Errorf("expected res.OK to be true")
	}
	if res.AttemptCount != 1 {
		t.Errorf("expected AttemptCount 1, got %d", res.AttemptCount)
	}
	if mockCheck.calls != 1 {
		t.Errorf("expected 1 checker call, got %d", mockCheck.calls)
	}
	if len(sleeper.sleepCalls) != 0 {
		t.Errorf("expected 0 sleep calls on first attempt success, got %d", len(sleeper.sleepCalls))
	}
}

func TestRetrier_FailureThenSuccess(t *testing.T) {
	mockCheck := &sequenceChecker{
		results: []checker.CheckResult{
			{MonitorID: "mon-retry-test", OK: false, ErrorClass: checker.ErrorClassStatus, StatusCode: 503},
			{MonitorID: "mon-retry-test", OK: true, StatusCode: 200},
		},
		errs: []error{nil, nil},
	}
	sleeper := &mockSleeper{}

	r := New(mockCheck, Config{
		Sleeper:  sleeper.Sleep,
		JitterFn: identityJitter,
	})

	res, err := r.Check(context.Background(), testMonitor())
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	if !res.OK {
		t.Errorf("expected res.OK to be true")
	}
	if res.StatusCode != 200 {
		t.Errorf("expected status code 200, got %d", res.StatusCode)
	}
	if res.AttemptCount != 2 {
		t.Errorf("expected AttemptCount 2, got %d", res.AttemptCount)
	}
	if mockCheck.calls != 2 {
		t.Errorf("expected 2 checker calls, got %d", mockCheck.calls)
	}
	if len(sleeper.sleepCalls) != 1 {
		t.Errorf("expected 1 sleep call, got %d", len(sleeper.sleepCalls))
	}
}

func TestRetrier_FailureTwiceThenSuccess(t *testing.T) {
	mockCheck := &sequenceChecker{
		results: []checker.CheckResult{
			{MonitorID: "mon-retry-test", OK: false, ErrorClass: checker.ErrorClassConnRefused},
			{MonitorID: "mon-retry-test", OK: false, ErrorClass: checker.ErrorClassConnRefused},
			{MonitorID: "mon-retry-test", OK: true},
		},
		errs: []error{nil, nil, nil},
	}
	sleeper := &mockSleeper{}

	r := New(mockCheck, Config{
		Sleeper:  sleeper.Sleep,
		JitterFn: identityJitter,
	})

	res, err := r.Check(context.Background(), testMonitor())
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	if !res.OK {
		t.Errorf("expected res.OK to be true")
	}
	if res.AttemptCount != 3 {
		t.Errorf("expected AttemptCount 3, got %d", res.AttemptCount)
	}
	if mockCheck.calls != 3 {
		t.Errorf("expected 3 checker calls, got %d", mockCheck.calls)
	}
	if len(sleeper.sleepCalls) != 2 {
		t.Errorf("expected 2 sleep calls, got %d", len(sleeper.sleepCalls))
	}
}

func TestRetrier_AllAttemptsFail(t *testing.T) {
	mockCheck := &sequenceChecker{
		results: []checker.CheckResult{
			{MonitorID: "mon-retry-test", OK: false, ErrorDetail: "fail-1"},
			{MonitorID: "mon-retry-test", OK: false, ErrorDetail: "fail-2"},
			{MonitorID: "mon-retry-test", OK: false, ErrorDetail: "fail-3"},
		},
		errs: []error{nil, nil, nil},
	}
	sleeper := &mockSleeper{}

	r := New(mockCheck, Config{
		Sleeper:  sleeper.Sleep,
		JitterFn: identityJitter,
	})

	res, err := r.Check(context.Background(), testMonitor())
	if err != nil {
		t.Fatalf("expected nil Go error for monitor failure, got: %v", err)
	}

	if res.OK {
		t.Errorf("expected res.OK to be false")
	}
	if res.AttemptCount != 3 {
		t.Errorf("expected AttemptCount 3, got %d", res.AttemptCount)
	}
	if res.ErrorDetail != "fail-3" {
		t.Errorf("expected last attempt ErrorDetail 'fail-3', got %q", res.ErrorDetail)
	}
	if mockCheck.calls != 3 {
		t.Errorf("expected 3 checker calls, got %d", mockCheck.calls)
	}
	if len(sleeper.sleepCalls) != 2 {
		t.Errorf("expected 2 sleep calls between 3 attempts, got %d", len(sleeper.sleepCalls))
	}
}

func TestRetrier_GoErrorNotRetried(t *testing.T) {
	configErr := errors.New("invalid target configuration")
	mockCheck := &sequenceChecker{
		results: []checker.CheckResult{{}},
		errs:    []error{configErr},
	}
	sleeper := &mockSleeper{}

	r := New(mockCheck, Config{
		Sleeper:  sleeper.Sleep,
		JitterFn: identityJitter,
	})

	_, err := r.Check(context.Background(), testMonitor())
	if !errors.Is(err, configErr) {
		t.Fatalf("expected configErr, got: %v", err)
	}
	if mockCheck.calls != 1 {
		t.Errorf("expected exactly 1 checker call for Go error, got %d", mockCheck.calls)
	}
	if len(sleeper.sleepCalls) != 0 {
		t.Errorf("expected 0 sleep calls on Go error, got %d", len(sleeper.sleepCalls))
	}
}

func TestRetrier_ExactlyThreeAttemptsMaximum(t *testing.T) {
	// Provide 5 failing results in mock checker
	mockCheck := &sequenceChecker{
		results: []checker.CheckResult{
			{OK: false}, {OK: false}, {OK: false}, {OK: false}, {OK: false},
		},
		errs: []error{nil, nil, nil, nil, nil},
	}
	sleeper := &mockSleeper{}

	r := New(mockCheck, Config{
		Sleeper:  sleeper.Sleep,
		JitterFn: identityJitter,
	})

	res, err := r.Check(context.Background(), testMonitor())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if res.OK {
		t.Errorf("expected res.OK to be false")
	}
	if res.AttemptCount != 3 {
		t.Errorf("expected AttemptCount 3, got %d", res.AttemptCount)
	}
	if mockCheck.calls != 3 {
		t.Errorf("expected exactly 3 attempts executed, got %d", mockCheck.calls)
	}
	if len(sleeper.sleepCalls) != 2 {
		t.Errorf("expected 2 sleep calls, got %d", len(sleeper.sleepCalls))
	}
}

func TestRetrier_ExponentialBackoffProgression(t *testing.T) {
	mockCheck := &sequenceChecker{
		results: []checker.CheckResult{
			{OK: false}, {OK: false}, {OK: false},
		},
		errs: []error{nil, nil, nil},
	}
	sleeper := &mockSleeper{}

	base := 50 * time.Millisecond
	r := New(mockCheck, Config{
		BaseDelay: base,
		MaxDelay:  1 * time.Second,
		Sleeper:   sleeper.Sleep,
		JitterFn:  identityJitter,
	})

	_, err := r.Check(context.Background(), testMonitor())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(sleeper.sleepCalls) != 2 {
		t.Fatalf("expected 2 sleep calls, got %d", len(sleeper.sleepCalls))
	}

	// Attempt 1 -> Attempt 2 backoff: base * 2^0 = 50ms
	expected1 := base
	if sleeper.sleepCalls[0] != expected1 {
		t.Errorf("expected first sleep %v, got %v", expected1, sleeper.sleepCalls[0])
	}

	// Attempt 2 -> Attempt 3 backoff: base * 2^1 = 100ms
	expected2 := base * 2
	if sleeper.sleepCalls[1] != expected2 {
		t.Errorf("expected second sleep %v, got %v", expected2, sleeper.sleepCalls[1])
	}
}

func TestRetrier_ContextCanceledBeforeAttempt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel before Check call

	mockCheck := &sequenceChecker{
		results: []checker.CheckResult{{OK: true}},
		errs:    []error{nil},
	}
	sleeper := &mockSleeper{}

	r := New(mockCheck, Config{
		Sleeper: sleeper.Sleep,
	})

	_, err := r.Check(ctx, testMonitor())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled error, got: %v", err)
	}
	if mockCheck.calls != 0 {
		t.Errorf("expected 0 checker calls when context already canceled, got %d", mockCheck.calls)
	}
}

func TestRetrier_ContextCanceledDuringBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mockCheck := &sequenceChecker{
		results: []checker.CheckResult{
			{OK: false}, {OK: false},
		},
		errs: []error{nil, nil},
	}

	// Sleeper cancels the context and returns ctx.Err()
	sleeper := &mockSleeper{
		sleepErr: context.Canceled,
	}

	r := New(mockCheck, Config{
		Sleeper: sleeper.Sleep,
	})

	_, err := r.Check(ctx, testMonitor())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled error, got: %v", err)
	}
	if mockCheck.calls != 1 {
		t.Errorf("expected checker called only once before cancellation during sleep, got %d", mockCheck.calls)
	}
}

func TestRetrier_TargetTimeoutRemainsRetryable(t *testing.T) {
	mockCheck := &sequenceChecker{
		results: []checker.CheckResult{
			{OK: false, ErrorClass: checker.ErrorClassTimeout, ErrorDetail: "target connection timed out"},
			{OK: true, StatusCode: 200},
		},
		errs: []error{nil, nil},
	}
	sleeper := &mockSleeper{}

	r := New(mockCheck, Config{
		Sleeper:  sleeper.Sleep,
		JitterFn: identityJitter,
	})

	// Parent context is active
	ctx := context.Background()
	res, err := r.Check(ctx, testMonitor())
	if err != nil {
		t.Fatalf("expected nil error for target timeout observation, got: %v", err)
	}

	if !res.OK {
		t.Errorf("expected res.OK to be true on second attempt")
	}
	if res.AttemptCount != 2 {
		t.Errorf("expected AttemptCount 2, got %d", res.AttemptCount)
	}
	if mockCheck.calls != 2 {
		t.Errorf("expected 2 checker calls, got %d", mockCheck.calls)
	}
}

func TestRetrier_InvalidUsage(t *testing.T) {
	t.Run("nil context", func(t *testing.T) {
		r := New(&sequenceChecker{}, Config{})
		_, err := r.Check(nil, testMonitor())
		if err == nil {
			t.Fatal("expected error for nil context, got nil")
		}
	})

	t.Run("nil checker in retrier", func(t *testing.T) {
		r := New(nil, Config{})
		_, err := r.Check(context.Background(), testMonitor())
		if err == nil {
			t.Fatal("expected error for nil checker, got nil")
		}
	})
}

func TestRetrier_DefaultSleepAndJitter(t *testing.T) {
	t.Run("defaultSleep with canceled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := defaultSleep(ctx, 1*time.Minute)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got: %v", err)
		}
	})

	t.Run("defaultJitter range", func(t *testing.T) {
		base := 100 * time.Millisecond
		for i := 0; i < 20; i++ {
			j := defaultJitter(base)
			if j < 0 || j >= base {
				t.Fatalf("jitter %v out of range [0, %v)", j, base)
			}
		}

		if defaultJitter(0) != 0 {
			t.Errorf("expected 0 jitter for 0 base")
		}
	})
}
