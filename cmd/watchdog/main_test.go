package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/chavaliadi/watchdog/internal/checker"
	"github.com/chavaliadi/watchdog/internal/monitor"
	"github.com/chavaliadi/watchdog/internal/persistence"
	"github.com/chavaliadi/watchdog/internal/retry"
	"github.com/chavaliadi/watchdog/internal/scheduler"
	"github.com/chavaliadi/watchdog/internal/state"
)

type mockAppRepo struct {
	getMonitorFn func(ctx context.Context, id string) (monitor.Monitor, error)
	getStateFn   func(ctx context.Context, monitorID string) (state.State, error)
	saveCycleFn  func(ctx context.Context, monitorID string, result checker.CheckResult, nextState state.State) error
}

var _ persistence.Repository = (*mockAppRepo)(nil)

func (m *mockAppRepo) GetMonitor(ctx context.Context, id string) (monitor.Monitor, error) {
	if m.getMonitorFn != nil {
		return m.getMonitorFn(ctx, id)
	}
	return monitor.Monitor{ID: id, Kind: monitor.KindHTTP, TargetURL: "http://example.com"}, nil
}

func (m *mockAppRepo) GetState(ctx context.Context, monitorID string) (state.State, error) {
	if m.getStateFn != nil {
		return m.getStateFn(ctx, monitorID)
	}
	return state.StateUnknown, nil
}

func (m *mockAppRepo) CreateMonitor(ctx context.Context, mon monitor.Monitor) error {
	return nil
}

func (m *mockAppRepo) SaveCycle(
	ctx context.Context,
	monitorID string,
	result checker.CheckResult,
	nextState state.State,
) error {
	if m.saveCycleFn != nil {
		return m.saveCycleFn(ctx, monitorID, result, nextState)
	}
	return nil
}

type mockAppChecker struct {
	result checker.CheckResult
	err    error
}

func (m *mockAppChecker) Check(ctx context.Context, mon monitor.Monitor) (checker.CheckResult, error) {
	return m.result, m.err
}

func TestLoadConfig_MissingDatabaseURL(t *testing.T) {
	t.Setenv("WATCHDOG_DATABASE_URL", "")
	t.Setenv("WATCHDOG_MONITOR_ID", "mon-123")

	_, err := loadConfig()
	if err == nil {
		t.Fatal("expected error for missing WATCHDOG_DATABASE_URL, got nil")
	}
	if !strings.Contains(err.Error(), "WATCHDOG_DATABASE_URL") {
		t.Errorf("error message want mention of WATCHDOG_DATABASE_URL, got %v", err)
	}
}

func TestLoadConfig_MissingMonitorID(t *testing.T) {
	t.Setenv("WATCHDOG_DATABASE_URL", "postgres://localhost:5432/db")
	t.Setenv("WATCHDOG_MONITOR_ID", "")

	_, err := loadConfig()
	if err == nil {
		t.Fatal("expected error for missing WATCHDOG_MONITOR_ID, got nil")
	}
	if !strings.Contains(err.Error(), "WATCHDOG_MONITOR_ID") {
		t.Errorf("error message want mention of WATCHDOG_MONITOR_ID, got %v", err)
	}
}

func TestLoadConfig_Valid(t *testing.T) {
	expectedDB := "postgres://user:pass@localhost:5432/watchdog_db"
	expectedMon := "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"

	t.Setenv("WATCHDOG_DATABASE_URL", expectedDB)
	t.Setenv("WATCHDOG_MONITOR_ID", expectedMon)

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DatabaseURL != expectedDB {
		t.Errorf("expected DatabaseURL %q, got %q", expectedDB, cfg.DatabaseURL)
	}
	if cfg.MonitorID != expectedMon {
		t.Errorf("expected MonitorID %q, got %q", expectedMon, cfg.MonitorID)
	}
}

func TestRun_DatabaseErrorsPropagate(t *testing.T) {
	// Point to unreachable port with immediate failure
	t.Setenv("WATCHDOG_DATABASE_URL", "postgres://user:pass@127.0.0.1:1/nonexistent?sslmode=disable&connect_timeout=1")
	t.Setenv("WATCHDOG_MONITOR_ID", "mon-123")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := run(ctx)
	if err == nil {
		t.Fatal("expected database error on unreachable database, got nil")
	}
}

func TestExecute_MonitorLoadingErrorsPropagate(t *testing.T) {
	expectedErr := errors.New("monitor not found in repository")
	repo := &mockAppRepo{
		getMonitorFn: func(ctx context.Context, id string) (monitor.Monitor, error) {
			return monitor.Monitor{}, expectedErr
		},
	}

	orch := scheduler.NewOrchestrator(nil, nil, retry.Config{}, repo)
	err := execute(context.Background(), repo, orch, "mon-not-found")
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected monitor loading error %v, got %v", expectedErr, err)
	}
}

func TestExecute_StateLoadingErrorsPropagate(t *testing.T) {
	expectedErr := errors.New("state corrupted or missing")
	repo := &mockAppRepo{
		getMonitorFn: func(ctx context.Context, id string) (monitor.Monitor, error) {
			return monitor.Monitor{ID: id, Kind: monitor.KindHTTP, TargetURL: "https://example.com"}, nil
		},
		getStateFn: func(ctx context.Context, monitorID string) (state.State, error) {
			return "", expectedErr
		},
	}

	orch := scheduler.NewOrchestrator(nil, nil, retry.Config{}, repo)
	err := execute(context.Background(), repo, orch, "mon-state-err")
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected state loading error %v, got %v", expectedErr, err)
	}
}

func TestExecute_TargetHealthFailureExitsSuccessfully(t *testing.T) {
	// A monitor health failure (e.g. status 500) must NOT fail the application.
	// The check result captures OK=false, state transitions to UNHEALTHY, SaveCycle persists it,
	// and execute returns nil (successful Watchdog cycle).
	repo := &mockAppRepo{
		getMonitorFn: func(ctx context.Context, id string) (monitor.Monitor, error) {
			return monitor.Monitor{ID: id, Kind: monitor.KindHTTP, TargetURL: "https://example.com"}, nil
		},
		getStateFn: func(ctx context.Context, monitorID string) (state.State, error) {
			return state.StateHealthy, nil
		},
	}

	httpMock := &mockAppChecker{
		result: checker.CheckResult{
			OK:           false,
			StatusCode:   500,
			Latency:      80 * time.Millisecond,
			ErrorClass:   checker.ErrorClassStatus,
			ErrorDetail:  "500 Internal Server Error",
			AttemptCount: 3,
			CheckedAt:    time.Now().UTC(),
		},
	}

	orch := scheduler.NewOrchestrator(httpMock, nil, retry.Config{}, repo)
	err := execute(context.Background(), repo, orch, "mon-target-fail")
	if err != nil {
		t.Fatalf("target failure should result in clean cycle execution (nil error), got: %v", err)
	}
}

func TestExecute_SuccessfulCycle(t *testing.T) {
	repo := &mockAppRepo{
		getMonitorFn: func(ctx context.Context, id string) (monitor.Monitor, error) {
			return monitor.Monitor{ID: id, Kind: monitor.KindHTTP, TargetURL: "https://example.com"}, nil
		},
		getStateFn: func(ctx context.Context, monitorID string) (state.State, error) {
			return state.StateUnknown, nil
		},
	}

	httpMock := &mockAppChecker{
		result: checker.CheckResult{
			OK:           true,
			StatusCode:   200,
			Latency:      35 * time.Millisecond,
			AttemptCount: 1,
			CheckedAt:    time.Now().UTC(),
		},
	}

	orch := scheduler.NewOrchestrator(httpMock, nil, retry.Config{}, repo)
	err := execute(context.Background(), repo, orch, "mon-success")
	if err != nil {
		t.Fatalf("expected nil error for successful cycle, got: %v", err)
	}
}
