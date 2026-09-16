package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chavaliadi/watchdog/internal/api"
	"github.com/chavaliadi/watchdog/internal/checker"
	"github.com/chavaliadi/watchdog/internal/monitor"
	"github.com/chavaliadi/watchdog/internal/persistence"
	"github.com/chavaliadi/watchdog/internal/retry"
	"github.com/chavaliadi/watchdog/internal/scheduler"
	"github.com/chavaliadi/watchdog/internal/service"
	"github.com/chavaliadi/watchdog/internal/state"
	"github.com/chavaliadi/watchdog/internal/worker"
)

type mockAppRepo struct {
	mu             sync.RWMutex
	monitors       []monitor.Monitor
	states         map[string]state.State
	listMonitorsFn func(ctx context.Context) ([]monitor.Monitor, error)
	getStateFn     func(ctx context.Context, monitorID string) (state.State, error)
	saveCycleFn    func(ctx context.Context, monitorID string, result checker.CheckResult, nextState state.State) error
}

var _ persistence.Repository = (*mockAppRepo)(nil)

func (m *mockAppRepo) GetMonitor(ctx context.Context, id string) (monitor.Monitor, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, mon := range m.monitors {
		if mon.ID == id {
			return mon, nil
		}
	}
	return monitor.Monitor{ID: id, Kind: monitor.KindHTTP, TargetURL: "http://example.com"}, nil
}

func (m *mockAppRepo) ListMonitors(ctx context.Context) ([]monitor.Monitor, error) {
	if m.listMonitorsFn != nil {
		return m.listMonitorsFn(ctx)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	res := make([]monitor.Monitor, len(m.monitors))
	copy(res, m.monitors)
	return res, nil
}

func (m *mockAppRepo) GetState(ctx context.Context, monitorID string) (state.State, error) {
	if m.getStateFn != nil {
		return m.getStateFn(ctx, monitorID)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if st, ok := m.states[monitorID]; ok {
		return st, nil
	}
	return state.StateUnknown, nil
}

func (m *mockAppRepo) CreateMonitor(ctx context.Context, mon monitor.Monitor) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.monitors = append(m.monitors, mon)
	return nil
}

func (m *mockAppRepo) UpdateMonitor(ctx context.Context, mon monitor.Monitor) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, existing := range m.monitors {
		if existing.ID == mon.ID {
			m.monitors[i] = mon
			return nil
		}
	}
	return errors.New("not found")
}

func (m *mockAppRepo) DeleteMonitor(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, existing := range m.monitors {
		if existing.ID == id {
			m.monitors = append(m.monitors[:i], m.monitors[i+1:]...)
			delete(m.states, id)
			return nil
		}
	}
	return errors.New("not found")
}

func (m *mockAppRepo) ListCheckResults(ctx context.Context, monitorID string, limit int) ([]checker.CheckResult, error) {
	return []checker.CheckResult{}, nil
}

func (m *mockAppRepo) GetStateWithTimestamp(ctx context.Context, monitorID string) (state.State, time.Time, error) {
	st, err := m.GetState(ctx, monitorID)
	return st, time.Now().UTC(), err
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
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.states == nil {
		m.states = make(map[string]state.State)
	}
	m.states[monitorID] = nextState
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

	_, err := loadConfig()
	if err == nil {
		t.Fatal("expected error for missing WATCHDOG_DATABASE_URL, got nil")
	}
	if !strings.Contains(err.Error(), "WATCHDOG_DATABASE_URL") {
		t.Errorf("error message want mention of WATCHDOG_DATABASE_URL, got %v", err)
	}
}

func TestLoadConfig_DefaultWorkerConcurrency(t *testing.T) {
	expectedDB := "postgres://user:pass@localhost:5432/watchdog_db"
	t.Setenv("WATCHDOG_DATABASE_URL", expectedDB)
	t.Setenv("WATCHDOG_WORKER_CONCURRENCY", "")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DatabaseURL != expectedDB {
		t.Errorf("expected DatabaseURL %q, got %q", expectedDB, cfg.DatabaseURL)
	}
	if cfg.WorkerConcurrency != defaultWorkerConcurrency {
		t.Errorf("expected default concurrency %d, got %d", defaultWorkerConcurrency, cfg.WorkerConcurrency)
	}
}

func TestLoadConfig_CustomWorkerConcurrency(t *testing.T) {
	expectedDB := "postgres://user:pass@localhost:5432/watchdog_db"
	t.Setenv("WATCHDOG_DATABASE_URL", expectedDB)
	t.Setenv("WATCHDOG_WORKER_CONCURRENCY", "12")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.WorkerConcurrency != 12 {
		t.Errorf("expected concurrency 12, got %d", cfg.WorkerConcurrency)
	}
}

func TestRun_DatabaseErrorsPropagate(t *testing.T) {
	t.Setenv("WATCHDOG_DATABASE_URL", "postgres://user:pass@127.0.0.1:1/nonexistent?sslmode=disable&connect_timeout=1")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := run(ctx)
	if err == nil {
		t.Fatal("expected database error on unreachable database, got nil")
	}
}

func testAPIServer(repo persistence.Repository, multiSched *scheduler.MultiScheduler) *api.Server {
	svc := service.NewMonitorService(repo, multiSched)
	handlers := api.NewHandlers(svc)
	return api.NewServer(api.Config{Addr: "127.0.0.1:0"}, handlers)
}

func TestExecute_DiscoveryErrorsPropagate(t *testing.T) {
	expectedErr := errors.New("database discovery failed")
	repo := &mockAppRepo{
		listMonitorsFn: func(ctx context.Context) ([]monitor.Monitor, error) {
			return nil, expectedErr
		},
	}

	orch := scheduler.NewOrchestrator(nil, nil, retry.Config{}, repo)
	pool, err := worker.NewPool(worker.Config{MaxConcurrency: 2}, orch)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}

	multiSched, err := scheduler.NewMultiScheduler(repo, pool)
	if err != nil {
		t.Fatalf("create multi-scheduler: %v", err)
	}

	err = execute(context.Background(), multiSched, testAPIServer(repo, multiSched))
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected discovery error %v, got %v", expectedErr, err)
	}
}

func TestExecute_MultiMonitorDaemonLifecycle(t *testing.T) {
	repo := &mockAppRepo{
		monitors: []monitor.Monitor{
			{ID: "mon-1", Name: "M1", Enabled: true, Kind: monitor.KindHTTP, TargetURL: "http://example.com/1", Interval: 50 * time.Millisecond},
			{ID: "mon-2", Name: "M2", Enabled: true, Kind: monitor.KindHTTP, TargetURL: "http://example.com/2", Interval: 50 * time.Millisecond},
			{ID: "mon-3", Name: "M3", Enabled: false, Kind: monitor.KindHTTP, TargetURL: "http://example.com/3", Interval: 50 * time.Millisecond},
		},
		states: map[string]state.State{
			"mon-1": state.StateUnknown,
			"mon-2": state.StateUnknown,
			"mon-3": state.StateUnknown,
		},
	}

	var savedMu sync.Mutex
	savedMonitors := make(map[string]bool)

	repo.saveCycleFn = func(ctx context.Context, monitorID string, result checker.CheckResult, nextState state.State) error {
		savedMu.Lock()
		savedMonitors[monitorID] = true
		savedMu.Unlock()
		return nil
	}

	httpMock := &mockAppChecker{
		result: checker.CheckResult{
			OK:           true,
			StatusCode:   200,
			AttemptCount: 1,
			CheckedAt:    time.Now().UTC(),
		},
	}

	orch := scheduler.NewOrchestrator(httpMock, nil, retry.Config{}, repo)
	pool, err := worker.NewPool(worker.Config{MaxConcurrency: 3}, orch)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	pool.Start(ctx)
	defer func() {
		pool.Stop()
		pool.Wait()
	}()

	multiSched, err := scheduler.NewMultiScheduler(repo, pool)
	if err != nil {
		t.Fatalf("create multi-scheduler: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- execute(ctx, multiSched, testAPIServer(repo, multiSched))
	}()

	// Wait until both enabled monitors have executed their initial cycles
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		savedMu.Lock()
		count := len(savedMonitors)
		savedMu.Unlock()
		if count >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	savedMu.Lock()
	mon1Run := savedMonitors["mon-1"]
	mon2Run := savedMonitors["mon-2"]
	mon3Run := savedMonitors["mon-3"]
	savedMu.Unlock()

	if !mon1Run || !mon2Run {
		t.Errorf("expected mon-1 and mon-2 to execute, got mon1=%v, mon2=%v", mon1Run, mon2Run)
	}
	if mon3Run {
		t.Error("disabled mon-3 must not execute")
	}

	// Trigger graceful cancellation
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected clean exit from execute, got: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for multi-monitor daemon to shut down")
	}
}

func TestExecute_HealthFailureRunsCleanly(t *testing.T) {
	// A health failure (HTTP 500) must transition state to UNHEALTHY, persist,
	// and keep the daemon running smoothly without crashing or aborting.
	repo := &mockAppRepo{
		monitors: []monitor.Monitor{
			{ID: "mon-fail", Name: "Failing", Enabled: true, Kind: monitor.KindHTTP, TargetURL: "http://example.com/fail", Interval: 50 * time.Millisecond},
		},
		states: map[string]state.State{
			"mon-fail": state.StateHealthy,
		},
	}

	savedCh := make(chan state.State, 1)
	repo.saveCycleFn = func(ctx context.Context, monitorID string, result checker.CheckResult, nextState state.State) error {
		select {
		case savedCh <- nextState:
		default:
		}
		return nil
	}

	httpMock := &mockAppChecker{
		result: checker.CheckResult{
			OK:           false,
			StatusCode:   500,
			ErrorClass:   checker.ErrorClassStatus,
			ErrorDetail:  "500 Internal Server Error",
			AttemptCount: 3,
			CheckedAt:    time.Now().UTC(),
		},
	}

	orch := scheduler.NewOrchestrator(httpMock, nil, retry.Config{}, repo)
	pool, err := worker.NewPool(worker.Config{MaxConcurrency: 1}, orch)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	pool.Start(ctx)
	defer func() {
		pool.Stop()
		pool.Wait()
	}()

	multiSched, err := scheduler.NewMultiScheduler(repo, pool)
	if err != nil {
		t.Fatalf("create multi-scheduler: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- execute(ctx, multiSched, testAPIServer(repo, multiSched))
	}()

	select {
	case nextSt := <-savedCh:
		if nextSt != state.StateUnhealthy {
			t.Errorf("expected transitioned state UNHEALTHY, got %v", nextSt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for failing cycle to persist")
	}

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected clean exit from execute on health failure, got: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for daemon to shut down")
	}
}

func TestLoadConfig_HTTPPort(t *testing.T) {
	t.Setenv("WATCHDOG_DATABASE_URL", "postgres://user:pass@localhost:5432/db")

	t.Run("default HTTP port", func(t *testing.T) {
		t.Setenv("WATCHDOG_HTTP_PORT", "")
		cfg, err := loadConfig()
		if err != nil {
			t.Fatalf("loadConfig failed: %v", err)
		}
		if cfg.HTTPPort != ":8080" {
			t.Errorf("expected default port :8080, got %q", cfg.HTTPPort)
		}
	})

	t.Run("custom HTTP port with colon", func(t *testing.T) {
		t.Setenv("WATCHDOG_HTTP_PORT", ":9090")
		cfg, err := loadConfig()
		if err != nil {
			t.Fatalf("loadConfig failed: %v", err)
		}
		if cfg.HTTPPort != ":9090" {
			t.Errorf("expected :9090, got %q", cfg.HTTPPort)
		}
	})

	t.Run("custom HTTP port without colon", func(t *testing.T) {
		t.Setenv("WATCHDOG_HTTP_PORT", "9090")
		cfg, err := loadConfig()
		if err != nil {
			t.Fatalf("loadConfig failed: %v", err)
		}
		if cfg.HTTPPort != ":9090" {
			t.Errorf("expected :9090, got %q", cfg.HTTPPort)
		}
	})
}

func TestExecute_ServerBindFailure(t *testing.T) {
	repo := &mockAppRepo{}
	orch := scheduler.NewOrchestrator(nil, nil, retry.Config{}, repo)
	pool, err := worker.NewPool(worker.Config{MaxConcurrency: 1}, orch)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}

	multiSched, err := scheduler.NewMultiScheduler(repo, pool)
	if err != nil {
		t.Fatalf("create multi-scheduler: %v", err)
	}

	// Create server with invalid bind address to force an immediate startup failure
	svc := service.NewMonitorService(repo, multiSched)
	handlers := api.NewHandlers(svc)
	badServer := api.NewServer(api.Config{Addr: "999.999.999.999:99999"}, handlers)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err = execute(ctx, multiSched, badServer)
	if err == nil {
		t.Fatal("expected execute to fail with server bind error, got nil")
	}
	if !strings.Contains(err.Error(), "http server error") {
		t.Errorf("expected 'http server error', got: %v", err)
	}
}

