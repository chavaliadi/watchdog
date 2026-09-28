package benchmark_test

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chavaliadi/watchdog/internal/checker"
	"github.com/chavaliadi/watchdog/internal/monitor"
	"github.com/chavaliadi/watchdog/internal/retry"
)

type mockChecker struct {
	checkFunc func(ctx context.Context, m monitor.Monitor) (checker.CheckResult, error)
	calls     int64
}

func (m *mockChecker) Check(ctx context.Context, mon monitor.Monitor) (checker.CheckResult, error) {
	atomic.AddInt64(&m.calls, 1)
	if m.checkFunc != nil {
		return m.checkFunc(ctx, mon)
	}
	return checker.CheckResult{OK: true, AttemptCount: 1}, nil
}

func TestRetryLayer_Scenarios(t *testing.T) {
	testMon := monitor.Monitor{
		ID:        "mon-retry-test",
		Kind:      monitor.KindHTTP,
		TargetURL: "http://127.0.0.1:9999",
		Timeout:   1 * time.Second,
	}

	// 1. First attempt succeeds
	t.Run("Scenario1_FirstAttemptSucceeds", func(t *testing.T) {
		mock := &mockChecker{
			checkFunc: func(ctx context.Context, m monitor.Monitor) (checker.CheckResult, error) {
				return checker.CheckResult{OK: true, Latency: 500 * time.Microsecond, AttemptCount: 1}, nil
			},
		}

		retrier := retry.New(mock, retry.Config{
			MaxAttempts: 3,
			BaseDelay:   20 * time.Millisecond,
		})

		iterations := 100
		durations := make([]time.Duration, iterations)
		wallStart := time.Now()

		for i := 0; i < iterations; i++ {
			start := time.Now()
			res, err := retrier.Check(context.Background(), testMon)
			durations[i] = time.Since(start)

			if err != nil || !res.OK {
				t.Fatalf("expected success, got err=%v, res=%+v", err, res)
			}
			if res.AttemptCount != 1 {
				t.Fatalf("expected 1 attempt, got %d", res.AttemptCount)
			}
		}

		stats := CalculateStats(durations, time.Since(wallStart))
		fmt.Printf("[RETRY] Scenario 1 (First attempt succeeds, N=%d):\n"+
			"  Attempts: 1\n"+
			"  Total Execution Time: Avg=%v, P50=%v, P95=%v\n"+
			"  Backoff Contribution: 0s\n"+
			"  Retry Overhead: ~%v\n",
			stats.Count, stats.Avg, stats.P50, stats.P95, stats.Avg-(500*time.Microsecond))
	})

	// 2. First attempt fails, second succeeds
	t.Run("Scenario2_FirstFails_SecondSucceeds", func(t *testing.T) {
		var callCount int64
		mock := &mockChecker{
			checkFunc: func(ctx context.Context, m monitor.Monitor) (checker.CheckResult, error) {
				n := atomic.AddInt64(&callCount, 1)
				if n%2 == 1 {
					// Odd call: fail
					return checker.CheckResult{OK: false, ErrorClass: checker.ErrorClassTimeout, Latency: 1 * time.Millisecond, AttemptCount: 1}, nil
				}
				// Even call: succeed
				return checker.CheckResult{OK: true, Latency: 1 * time.Millisecond, AttemptCount: 1}, nil
			},
		}

		baseDelay := 10 * time.Millisecond
		retrier := retry.New(mock, retry.Config{
			MaxAttempts: 3,
			BaseDelay:   baseDelay,
		})

		iterations := 50
		durations := make([]time.Duration, iterations)
		wallStart := time.Now()

		for i := 0; i < iterations; i++ {
			start := time.Now()
			res, err := retrier.Check(context.Background(), testMon)
			durations[i] = time.Since(start)

			if err != nil || !res.OK {
				t.Fatalf("expected success on second attempt, got err=%v, res=%+v", err, res)
			}
			if res.AttemptCount != 2 {
				t.Fatalf("expected 2 attempts, got %d", res.AttemptCount)
			}
		}

		stats := CalculateStats(durations, time.Since(wallStart))
		fmt.Printf("[RETRY] Scenario 2 (First fails, second succeeds, N=%d):\n"+
			"  Attempts: 2\n"+
			"  Total Execution Time: Avg=%v, P50=%v, P95=%v\n"+
			"  Base Delay Configured: %v\n"+
			"  Estimated Backoff + Jitter Contribution: ~%v\n",
			stats.Count, stats.Avg, stats.P50, stats.P95, baseDelay, stats.Avg-(2*time.Millisecond))
	})

	// 3. All attempts fail (3 attempts)
	t.Run("Scenario3_AllAttemptsFail", func(t *testing.T) {
		mock := &mockChecker{
			checkFunc: func(ctx context.Context, m monitor.Monitor) (checker.CheckResult, error) {
				return checker.CheckResult{OK: false, ErrorClass: checker.ErrorClassConnRefused, Latency: 500 * time.Microsecond, AttemptCount: 1}, nil
			},
		}

		baseDelay := 10 * time.Millisecond
		retrier := retry.New(mock, retry.Config{
			MaxAttempts: 3,
			BaseDelay:   baseDelay,
		})

		iterations := 30
		durations := make([]time.Duration, iterations)
		wallStart := time.Now()

		for i := 0; i < iterations; i++ {
			start := time.Now()
			res, err := retrier.Check(context.Background(), testMon)
			durations[i] = time.Since(start)

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if res.OK {
				t.Fatalf("expected failure after 3 attempts")
			}
			if res.AttemptCount != 3 {
				t.Fatalf("expected 3 attempts, got %d", res.AttemptCount)
			}
		}

		stats := CalculateStats(durations, time.Since(wallStart))
		fmt.Printf("[RETRY] Scenario 3 (All 3 attempts fail, N=%d):\n"+
			"  Attempts: 3\n"+
			"  Total Execution Time: Avg=%v, P50=%v, P95=%v\n"+
			"  Cumulative Backoff (2 retry delays + jitter): ~%v\n",
			stats.Count, stats.Avg, stats.P50, stats.P95, stats.Avg-(1500*time.Microsecond))
	})

	// 4. Cancellation during backoff
	t.Run("Scenario4_CancellationDuringBackoff", func(t *testing.T) {
		mock := &mockChecker{
			checkFunc: func(ctx context.Context, m monitor.Monitor) (checker.CheckResult, error) {
				// First check fails
				return checker.CheckResult{OK: false, ErrorClass: checker.ErrorClassTimeout, AttemptCount: 1}, nil
			},
		}

		// Configure long delay so cancellation occurs while sleeping
		retrier := retry.New(mock, retry.Config{
			MaxAttempts: 3,
			BaseDelay:   2 * time.Second,
		})

		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(30 * time.Millisecond)
			cancel()
		}()

		start := time.Now()
		res, err := retrier.Check(ctx, testMon)
		elapsed := time.Since(start)

		if err == nil && res.OK {
			t.Fatalf("expected error or failed result on cancellation")
		}
		if elapsed > 500*time.Millisecond {
			t.Fatalf("cancellation during backoff did not abort promptly: took %v", elapsed)
		}

		fmt.Printf("[RETRY] Scenario 4 (Cancellation during backoff):\n"+
			"  Elapsed Before Abort: %v\n"+
			"  Context Canceled Handled Promptly: true\n"+
			"  Attempts Prior to Cancel: %d\n",
			elapsed, res.AttemptCount)
	})

	// 5. Non-retryable / Domain Error
	t.Run("Scenario5_NonRetryableDomainError", func(t *testing.T) {
		domainErr := errors.New("domain validation error: invalid target URL")
		mock := &mockChecker{
			checkFunc: func(ctx context.Context, m monitor.Monitor) (checker.CheckResult, error) {
				return checker.CheckResult{}, domainErr
			},
		}

		retrier := retry.New(mock, retry.Config{
			MaxAttempts: 3,
			BaseDelay:   50 * time.Millisecond,
		})

		start := time.Now()
		res, err := retrier.Check(context.Background(), testMon)
		elapsed := time.Since(start)

		if !errors.Is(err, domainErr) {
			t.Fatalf("expected domainErr %v, got %v", domainErr, err)
		}
		if res.AttemptCount > 1 {
			t.Fatalf("domain error should not be retried, attempts=%d", res.AttemptCount)
		}

		fmt.Printf("[RETRY] Scenario 5 (Non-retryable/domain error):\n"+
			"  Execution Time: %v\n"+
			"  Attempts: 1\n"+
			"  Backoff Contribution: 0s (no retries performed)\n"+
			"  Error: %v\n",
			elapsed, err)
	})
}

func BenchmarkRetry_FirstAttemptSuccess(b *testing.B) {
	mock := &mockChecker{
		checkFunc: func(ctx context.Context, m monitor.Monitor) (checker.CheckResult, error) {
			return checker.CheckResult{OK: true, AttemptCount: 1}, nil
		},
	}
	retrier := retry.New(mock, retry.Config{MaxAttempts: 3, BaseDelay: 10 * time.Millisecond})
	m := monitor.Monitor{ID: "m-bench", Kind: monitor.KindHTTP, TargetURL: "http://127.0.0.1"}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = retrier.Check(context.Background(), m)
	}
}
