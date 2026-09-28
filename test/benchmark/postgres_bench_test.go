package benchmark_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/chavaliadi/watchdog/internal/checker"
	"github.com/chavaliadi/watchdog/internal/monitor"
	"github.com/chavaliadi/watchdog/internal/persistence/postgres"
	"github.com/chavaliadi/watchdog/internal/state"
)

func TestPostgres_Performance(t *testing.T) {
	db, connStr, cleanup, err := StartEphemeralPostgres()
	if err != nil {
		t.Fatalf("failed to start ephemeral postgres: %v", err)
	}
	defer cleanup()

	fmt.Printf("[POSTGRES] Connected to ephemeral PostgreSQL at %s\n", connStr)

	repo := postgres.New(db)
	ctx := context.Background()

	// Seed test monitors
	numMonitors := 100
	testMonitors := make([]monitor.Monitor, numMonitors)
	for i := 0; i < numMonitors; i++ {
		m := monitor.Monitor{
			ID:                  fmt.Sprintf("00000000-0000-0000-0000-%012d", i+1),
			Name:                fmt.Sprintf("Seed Monitor %d", i+1),
			Kind:                monitor.KindHTTP,
			TargetURL:           "http://127.0.0.1:8080/health",
			Method:              "GET",
			ExpectedStatusRange: "200-299",
			Interval:            60 * time.Second,
			Timeout:             5 * time.Second,
			Enabled:             true,
		}
		if err := repo.CreateMonitor(ctx, m); err != nil {
			t.Fatalf("failed to seed monitor: %v", err)
		}
		testMonitors[i] = m
	}
	fmt.Printf("[POSTGRES] Seeded %d monitors with initial states\n", numMonitors)

	// 1. Sequential SaveCycle baseline (500 cycles)
	t.Run("Sequential_SaveCycle", func(t *testing.T) {
		iterations := 500
		durations := make([]time.Duration, iterations)
		wallStart := time.Now()

		for i := 0; i < iterations; i++ {
			m := testMonitors[i%numMonitors]
			res := checker.CheckResult{
				MonitorID:    m.ID,
				CheckedAt:    time.Now().UTC(),
				OK:           true,
				StatusCode:   200,
				Latency:      12 * time.Millisecond,
				AttemptCount: 1,
			}

			start := time.Now()
			err := repo.SaveCycle(ctx, m.ID, res, state.StateHealthy)
			durations[i] = time.Since(start)

			if err != nil {
				t.Fatalf("save cycle error: %v", err)
			}
		}

		stats := CalculateStats(durations, time.Since(wallStart))
		dbStats := db.Stats()
		fmt.Printf("[POSTGRES] Sequential SaveCycle (N=%d):\n"+
			"  Throughput: %.2f ops/s\n"+
			"  Latency: Min=%v, Avg=%v, P50=%v, P95=%v, P99=%v, Max=%v\n"+
			"  DB Pool Stats: Open=%d, InUse=%d, Idle=%d, WaitCount=%d, WaitDuration=%v\n",
			stats.Count, stats.Throughput, stats.Min, stats.Avg, stats.P50, stats.P95, stats.P99, stats.Max,
			dbStats.OpenConnections, dbStats.InUse, dbStats.Idle, dbStats.WaitCount, dbStats.WaitDuration)
	})

	// 2. Concurrent SaveCycle scaling across goroutines (5, 10, 25, 50 workers)
	concurrencyLevels := []int{5, 10, 25, 50}
	totalOps := 500

	fmt.Println("\n=================== CONCURRENT POSTGRES SAVECYCLE SWEEP ===================")
	fmt.Printf("%-12s | %-12s | %-12s | %-12s | %-12s | %-10s | %-12s\n",
		"Concurrency", "Throughput", "Avg Latency", "P50 Latency", "P95 Latency", "Open Conns", "Wait Duration")
	fmt.Println("---------------------------------------------------------------------------------------------")

	for _, conc := range concurrencyLevels {
		durations := make([]time.Duration, totalOps)
		var wg sync.WaitGroup
		opsPerGoroutine := totalOps / conc

		wallStart := time.Now()
		for c := 0; c < conc; c++ {
			wg.Add(1)
			go func(workerIdx int) {
				defer wg.Done()
				startIdx := workerIdx * opsPerGoroutine
				endIdx := startIdx + opsPerGoroutine
				if workerIdx == conc-1 {
					endIdx = totalOps
				}

				for i := startIdx; i < endIdx; i++ {
					m := testMonitors[i%numMonitors]
					res := checker.CheckResult{
						MonitorID:    m.ID,
						CheckedAt:    time.Now().UTC(),
						OK:           true,
						StatusCode:   200,
						Latency:      10 * time.Millisecond,
						AttemptCount: 1,
					}

					opStart := time.Now()
					err := repo.SaveCycle(ctx, m.ID, res, state.StateHealthy)
					durations[i] = time.Since(opStart)
					if err != nil {
						fmt.Printf("concurrent SaveCycle err: %v\n", err)
						return
					}
				}
			}(c)
		}
		wg.Wait()
		wallElapsed := time.Since(wallStart)

		stats := CalculateStats(durations, wallElapsed)
		dbStats := db.Stats()
		fmt.Printf("%-12d | %-10.2f/s | %-12v | %-12v | %-12v | %-10d | %-12v\n",
			conc, stats.Throughput, stats.Avg, stats.P50, stats.P95, dbStats.OpenConnections, dbStats.WaitDuration)
	}
	fmt.Println("=============================================================================================")

	// 3. GetState & GetStateWithTimestamp latency
	t.Run("GetState_Latency", func(t *testing.T) {
		iterations := 500
		durations := make([]time.Duration, iterations)
		wallStart := time.Now()

		for i := 0; i < iterations; i++ {
			m := testMonitors[i%numMonitors]
			start := time.Now()
			st, err := repo.GetState(ctx, m.ID)
			durations[i] = time.Since(start)

			if err != nil || st != state.StateHealthy {
				t.Fatalf("unexpected GetState err=%v, st=%s", err, st)
			}
		}

		stats := CalculateStats(durations, time.Since(wallStart))
		fmt.Printf("[POSTGRES] GetState (N=%d): Avg=%v, P50=%v, P95=%v, P99=%v, Throughput=%.2f ops/s\n",
			stats.Count, stats.Avg, stats.P50, stats.P95, stats.P99, stats.Throughput)
	})

	// 4. ListCheckResults latency
	t.Run("ListCheckResults_Latency", func(t *testing.T) {
		m := testMonitors[0]
		iterations := 100
		durations := make([]time.Duration, iterations)
		wallStart := time.Now()

		for i := 0; i < iterations; i++ {
			start := time.Now()
			checks, err := repo.ListCheckResults(ctx, m.ID, 10)
			durations[i] = time.Since(start)

			if err != nil {
				t.Fatalf("ListCheckResults err: %v", err)
			}
			if len(checks) == 0 {
				t.Fatalf("expected non-empty checks")
			}
		}

		stats := CalculateStats(durations, time.Since(wallStart))
		fmt.Printf("[POSTGRES] ListCheckResults limit 10 (N=%d): Avg=%v, P50=%v, P95=%v, P99=%v, Throughput=%.2f ops/s\n",
			stats.Count, stats.Avg, stats.P50, stats.P95, stats.P99, stats.Throughput)
	})
}
