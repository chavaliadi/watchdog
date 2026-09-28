package benchmark_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/chavaliadi/watchdog/internal/api"
	"github.com/chavaliadi/watchdog/internal/checker"
	"github.com/chavaliadi/watchdog/internal/monitor"
	"github.com/chavaliadi/watchdog/internal/persistence/postgres"
	"github.com/chavaliadi/watchdog/internal/retry"
	"github.com/chavaliadi/watchdog/internal/scheduler"
	"github.com/chavaliadi/watchdog/internal/service"
	"github.com/chavaliadi/watchdog/internal/telemetry"
	"github.com/chavaliadi/watchdog/internal/worker"
)

func TestResourceBehavior_AndPrometheusMetrics(t *testing.T) {
	db, _, cleanup, err := StartEphemeralPostgres()
	if err != nil {
		t.Fatalf("start ephemeral postgres: %v", err)
	}
	defer cleanup()

	// Local target
	targetSrv := StartLocalHTTPServer(2*time.Millisecond, http.StatusOK, `{"status":"ok"}`)
	defer targetSrv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	metrics := telemetry.NewMetrics()
	repo := postgres.New(db, postgres.WithRecorder(metrics))

	httpChecker := checker.NewHTTPChecker(nil)
	orch := scheduler.NewOrchestrator(httpChecker, nil, retry.Config{
		MaxAttempts: 2,
		BaseDelay:   10 * time.Millisecond,
		Recorder:    metrics,
	}, repo).WithRecorder(metrics)

	poolConcurrency := 10
	pool, err := worker.NewPool(worker.Config{
		MaxConcurrency: poolConcurrency,
	}, orch)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	pool.Start(ctx)
	defer func() {
		pool.Stop()
		pool.Wait()
	}()

	metrics.RegisterWorkerPool(pool)

	multiSched, err := scheduler.NewMultiScheduler(repo, pool)
	if err != nil {
		t.Fatalf("create multisched: %v", err)
	}
	multiSched.WithRecorder(metrics)

	// Seed 50 active monitors
	numMonitors := 50
	for i := 0; i < numMonitors; i++ {
		m := monitor.Monitor{
			ID:                  fmt.Sprintf("40000000-0000-0000-0000-%012d", i+1),
			Name:                fmt.Sprintf("Resource Mon %d", i+1),
			Kind:                monitor.KindHTTP,
			TargetURL:           targetSrv.URL,
			Method:              "GET",
			ExpectedStatusRange: "200-299",
			Interval:            100 * time.Millisecond, // 10 ticks per second per monitor
			Timeout:             1 * time.Second,
			Enabled:             true,
		}
		if err := repo.CreateMonitor(ctx, m); err != nil {
			t.Fatalf("seed monitor: %v", err)
		}
	}

	monitorSvc := service.NewMonitorService(repo, multiSched)
	handlers := api.NewHandlers(
		monitorSvc,
		api.WithHealthChecks(db, multiSched, nil),
		api.WithMetrics(metrics.Handler()),
		api.WithRecorder(metrics),
	)
	apiServer := api.NewServer(api.Config{}, handlers)
	httpServer := httptest.NewServer(apiServer.Handler())
	defer httpServer.Close()

	// Baseline snapshot
	runtime.GC()
	var memBaseline runtime.MemStats
	runtime.ReadMemStats(&memBaseline)
	goroutinesBaseline := runtime.NumGoroutine()

	// Start scheduling
	if err := multiSched.Start(ctx); err != nil {
		t.Fatalf("start multisched: %v", err)
	}

	// Sample over 3 intervals (t=1s, t=2s, t=3s)
	type Sample struct {
		Timestamp   time.Duration
		Goroutines  int
		HeapAllocMB float64
		HeapSysMB   float64
		NumGC       uint32
		ActiveWork  int
		QueueDepth  int
		DBOpenConns int
		DBInUse     int
	}

	samples := make([]Sample, 3)
	for s := 0; s < 3; s++ {
		time.Sleep(1000 * time.Millisecond)
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		dbSt := db.Stats()

		samples[s] = Sample{
			Timestamp:   time.Duration(s+1) * time.Second,
			Goroutines:  runtime.NumGoroutine(),
			HeapAllocMB: float64(ms.HeapAlloc) / (1024 * 1024),
			HeapSysMB:   float64(ms.HeapSys) / (1024 * 1024),
			NumGC:       ms.NumGC,
			ActiveWork:  pool.ActiveCount(),
			QueueDepth:  pool.PendingCount(),
			DBOpenConns: dbSt.OpenConnections,
			DBInUse:     dbSt.InUse,
		}
	}

	// Make a few API calls to exercise HTTP metrics
	_, _ = http.Get(httpServer.URL + "/monitors")
	_, _ = http.Get(fmt.Sprintf("%s/monitors/40000000-0000-0000-0000-000000000001", httpServer.URL))

	// Scrape Prometheus metrics
	resp, err := http.Get(httpServer.URL + "/metrics")
	if err != nil {
		t.Fatalf("scrape /metrics failed: %v", err)
	}
	defer resp.Body.Close()

	promMetrics := parsePrometheusMetrics(resp.Body)

	// Shutdown
	cancel()
	multiSched.Stop()
	multiSched.Wait()
	pool.Stop()
	pool.Wait()

	// Post-shutdown snapshot
	runtime.GC()
	goroutinesAfter := runtime.NumGoroutine()

	fmt.Println("\n======================= RESOURCE STABILITY SAMPLES (3s RUN) =======================")
	fmt.Printf("%-10s | %-12s | %-12s | %-12s | %-8s | %-10s | %-10s | %-10s\n",
		"Elapsed", "Goroutines", "Heap Alloc", "Heap Sys", "NumGC", "ActiveWork", "QueueDepth", "DB Conns")
	fmt.Println("---------------------------------------------------------------------------------------------")
	for _, sm := range samples {
		fmt.Printf("%-10v | %-12d | %-10.2fMB | %-10.2fMB | %-8d | %-10d | %-10d | %-10d\n",
			sm.Timestamp, sm.Goroutines, sm.HeapAllocMB, sm.HeapSysMB, sm.NumGC, sm.ActiveWork, sm.QueueDepth, sm.DBOpenConns)
	}
	fmt.Println("=============================================================================================")
	fmt.Printf("Baseline Goroutines: %d -> Active Load: ~%d -> Post-Shutdown: %d (Delta: %d)\n",
		goroutinesBaseline, samples[2].Goroutines, goroutinesAfter, goroutinesAfter-goroutinesBaseline)

	fmt.Println("\n======================= PROMETHEUS METRICS SCRAPED (/metrics) =======================")
	requiredMetrics := []string{
		"watchdog_monitors_active",
		"watchdog_worker_pool_active_workers",
		"watchdog_worker_pool_queued_jobs",
		"watchdog_worker_pool_capacity",
		"watchdog_checks_total",
		"watchdog_check_duration_seconds",
		"watchdog_check_retries_total",
		"watchdog_scheduler_cycle_errors_total",
		"watchdog_http_requests_total",
		"watchdog_http_request_duration_seconds",
		"watchdog_db_operations_total",
		"watchdog_db_operation_duration_seconds",
	}

	for _, name := range requiredMetrics {
		lines, found := promMetrics[name]
		if !found || len(lines) == 0 {
			// Check if histogram suffix matched
			var matched []string
			for k, v := range promMetrics {
				if strings.HasPrefix(k, name) {
					matched = append(matched, v...)
				}
			}
			if len(matched) > 0 {
				for _, l := range matched[:min(2, len(matched))] {
					fmt.Printf("%-40s | %s\n", name, l)
				}
			} else {
				fmt.Printf("%-40s | ZERO / NO EVENTS RECORDED\n", name)
			}
		} else {
			for _, line := range lines {
				fmt.Printf("%-40s | %s\n", name, line)
			}
		}
	}
	fmt.Println("=======================================================================================")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func parsePrometheusMetrics(r io.Reader) map[string][]string {
	result := make(map[string][]string)
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// metric line: e.g. watchdog_monitors_active{kind="http"} 50
		parts := strings.SplitN(line, "{", 2)
		if len(parts) == 2 {
			name := parts[0]
			result[name] = append(result[name], line)
		} else {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				name := fields[0]
				result[name] = append(result[name], line)
			}
		}
	}
	return result
}
