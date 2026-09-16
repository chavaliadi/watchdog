package postgres_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/chavaliadi/watchdog/internal/checker"
	"github.com/chavaliadi/watchdog/internal/monitor"
	"github.com/chavaliadi/watchdog/internal/persistence/postgres"
	"github.com/chavaliadi/watchdog/internal/retry"
	"github.com/chavaliadi/watchdog/internal/scheduler"
	"github.com/chavaliadi/watchdog/internal/state"
)

type integrationMockChecker struct {
	results []checker.CheckResult
	errs    []error
	callIdx int
}

func (m *integrationMockChecker) Check(ctx context.Context, mon monitor.Monitor) (checker.CheckResult, error) {
	idx := m.callIdx
	m.callIdx++
	var res checker.CheckResult
	var err error
	if idx < len(m.results) {
		res = m.results[idx]
	}
	if idx < len(m.errs) {
		err = m.errs[idx]
	}
	return res, err
}

func integrationRetryConfig() retry.Config {
	return retry.Config{
		MaxAttempts: 3,
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

// TestOrchestratorPostgresIntegration verifies the complete end-to-end flow:
// PostgreSQL monitor -> Repository.GetMonitor / GetState -> Orchestrator.RunCycle
// -> Checker -> State Transition -> Repository.SaveCycle -> PostgreSQL verification.
func TestOrchestratorPostgresIntegration(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()
	repo := postgres.New(testDB)

	mID := "99999999-0000-0000-0000-000000000001"
	origMonitor := monitor.Monitor{
		ID:                  mID,
		Name:                "Integration API Target",
		Kind:                monitor.KindHTTP,
		TargetURL:           "https://api.integration-test.local/health",
		Method:              "GET",
		ExpectedStatusRange: "200-299",
	}

	// 1. Persist initial monitor and state in PostgreSQL
	if err := repo.CreateMonitor(ctx, origMonitor); err != nil {
		t.Fatalf("CreateMonitor failed: %v", err)
	}

	// 2. Load monitor and current state through Repository interface
	loadedMonitor, err := repo.GetMonitor(ctx, mID)
	if err != nil {
		t.Fatalf("GetMonitor failed: %v", err)
	}
	currentState, err := repo.GetState(ctx, mID)
	if err != nil {
		t.Fatalf("GetState failed: %v", err)
	}
	if currentState != state.StateUnknown {
		t.Fatalf("expected initial state UNKNOWN, got %q", currentState)
	}

	// 3. Configure Orchestrator with mock checkers and PostgreSQL repository
	httpMock := &integrationMockChecker{
		results: []checker.CheckResult{
			{
				MonitorID:    mID,
				OK:           true,
				StatusCode:   200,
				Latency:      65 * time.Millisecond,
				AttemptCount: 1,
				CheckedAt:    time.Now().UTC(),
			},
		},
		errs: []error{nil},
	}
	tcpMock := &integrationMockChecker{}

	orch := scheduler.NewOrchestrator(httpMock, tcpMock, integrationRetryConfig(), repo)

	// 4. Run cycle: checker -> transition -> SaveCycle
	cycleResult, err := orch.RunCycle(ctx, loadedMonitor, currentState)
	if err != nil {
		t.Fatalf("RunCycle failed: %v", err)
	}

	if !cycleResult.CheckResult.OK {
		t.Errorf("expected CheckResult.OK = true")
	}
	if cycleResult.TransitionResult.NextState != state.StateHealthy {
		t.Errorf("expected TransitionResult.NextState = HEALTHY, got %q", cycleResult.TransitionResult.NextState)
	}

	// 5. Query PostgreSQL directly to verify persisted check_results row
	var (
		resID        int64
		ok           bool
		statusCode   sql.NullInt64
		latencyMs    int64
		attemptCount int
		resCheckedAt time.Time
	)
	err = testDB.QueryRow(`
		SELECT id, ok, status_code, latency_ms, attempt_count, checked_at
		FROM check_results
		WHERE monitor_id = $1
	`, mID).Scan(&resID, &ok, &statusCode, &latencyMs, &attemptCount, &resCheckedAt)
	if err != nil {
		t.Fatalf("failed to query check_results row in PostgreSQL: %v", err)
	}

	if !ok {
		t.Errorf("persisted check_result ok is false, want true")
	}
	if !statusCode.Valid || statusCode.Int64 != 200 {
		t.Errorf("persisted status_code want 200, got %v", statusCode)
	}
	if latencyMs != 65 {
		t.Errorf("persisted latency_ms want 65, got %d", latencyMs)
	}
	if attemptCount != 1 {
		t.Errorf("persisted attempt_count want 1, got %d", attemptCount)
	}

	// 6. Query PostgreSQL directly to verify updated monitor_states row
	var persistedState string
	err = testDB.QueryRow(`
		SELECT state
		FROM monitor_states
		WHERE monitor_id = $1
	`, mID).Scan(&persistedState)
	if err != nil {
		t.Fatalf("failed to query monitor_states row in PostgreSQL: %v", err)
	}
	if persistedState != "HEALTHY" {
		t.Errorf("persisted state in PostgreSQL want 'HEALTHY', got %q", persistedState)
	}

	// 7. Execute subsequent failing cycle through Orchestrator and verify PostgreSQL updates
	httpMock.results = append(httpMock.results,
		checker.CheckResult{
			MonitorID:    mID,
			OK:           false,
			StatusCode:   503,
			Latency:      120 * time.Millisecond,
			ErrorClass:   checker.ErrorClassStatus,
			ErrorDetail:  "Service Unavailable",
			AttemptCount: 3,
			CheckedAt:    time.Now().UTC(),
		},
		checker.CheckResult{
			MonitorID:    mID,
			OK:           false,
			StatusCode:   503,
			Latency:      120 * time.Millisecond,
			ErrorClass:   checker.ErrorClassStatus,
			ErrorDetail:  "Service Unavailable",
			AttemptCount: 3,
			CheckedAt:    time.Now().UTC(),
		},
		checker.CheckResult{
			MonitorID:    mID,
			OK:           false,
			StatusCode:   503,
			Latency:      120 * time.Millisecond,
			ErrorClass:   checker.ErrorClassStatus,
			ErrorDetail:  "Service Unavailable",
			AttemptCount: 3,
			CheckedAt:    time.Now().UTC(),
		},
	)
	httpMock.errs = append(httpMock.errs, nil, nil, nil)

	currentState, err = repo.GetState(ctx, mID)
	if err != nil {
		t.Fatalf("GetState failed before second cycle: %v", err)
	}
	if currentState != state.StateHealthy {
		t.Fatalf("expected state before second cycle to be HEALTHY, got %q", currentState)
	}

	cycleResult2, err := orch.RunCycle(ctx, loadedMonitor, currentState)
	if err != nil {
		t.Fatalf("second RunCycle failed: %v", err)
	}
	if cycleResult2.CheckResult.OK {
		t.Errorf("second cycle expected OK = false")
	}
	if cycleResult2.TransitionResult.NextState != state.StateUnhealthy {
		t.Errorf("second cycle expected NextState = UNHEALTHY, got %q", cycleResult2.TransitionResult.NextState)
	}

	// Verify monitor_states in PostgreSQL updated to UNHEALTHY
	err = testDB.QueryRow(`
		SELECT state
		FROM monitor_states
		WHERE monitor_id = $1
	`, mID).Scan(&persistedState)
	if err != nil {
		t.Fatalf("failed to query monitor_states row after second cycle: %v", err)
	}
	if persistedState != "UNHEALTHY" {
		t.Errorf("persisted state in PostgreSQL want 'UNHEALTHY', got %q", persistedState)
	}

	// Verify check_results row count is now 2
	var checkResultCount int
	err = testDB.QueryRow(`
		SELECT COUNT(*)
		FROM check_results
		WHERE monitor_id = $1
	`, mID).Scan(&checkResultCount)
	if err != nil {
		t.Fatalf("failed to count check_results: %v", err)
	}
	if checkResultCount != 2 {
		t.Errorf("check_results count want 2, got %d", checkResultCount)
	}
}
