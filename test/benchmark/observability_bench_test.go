package benchmark_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/chavaliadi/watchdog/internal/checker"
	"github.com/chavaliadi/watchdog/internal/monitor"
	"github.com/chavaliadi/watchdog/internal/persistence/postgres"
	"github.com/chavaliadi/watchdog/internal/retry"
	"github.com/chavaliadi/watchdog/internal/scheduler"
	"github.com/chavaliadi/watchdog/internal/state"
	"github.com/chavaliadi/watchdog/internal/telemetry"
)

func TestObservability_Overhead(t *testing.T) {
	metrics := telemetry.NewMetrics()
	noop := telemetry.NoopRecorder{}

	iterations := 10000

	// 1. Direct Recorder Micro-Overhead
	fmt.Println("\n======================= OBSERVABILITY OVERHEAD COMPARISON =======================")
	fmt.Printf("%-30s | %-15s | %-15s | %-15s\n", "Operation", "Noop Duration", "Metrics Duration", "Overhead / Op")
	fmt.Println("---------------------------------------------------------------------------------")

	// A. RecordCheck
	start := time.Now()
	for i := 0; i < iterations; i++ {
		noop.RecordCheck("http", true, "", 10*time.Millisecond)
	}
	noopCheck := time.Since(start)

	start = time.Now()
	for i := 0; i < iterations; i++ {
		metrics.RecordCheck("http", true, "", 10*time.Millisecond)
	}
	metricsCheck := time.Since(start)
	overheadCheck := (metricsCheck - noopCheck) / time.Duration(iterations)
	fmt.Printf("%-30s | %-15v | %-15v | %-15v\n", "RecordCheck (N=10k)", noopCheck, metricsCheck, overheadCheck)

	// B. RecordHTTPRequest
	start = time.Now()
	for i := 0; i < iterations; i++ {
		noop.RecordHTTPRequest("GET", "/monitors", 200, 500*time.Microsecond)
	}
	noopHTTP := time.Since(start)

	start = time.Now()
	for i := 0; i < iterations; i++ {
		metrics.RecordHTTPRequest("GET", "/monitors", 200, 500*time.Microsecond)
	}
	metricsHTTP := time.Since(start)
	overheadHTTP := (metricsHTTP - noopHTTP) / time.Duration(iterations)
	fmt.Printf("%-30s | %-15v | %-15v | %-15v\n", "RecordHTTPRequest (N=10k)", noopHTTP, metricsHTTP, overheadHTTP)

	// C. RecordDBOperation
	start = time.Now()
	for i := 0; i < iterations; i++ {
		noop.RecordDBOperation("save_cycle", true, 2*time.Millisecond)
	}
	noopDB := time.Since(start)

	start = time.Now()
	for i := 0; i < iterations; i++ {
		metrics.RecordDBOperation("save_cycle", true, 2*time.Millisecond)
	}
	metricsDB := time.Since(start)
	overheadDB := (metricsDB - noopDB) / time.Duration(iterations)
	fmt.Printf("%-30s | %-15v | %-15v | %-15v\n", "RecordDBOperation (N=10k)", noopDB, metricsDB, overheadDB)

	// 2. Structured Logging Overhead (slog JSON handler to io.Discard vs disabled logger)
	loggerEnabled := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelInfo}))
	loggerDisabled := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))

	start = time.Now()
	for i := 0; i < iterations; i++ {
		loggerDisabled.Info("cycle completed", "monitor_id", "mon-123", "duration_ms", 15)
	}
	durLogDisabled := time.Since(start)

	start = time.Now()
	for i := 0; i < iterations; i++ {
		loggerEnabled.Info("cycle completed", "monitor_id", "mon-123", "duration_ms", 15)
	}
	durLogEnabled := time.Since(start)
	overheadLog := (durLogEnabled - durLogDisabled) / time.Duration(iterations)
	fmt.Printf("%-30s | %-15v | %-15v | %-15v\n", "slog.Info JSON (N=10k)", durLogDisabled, durLogEnabled, overheadLog)

	// 3. PostgreSQL SaveCycle with Noop vs Metrics
	db, _, cleanup, err := StartEphemeralPostgres()
	if err != nil {
		t.Fatalf("start ephemeral postgres: %v", err)
	}
	defer cleanup()

	repoNoop := postgres.New(db)
	repoMetrics := postgres.New(db, postgres.WithRecorder(metrics))

	testMon := monitor.Monitor{
		ID:                  "30000000-0000-0000-0000-000000000001",
		Name:                "Obs Monitor",
		Kind:                monitor.KindHTTP,
		TargetURL:           "http://127.0.0.1",
		Method:              "GET",
		ExpectedStatusRange: "200-299",
		Interval:            60 * time.Second,
		Timeout:             5 * time.Second,
		Enabled:             true,
	}
	_ = repoNoop.CreateMonitor(context.Background(), testMon)

	dbCycles := 500
	cRes := checker.CheckResult{
		MonitorID:    testMon.ID,
		CheckedAt:    time.Now().UTC(),
		OK:           true,
		StatusCode:   200,
		Latency:      5 * time.Millisecond,
		AttemptCount: 1,
	}

	start = time.Now()
	for i := 0; i < dbCycles; i++ {
		_ = repoNoop.SaveCycle(context.Background(), testMon.ID, cRes, state.StateHealthy)
	}
	durSaveNoop := time.Since(start)

	start = time.Now()
	for i := 0; i < dbCycles; i++ {
		_ = repoMetrics.SaveCycle(context.Background(), testMon.ID, cRes, state.StateHealthy)
	}
	durSaveMetrics := time.Since(start)
	overheadSave := (durSaveMetrics - durSaveNoop) / time.Duration(dbCycles)
	fmt.Printf("%-30s | %-15v | %-15v | %-15v\n", "PG SaveCycle (N=500)", durSaveNoop, durSaveMetrics, overheadSave)

	// 4. Orchestrator Cycle Execution with Noop vs Metrics
	mockChk := &mockChecker{
		checkFunc: func(ctx context.Context, m monitor.Monitor) (checker.CheckResult, error) {
			return checker.CheckResult{OK: true, AttemptCount: 1}, nil
		},
	}
	orchNoop := scheduler.NewOrchestrator(mockChk, nil, retry.Config{MaxAttempts: 1})
	orchMetrics := scheduler.NewOrchestrator(mockChk, nil, retry.Config{MaxAttempts: 1, Recorder: metrics}).WithRecorder(metrics)

	orchCycles := 2000
	start = time.Now()
	for i := 0; i < orchCycles; i++ {
		_, _ = orchNoop.RunCycle(context.Background(), testMon, state.StateHealthy)
	}
	durOrchNoop := time.Since(start)

	start = time.Now()
	for i := 0; i < orchCycles; i++ {
		_, _ = orchMetrics.RunCycle(context.Background(), testMon, state.StateHealthy)
	}
	durOrchMetrics := time.Since(start)
	overheadOrch := (durOrchMetrics - durOrchNoop) / time.Duration(orchCycles)
	fmt.Printf("%-30s | %-15v | %-15v | %-15v\n", "Orchestrator Cycle (N=2k)", durOrchNoop, durOrchMetrics, overheadOrch)
	fmt.Println("=================================================================================")
}

func BenchmarkPrometheus_RecordCheck(b *testing.B) {
	metrics := telemetry.NewMetrics()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		metrics.RecordCheck("http", true, "", 10*time.Millisecond)
	}
}

func BenchmarkSlog_JSON_Info(b *testing.B) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.Info("check performed", "monitor_id", "mon-1", "ok", true, "latency_ms", 12)
	}
}
