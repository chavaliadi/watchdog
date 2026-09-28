package benchmark_test

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/chavaliadi/watchdog/internal/checker"
	"github.com/chavaliadi/watchdog/internal/monitor"
)

func TestTCPChecker_Performance(t *testing.T) {
	// 1. Success Listener
	_, successAddr, successClean := StartLocalTCPListener()
	defer successClean()

	// 2. Refused Address (find an unused port and close it)
	freePort, err := getFreePort()
	if err != nil {
		t.Fatalf("failed to find free port: %v", err)
	}
	refusedAddr := fmt.Sprintf("127.0.0.1:%d", freePort)

	// 3. Hanging / Timeout Listener
	// A listener that accepts but if we simulate timeout using a non-routable/blackhole address
	// or hanging dial, wait: in Go TCP dial on 127.0.0.1: dial connects immediately once accepted!
	// To test dial timeout on TCP, dialing an unroutable IP (like 10.255.255.1:80) or custom dialer with delay:
	// But let's check monitor-level timeout enforcement in TCPChecker:
	// If dialer takes 200ms and monitor timeout is 40ms, checkCtx context timeout fires!

	chk := checker.NewTCPChecker(nil)
	ctx := context.Background()

	// 1. Successful connection latency & throughput (1000 iterations)
	t.Run("SuccessConnection", func(t *testing.T) {
		m := monitor.Monitor{
			ID:        "mon-tcp-success",
			Kind:      monitor.KindTCP,
			TargetURL: successAddr,
			Timeout:   2 * time.Second,
		}

		iterations := 1000
		durations := make([]time.Duration, iterations)
		wallStart := time.Now()

		for i := 0; i < iterations; i++ {
			start := time.Now()
			res, err := chk.Check(ctx, m)
			durations[i] = time.Since(start)

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !res.OK {
				t.Fatalf("expected OK result, got failed: %s", res.ErrorDetail)
			}
		}
		stats := CalculateStats(durations, time.Since(wallStart))
		fmt.Printf("[TCP CHECKER] Success Connection (N=%d): Avg=%v, P50=%v, P95=%v, P99=%v, Min=%v, Max=%v, Throughput=%.2f ops/s\n",
			stats.Count, stats.Avg, stats.P50, stats.P95, stats.P99, stats.Min, stats.Max, stats.Throughput)
	})

	// 2. Refused connection behavior (1000 iterations)
	t.Run("RefusedConnection", func(t *testing.T) {
		m := monitor.Monitor{
			ID:        "mon-tcp-refused",
			Kind:      monitor.KindTCP,
			TargetURL: refusedAddr,
			Timeout:   2 * time.Second,
		}

		iterations := 1000
		durations := make([]time.Duration, iterations)
		wallStart := time.Now()

		for i := 0; i < iterations; i++ {
			start := time.Now()
			res, err := chk.Check(ctx, m)
			durations[i] = time.Since(start)

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if res.OK {
				t.Fatalf("expected non-OK on refused connection")
			}
			if res.ErrorClass != checker.ErrorClassConnRefused {
				t.Fatalf("expected ErrorClassConnRefused, got %s", res.ErrorClass)
			}
		}
		stats := CalculateStats(durations, time.Since(wallStart))
		fmt.Printf("[TCP CHECKER] Refused Connection (N=%d): Avg=%v, P50=%v, P95=%v, P99=%v, Min=%v, Max=%v, Throughput=%.2f ops/s\n",
			stats.Count, stats.Avg, stats.P50, stats.P95, stats.P99, stats.Min, stats.Max, stats.Throughput)
	})

	// 3. Timeout behavior and Monitor-level timeout enforcement
	t.Run("TimeoutEnforcement", func(t *testing.T) {
		// Custom dialer that sleeps for 200ms to test monitor-level timeout enforcement deterministically
		slowDialer := &slowTestDialer{delay: 200 * time.Millisecond}
		slowChecker := checker.NewTCPChecker(slowDialer)

		m := monitor.Monitor{
			ID:        "mon-tcp-timeout",
			Kind:      monitor.KindTCP,
			TargetURL: "127.0.0.1:8080",
			Timeout:   40 * time.Millisecond, // strictly enforced at 40ms
		}

		iterations := 20
		durations := make([]time.Duration, iterations)
		wallStart := time.Now()

		for i := 0; i < iterations; i++ {
			start := time.Now()
			res, err := slowChecker.Check(ctx, m)
			elapsed := time.Since(start)
			durations[i] = elapsed

			if err != nil {
				t.Fatalf("expected nil error (reported in CheckResult), got: %v", err)
			}
			if res.OK {
				t.Fatalf("expected failure, got OK")
			}
			if res.ErrorClass != checker.ErrorClassTimeout {
				t.Fatalf("expected ErrorClassTimeout, got %s (detail: %s)", res.ErrorClass, res.ErrorDetail)
			}
			if elapsed > 150*time.Millisecond {
				t.Fatalf("timeout enforcement failed: took %v, expected ~40ms", elapsed)
			}
		}
		stats := CalculateStats(durations, time.Since(wallStart))
		fmt.Printf("[TCP CHECKER] Timeout Enforcement 40ms against 200ms dialer (N=%d): Avg=%v, P50=%v, P95=%v, P99=%v, Min=%v, Max=%v, ErrorClass=%s\n",
			stats.Count, stats.Avg, stats.P50, stats.P95, stats.P99, stats.Min, stats.Max, checker.ErrorClassTimeout)
	})
}

type slowTestDialer struct {
	delay time.Duration
}

func (s *slowTestDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	select {
	case <-time.After(s.delay):
		return &net.TCPConn{}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func BenchmarkTCPChecker_Success(b *testing.B) {
	_, successAddr, cleanup := StartLocalTCPListener()
	defer cleanup()

	chk := checker.NewTCPChecker(nil)
	ctx := context.Background()
	m := monitor.Monitor{
		ID:        "mon-tcp-bench",
		Kind:      monitor.KindTCP,
		TargetURL: successAddr,
		Timeout:   2 * time.Second,
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = chk.Check(ctx, m)
	}
}

func BenchmarkTCPChecker_Refused(b *testing.B) {
	freePort, err := getFreePort()
	if err != nil {
		b.Fatalf("failed to find free port: %v", err)
	}
	refusedAddr := fmt.Sprintf("127.0.0.1:%d", freePort)

	chk := checker.NewTCPChecker(nil)
	ctx := context.Background()
	m := monitor.Monitor{
		ID:        "mon-tcp-bench-refused",
		Kind:      monitor.KindTCP,
		TargetURL: refusedAddr,
		Timeout:   2 * time.Second,
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = chk.Check(ctx, m)
	}
}
