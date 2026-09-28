package benchmark_test

import (
	"context"
	"fmt"
	"net/http"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chavaliadi/watchdog/internal/checker"
	"github.com/chavaliadi/watchdog/internal/monitor"
	"github.com/chavaliadi/watchdog/internal/persistence/postgres"
	"github.com/chavaliadi/watchdog/internal/retry"
	"github.com/chavaliadi/watchdog/internal/scheduler"
	"github.com/chavaliadi/watchdog/internal/state"
	"github.com/chavaliadi/watchdog/internal/telemetry"
	"github.com/chavaliadi/watchdog/internal/worker"
)

func TestScheduler_Scaling(t *testing.T) {
	db, _, cleanup, err := StartEphemeralPostgres()
	if err != nil {
		t.Fatalf("start ephemeral postgres: %v", err)
	}
	defer cleanup()

	// Local fast HTTP target
	targetSrv := StartLocalHTTPServer(0, http.StatusOK, `{"status":"healthy"}`)
	defer targetSrv.Close()

	monitorScales := []int{10, 50, 100, 250, 500}
	poolConcurrency := 25
	runDuration := 1500 * time.Millisecond // 1.5s per scale test

	fmt.Println("\n========================== MULTI-SCHEDULER SCALING BENCHMARK ==========================")
	fmt.Printf("%-10s | %-12s | %-12s | %-10s | %-10s | %-10s | %-12s | %-12s\n",
		"Monitors", "Cycles Run", "Throughput", "Goroutines", "Heap Alloc", "Pool Act", "Pool Queue", "Avg Cycle Lat")
	fmt.Println("---------------------------------------------------------------------------------------------------------")

	for _, count := range monitorScales {
		ctx, cancel := context.WithCancel(context.Background())

		// Clean DB tables
		_, _ = db.Exec("TRUNCATE monitors CASCADE")

		repo := postgres.New(db)

		// Seed monitors with 50ms interval to generate continuous scheduling ticks
		for i := 0; i < count; i++ {
			m := monitor.Monitor{
				ID:                  fmt.Sprintf("00000000-0000-0000-0000-%012d", i+1),
				Name:                fmt.Sprintf("Monitor Scale %d", i+1),
				Kind:                monitor.KindHTTP,
				TargetURL:           targetSrv.URL,
				Method:              "GET",
				ExpectedStatusRange: "200-299",
				Interval:            50 * time.Millisecond,
				Timeout:             1 * time.Second,
				Enabled:             true,
			}
			if err := repo.CreateMonitor(ctx, m); err != nil {
				t.Fatalf("create monitor: %v", err)
			}
		}

		metrics := telemetry.NewMetrics()
		httpChecker := checker.NewHTTPChecker(nil)
		orch := scheduler.NewOrchestrator(httpChecker, nil, retry.Config{
			MaxAttempts: 1, // 1 attempt for clean cycle baseline
		}, repo).WithRecorder(metrics)

		var cyclesExecuted int64
		var totalCycleDuration int64
		trackedRunner := &mockTrackingCycleRunner{
			inner: orch,
			onComplete: func(d time.Duration) {
				atomic.AddInt64(&cyclesExecuted, 1)
				atomic.AddInt64(&totalCycleDuration, int64(d))
			},
		}

		pool, err := worker.NewPool(worker.Config{
			MaxConcurrency: poolConcurrency,
		}, trackedRunner)
		if err != nil {
			t.Fatalf("create pool: %v", err)
		}
		pool.Start(ctx)

		multiSched, err := scheduler.NewMultiScheduler(repo, pool)
		if err != nil {
			t.Fatalf("create multischeduler: %v", err)
		}
		multiSched.WithRecorder(metrics)

		runtime.GC()
		var memBefore runtime.MemStats
		runtime.ReadMemStats(&memBefore)
		goroutinesBefore := runtime.NumGoroutine()

		wallStart := time.Now()
		if err := multiSched.Start(ctx); err != nil {
			t.Fatalf("start multischeduler: %v", err)
		}

		time.Sleep(runDuration)

		peakActive := pool.ActiveCount()
		peakQueue := pool.PendingCount()
		goroutinesDuring := runtime.NumGoroutine()
		var memDuring runtime.MemStats
		runtime.ReadMemStats(&memDuring)

		cancel()
		multiSched.Stop()
		multiSched.Wait()
		pool.Stop()
		pool.Wait()

		wallElapsed := time.Since(wallStart)
		cycles := atomic.LoadInt64(&cyclesExecuted)
		throughput := float64(cycles) / wallElapsed.Seconds()
		avgLatency := time.Duration(0)
		if cycles > 0 {
			avgLatency = time.Duration(atomic.LoadInt64(&totalCycleDuration) / cycles)
		}

		heapMB := float64(memDuring.HeapAlloc) / (1024 * 1024)

		fmt.Printf("%-10d | %-12d | %-10.2f/s | %-10d | %-8.2fMB | %-10d | %-12d | %-12v\n",
			count, cycles, throughput, goroutinesDuring-goroutinesBefore, heapMB, peakActive, peakQueue, avgLatency)
	}
	fmt.Println("=========================================================================================================")
}

type mockTrackingCycleRunner struct {
	inner      scheduler.CycleRunner
	onComplete func(d time.Duration)
}

func (m *mockTrackingCycleRunner) RunCycle(ctx context.Context, mon monitor.Monitor, current state.State) (scheduler.CycleResult, error) {
	start := time.Now()
	res, err := m.inner.RunCycle(ctx, mon, current)
	if m.onComplete != nil {
		m.onComplete(time.Since(start))
	}
	return res, err
}
