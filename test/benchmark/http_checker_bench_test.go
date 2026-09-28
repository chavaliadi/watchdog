package benchmark_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/chavaliadi/watchdog/internal/checker"
	"github.com/chavaliadi/watchdog/internal/monitor"
)

func TestHTTPChecker_Performance(t *testing.T) {
	// A. Fast Response Server (200 OK)
	fastSrv := StartLocalHTTPServer(0, http.StatusOK, `{"status":"ok"}`)
	defer fastSrv.Close()

	// B. Failed Status Server (500 Internal Server Error)
	errSrv := StartLocalHTTPServer(0, http.StatusInternalServerError, `{"error":"internal"}`)
	defer errSrv.Close()

	// C. Delayed Server (15ms delay)
	delayedSrv := StartLocalHTTPServer(15*time.Millisecond, http.StatusOK, `{"status":"ok"}`)
	defer delayedSrv.Close()

	// D. Slow Server for Timeout testing (200ms delay)
	slowSrv := StartLocalHTTPServer(200*time.Millisecond, http.StatusOK, `{"status":"slow"}`)
	defer slowSrv.Close()

	chk := checker.NewHTTPChecker(nil)
	ctx := context.Background()

	// 1. Fast response measurement (1000 iterations)
	t.Run("FastResponse_200OK", func(t *testing.T) {
		fastMon := monitor.Monitor{
			ID:                   "mon-http-fast",
			Name:                 "Fast HTTP Monitor",
			Kind:                 monitor.KindHTTP,
			TargetURL:            fastSrv.URL,
			Method:               "GET",
			ExpectedStatusRange:  "200-299",
			Timeout:              2 * time.Second,
		}

		iterations := 1000
		durations := make([]time.Duration, iterations)
		wallStart := time.Now()

		for i := 0; i < iterations; i++ {
			start := time.Now()
			res, err := chk.Check(ctx, fastMon)
			durations[i] = time.Since(start)

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !res.OK || res.StatusCode != 200 {
				t.Fatalf("unexpected result: ok=%v, status=%d", res.OK, res.StatusCode)
			}
		}
		stats := CalculateStats(durations, time.Since(wallStart))
		fmt.Printf("[HTTP CHECKER] Fast 200 OK (N=%d): Avg=%v, P50=%v, P95=%v, P99=%v, Min=%v, Max=%v, Throughput=%.2f ops/s\n",
			stats.Count, stats.Avg, stats.P50, stats.P95, stats.P99, stats.Min, stats.Max, stats.Throughput)
	})

	// 2. Failed-status measurement (1000 iterations)
	t.Run("FailedStatus_500Error", func(t *testing.T) {
		errMon := monitor.Monitor{
			ID:                   "mon-http-err",
			Name:                 "Err HTTP Monitor",
			Kind:                 monitor.KindHTTP,
			TargetURL:            errSrv.URL,
			Method:               "GET",
			ExpectedStatusRange:  "200-299",
			Timeout:              2 * time.Second,
		}

		iterations := 1000
		durations := make([]time.Duration, iterations)
		wallStart := time.Now()

		for i := 0; i < iterations; i++ {
			start := time.Now()
			res, err := chk.Check(ctx, errMon)
			durations[i] = time.Since(start)

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if res.OK || res.StatusCode != 500 {
				t.Fatalf("expected non-OK with 500, got: ok=%v, status=%d", res.OK, res.StatusCode)
			}
		}
		stats := CalculateStats(durations, time.Since(wallStart))
		fmt.Printf("[HTTP CHECKER] Failed Status 500 (N=%d): Avg=%v, P50=%v, P95=%v, P99=%v, Min=%v, Max=%v, Throughput=%.2f ops/s\n",
			stats.Count, stats.Avg, stats.P50, stats.P95, stats.P99, stats.Min, stats.Max, stats.Throughput)
	})

	// 3. Deliberately delayed response (100 iterations)
	t.Run("DelayedResponse_15ms", func(t *testing.T) {
		delayMon := monitor.Monitor{
			ID:                   "mon-http-delay",
			Name:                 "Delay HTTP Monitor",
			Kind:                 monitor.KindHTTP,
			TargetURL:            delayedSrv.URL,
			Method:               "GET",
			ExpectedStatusRange:  "200-299",
			Timeout:              500 * time.Millisecond,
		}

		iterations := 100
		durations := make([]time.Duration, iterations)
		wallStart := time.Now()

		for i := 0; i < iterations; i++ {
			start := time.Now()
			res, err := chk.Check(ctx, delayMon)
			durations[i] = time.Since(start)

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !res.OK || res.StatusCode != 200 {
				t.Fatalf("unexpected result: ok=%v, status=%d", res.OK, res.StatusCode)
			}
		}
		stats := CalculateStats(durations, time.Since(wallStart))
		fmt.Printf("[HTTP CHECKER] Delayed 15ms (N=%d): Avg=%v, P50=%v, P95=%v, P99=%v, Min=%v, Max=%v, Throughput=%.2f ops/s\n",
			stats.Count, stats.Avg, stats.P50, stats.P95, stats.P99, stats.Min, stats.Max, stats.Throughput)
	})

	// 4. Timeout behavior and Monitor-level timeout enforcement verification
	t.Run("TimeoutEnforcement", func(t *testing.T) {
		timeoutMon := monitor.Monitor{
			ID:                   "mon-http-timeout",
			Name:                 "Timeout HTTP Monitor",
			Kind:                 monitor.KindHTTP,
			TargetURL:            slowSrv.URL, // takes 200ms
			Method:               "GET",
			ExpectedStatusRange:  "200-299",
			Timeout:              40 * time.Millisecond, // strictly enforced at 40ms
		}

		iterations := 20
		durations := make([]time.Duration, iterations)
		wallStart := time.Now()

		for i := 0; i < iterations; i++ {
			start := time.Now()
			res, err := chk.Check(ctx, timeoutMon)
			elapsed := time.Since(start)
			durations[i] = elapsed

			if err != nil {
				t.Fatalf("HTTPChecker should return CheckResult on network/timeout error, got err=%v", err)
			}
			if res.OK {
				t.Fatalf("expected failure due to timeout, got OK")
			}
			if res.ErrorClass != checker.ErrorClassTimeout {
				t.Fatalf("expected ErrorClass %s, got %s (detail: %s)", checker.ErrorClassTimeout, res.ErrorClass, res.ErrorDetail)
			}
			// Verify latency reflects timeout (~40ms, definitely not 200ms)
			if elapsed > 150*time.Millisecond {
				t.Fatalf("timeout enforcement failed: took %v, expected ~40ms", elapsed)
			}
		}
		stats := CalculateStats(durations, time.Since(wallStart))
		fmt.Printf("[HTTP CHECKER] Timeout Enforcement 40ms against 200ms server (N=%d): Avg=%v, P50=%v, P95=%v, P99=%v, Min=%v, Max=%v, ErrorClass=%s\n",
			stats.Count, stats.Avg, stats.P50, stats.P95, stats.P99, stats.Min, stats.Max, checker.ErrorClassTimeout)
	})
}

func BenchmarkHTTPChecker_Fast(b *testing.B) {
	fastSrv := StartLocalHTTPServer(0, http.StatusOK, `{"status":"ok"}`)
	defer fastSrv.Close()

	chk := checker.NewHTTPChecker(nil)
	ctx := context.Background()
	m := monitor.Monitor{
		ID:                  "mon-http-bench",
		Kind:                monitor.KindHTTP,
		TargetURL:           fastSrv.URL,
		Method:              "GET",
		ExpectedStatusRange: "200-299",
		Timeout:             2 * time.Second,
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = chk.Check(ctx, m)
	}
}

func BenchmarkHTTPChecker_FailedStatus(b *testing.B) {
	errSrv := StartLocalHTTPServer(0, http.StatusInternalServerError, `{"error":"err"}`)
	defer errSrv.Close()

	chk := checker.NewHTTPChecker(nil)
	ctx := context.Background()
	m := monitor.Monitor{
		ID:                  "mon-http-bench-err",
		Kind:                monitor.KindHTTP,
		TargetURL:           errSrv.URL,
		Method:              "GET",
		ExpectedStatusRange: "200-299",
		Timeout:             2 * time.Second,
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = chk.Check(ctx, m)
	}
}
