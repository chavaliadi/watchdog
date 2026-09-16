package service_test

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chavaliadi/watchdog/internal/checker"
	"github.com/chavaliadi/watchdog/internal/monitor"
	"github.com/chavaliadi/watchdog/internal/persistence"
	"github.com/chavaliadi/watchdog/internal/scheduler"
	"github.com/chavaliadi/watchdog/internal/service"
	"github.com/chavaliadi/watchdog/internal/state"
)

type mockRepo struct {
	mu           sync.Mutex
	monitors     map[string]monitor.Monitor
	states       map[string]state.State
	timestamps   map[string]time.Time
	checkResults map[string][]checker.CheckResult

	createErr error
	updateErr error
	deleteErr error
}

var _ persistence.Repository = (*mockRepo)(nil)

func newMockRepo() *mockRepo {
	return &mockRepo{
		monitors:     make(map[string]monitor.Monitor),
		states:       make(map[string]state.State),
		timestamps:   make(map[string]time.Time),
		checkResults: make(map[string][]checker.CheckResult),
	}
}

func (m *mockRepo) GetMonitor(ctx context.Context, id string) (monitor.Monitor, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	mon, ok := m.monitors[id]
	if !ok {
		return monitor.Monitor{}, sql.ErrNoRows
	}
	return mon, nil
}

func (m *mockRepo) ListMonitors(ctx context.Context) ([]monitor.Monitor, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	list := make([]monitor.Monitor, 0, len(m.monitors))
	for _, mon := range m.monitors {
		list = append(list, mon)
	}
	return list, nil
}

func (m *mockRepo) GetState(ctx context.Context, monitorID string) (state.State, error) {
	st, _, err := m.GetStateWithTimestamp(ctx, monitorID)
	return st, err
}

func (m *mockRepo) GetStateWithTimestamp(ctx context.Context, monitorID string) (state.State, time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.monitors[monitorID]; !ok {
		return "", time.Time{}, sql.ErrNoRows
	}
	st, ok := m.states[monitorID]
	if !ok {
		st = state.StateUnknown
	}
	ts := m.timestamps[monitorID]
	return st, ts, nil
}

func (m *mockRepo) CreateMonitor(ctx context.Context, mon monitor.Monitor) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.createErr != nil {
		return m.createErr
	}
	m.monitors[mon.ID] = mon
	m.states[mon.ID] = state.StateUnknown
	m.timestamps[mon.ID] = time.Now().UTC()
	return nil
}

func (m *mockRepo) UpdateMonitor(ctx context.Context, mon monitor.Monitor) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.updateErr != nil {
		return m.updateErr
	}
	if _, ok := m.monitors[mon.ID]; !ok {
		return sql.ErrNoRows
	}
	m.monitors[mon.ID] = mon
	return nil
}

func (m *mockRepo) DeleteMonitor(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.deleteErr != nil {
		return m.deleteErr
	}
	if _, ok := m.monitors[id]; !ok {
		return sql.ErrNoRows
	}
	delete(m.monitors, id)
	delete(m.states, id)
	delete(m.timestamps, id)
	delete(m.checkResults, id)
	return nil
}

func (m *mockRepo) ListCheckResults(ctx context.Context, monitorID string, limit int) ([]checker.CheckResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	results := m.checkResults[monitorID]
	if len(results) > limit {
		results = results[:limit]
	}
	res := make([]checker.CheckResult, len(results))
	copy(res, results)
	return res, nil
}

func (m *mockRepo) SaveCycle(ctx context.Context, monitorID string, result checker.CheckResult, nextState state.State) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states[monitorID] = nextState
	m.timestamps[monitorID] = result.CheckedAt
	m.checkResults[monitorID] = append([]checker.CheckResult{result}, m.checkResults[monitorID]...)
	return nil
}

type mockScheduler struct {
	mu          sync.Mutex
	startCalls  []string
	stopCalls   []string
	updateCalls []string

	startErr  error
	stopErr   error
	updateErr error
}

func (m *mockScheduler) StartMonitor(ctx context.Context, mon monitor.Monitor) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.startCalls = append(m.startCalls, mon.ID)
	return m.startErr
}

func (m *mockScheduler) StopMonitor(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopCalls = append(m.stopCalls, id)
	return m.stopErr
}

func (m *mockScheduler) UpdateMonitor(ctx context.Context, mon monitor.Monitor) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updateCalls = append(m.updateCalls, mon.ID)
	return m.updateErr
}

func TestMonitorService_CreateMonitor(t *testing.T) {
	ctx := context.Background()

	t.Run("valid HTTP monitor with defaults", func(t *testing.T) {
		repo := newMockRepo()
		sched := &mockScheduler{}
		svc := service.NewMonitorService(repo, sched)

		req := service.CreateMonitorRequest{
			Name:   "Production Web",
			Kind:   monitor.KindHTTP,
			Target: "https://example.com/health",
		}

		created, err := svc.CreateMonitor(ctx, req)
		if err != nil {
			t.Fatalf("CreateMonitor failed: %v", err)
		}

		if created.ID == "" {
			t.Error("expected non-empty ID")
		}
		if created.Method != "GET" {
			t.Errorf("expected default method GET, got %q", created.Method)
		}
		if created.Interval != 60*time.Second {
			t.Errorf("expected default interval 60s, got %v", created.Interval)
		}
		if created.Timeout != 5*time.Second {
			t.Errorf("expected default timeout 5s, got %v", created.Timeout)
		}
		if !created.Enabled {
			t.Error("expected default enabled true")
		}

		// Verify scheduler started
		if len(sched.startCalls) != 1 || sched.startCalls[0] != created.ID {
			t.Errorf("expected scheduler.StartMonitor called for %q", created.ID)
		}
	})

	t.Run("valid TCP monitor", func(t *testing.T) {
		repo := newMockRepo()
		sched := &mockScheduler{}
		svc := service.NewMonitorService(repo, sched)

		req := service.CreateMonitorRequest{
			Name:   "Redis Cluster",
			Kind:   monitor.KindTCP,
			Target: "127.0.0.1:6379",
		}

		created, err := svc.CreateMonitor(ctx, req)
		if err != nil {
			t.Fatalf("CreateMonitor failed: %v", err)
		}

		if created.Kind != monitor.KindTCP {
			t.Errorf("expected Kind TCP, got %v", created.Kind)
		}
		if created.TargetURL != "127.0.0.1:6379" {
			t.Errorf("expected target 127.0.0.1:6379, got %v", created.TargetURL)
		}
	})

	t.Run("compensating rollback on scheduler start failure", func(t *testing.T) {
		repo := newMockRepo()
		sched := &mockScheduler{
			startErr: errors.New("scheduler queue full"),
		}
		svc := service.NewMonitorService(repo, sched)

		req := service.CreateMonitorRequest{
			Name:   "Rollback Test",
			Kind:   monitor.KindHTTP,
			Target: "https://example.com",
		}

		_, err := svc.CreateMonitor(ctx, req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		// Verify DB monitor was rolled back (deleted)
		if len(repo.monitors) != 0 {
			t.Fatalf("expected 0 monitors in repo after rollback, got %d", len(repo.monitors))
		}
	})
}

func TestMonitorService_Validation(t *testing.T) {
	repo := newMockRepo()
	sched := &mockScheduler{}
	svc := service.NewMonitorService(repo, sched)
	ctx := context.Background()

	tests := []struct {
		name string
		req  service.CreateMonitorRequest
	}{
		{"empty name", service.CreateMonitorRequest{Name: "", Kind: monitor.KindHTTP, Target: "https://example.com"}},
		{"whitespace name", service.CreateMonitorRequest{Name: "   ", Kind: monitor.KindHTTP, Target: "https://example.com"}},
		{"empty target", service.CreateMonitorRequest{Name: "Name", Kind: monitor.KindHTTP, Target: ""}},
		{"unsupported kind", service.CreateMonitorRequest{Name: "Name", Kind: "udp", Target: "127.0.0.1:53"}},
		{"http invalid scheme", service.CreateMonitorRequest{Name: "Name", Kind: monitor.KindHTTP, Target: "ftp://example.com"}},
		{"http invalid method", service.CreateMonitorRequest{Name: "Name", Kind: monitor.KindHTTP, Target: "https://example.com", Method: "OPTIONS"}},
		{"http invalid status range A > B", service.CreateMonitorRequest{Name: "Name", Kind: monitor.KindHTTP, Target: "https://example.com", ExpectedStatusRange: "300-200"}},
		{"http invalid status range code > 599", service.CreateMonitorRequest{Name: "Name", Kind: monitor.KindHTTP, Target: "https://example.com", ExpectedStatusRange: "600"}},
		{"tcp target without port", service.CreateMonitorRequest{Name: "Name", Kind: monitor.KindTCP, Target: "example.com"}},
		{"tcp port out of range", service.CreateMonitorRequest{Name: "Name", Kind: monitor.KindTCP, Target: "example.com:70000"}},
		{"tcp with method", service.CreateMonitorRequest{Name: "Name", Kind: monitor.KindTCP, Target: "example.com:80", Method: "GET"}},
		{"tcp with expected status range", service.CreateMonitorRequest{Name: "Name", Kind: monitor.KindTCP, Target: "example.com:80", ExpectedStatusRange: "200"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.CreateMonitor(ctx, tc.req)
			if err == nil {
				t.Fatalf("expected validation error for %s, got nil", tc.name)
			}
			if !errors.Is(err, service.ErrInvalidInput) {
				t.Errorf("expected ErrInvalidInput, got %v", err)
			}
		})
	}
}

func TestMonitorService_PatchMonitor(t *testing.T) {
	repo := newMockRepo()
	sched := &mockScheduler{}
	svc := service.NewMonitorService(repo, sched)
	ctx := context.Background()

	created, err := svc.CreateMonitor(ctx, service.CreateMonitorRequest{
		Name:   "Original",
		Kind:   monitor.KindHTTP,
		Target: "https://original.example.com",
	})
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	t.Run("immutable kind rejection", func(t *testing.T) {
		newKind := monitor.KindTCP
		_, err := svc.PatchMonitor(ctx, created.ID, service.PatchMonitorRequest{
			Kind: &newKind,
		})
		if !errors.Is(err, service.ErrInvalidInput) {
			t.Fatalf("expected ErrInvalidInput when modifying kind, got: %v", err)
		}
	})

	t.Run("valid partial update", func(t *testing.T) {
		newName := "Patched Name"
		newInterval := int64(15000)
		newEnabled := false

		patched, err := svc.PatchMonitor(ctx, created.ID, service.PatchMonitorRequest{
			Name:       &newName,
			IntervalMs: &newInterval,
			Enabled:    &newEnabled,
		})
		if err != nil {
			t.Fatalf("PatchMonitor failed: %v", err)
		}

		if patched.Name != "Patched Name" {
			t.Errorf("expected name 'Patched Name', got %q", patched.Name)
		}
		if patched.Interval != 15*time.Second {
			t.Errorf("expected interval 15s, got %v", patched.Interval)
		}
		if patched.Enabled != false {
			t.Errorf("expected enabled false, got true")
		}

		// Verify scheduler update was called
		if len(sched.updateCalls) == 0 || sched.updateCalls[len(sched.updateCalls)-1] != created.ID {
			t.Error("expected scheduler.UpdateMonitor called")
		}
	})
}

func TestMonitorService_DeleteMonitor(t *testing.T) {
	repo := newMockRepo()
	sched := &mockScheduler{}
	svc := service.NewMonitorService(repo, sched)
	ctx := context.Background()

	created, err := svc.CreateMonitor(ctx, service.CreateMonitorRequest{
		Name:   "To Delete",
		Kind:   monitor.KindHTTP,
		Target: "https://delete.example.com",
	})
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// Verify StopMonitor is called before DeleteMonitor
	err = svc.DeleteMonitor(ctx, created.ID)
	if err != nil {
		t.Fatalf("DeleteMonitor failed: %v", err)
	}

	if len(sched.stopCalls) != 1 || sched.stopCalls[0] != created.ID {
		t.Errorf("expected scheduler.StopMonitor called for %q", created.ID)
	}
	if len(repo.monitors) != 0 {
		t.Errorf("expected monitor deleted from repo, got %d", len(repo.monitors))
	}

	// Delete missing monitor returns ErrNotFound
	err = svc.DeleteMonitor(ctx, "00000000-0000-0000-0000-000000000000")
	if !errors.Is(err, service.ErrNotFound) {
		t.Errorf("expected ErrNotFound for missing monitor, got %v", err)
	}
}

func TestMonitorService_GetStatusAndChecks(t *testing.T) {
	repo := newMockRepo()
	sched := &mockScheduler{}
	svc := service.NewMonitorService(repo, sched)
	ctx := context.Background()

	created, err := svc.CreateMonitor(ctx, service.CreateMonitorRequest{
		Name:   "Status Test",
		Kind:   monitor.KindHTTP,
		Target: "https://status.example.com",
	})
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// Status
	status, err := svc.GetStatus(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetStatus failed: %v", err)
	}
	if status.State != state.StateUnknown {
		t.Errorf("expected UNKNOWN state, got %q", status.State)
	}

	// Save check cycle
	cycleRes := checker.CheckResult{
		MonitorID:    created.ID,
		OK:           true,
		StatusCode:   200,
		Latency:      45 * time.Millisecond,
		AttemptCount: 1,
		CheckedAt:    time.Now().UTC(),
	}
	_ = repo.SaveCycle(ctx, created.ID, cycleRes, state.StateHealthy)

	// Status after cycle
	status, err = svc.GetStatus(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetStatus failed: %v", err)
	}
	if status.State != state.StateHealthy {
		t.Errorf("expected HEALTHY state, got %q", status.State)
	}

	// History
	history, err := svc.ListChecks(ctx, created.ID, 10)
	if err != nil {
		t.Fatalf("ListChecks failed: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("expected 1 check result, got %d", len(history))
	}
	if history[0].StatusCode != 200 {
		t.Errorf("expected status code 200, got %d", history[0].StatusCode)
	}
}

func TestCriticalRace_DeleteDuringActiveCycle(t *testing.T) {
	repo := newMockRepo()

	cycleRunning := make(chan struct{})
	allowCycleFinish := make(chan struct{})
	cycleCompleted := make(chan struct{})

	runner := &raceCycleRunner{
		runFn: func(ctx context.Context, m monitor.Monitor, current state.State) (scheduler.CycleResult, error) {
			close(cycleRunning)
			<-allowCycleFinish
			close(cycleCompleted)
			return scheduler.CycleResult{
				TransitionResult: state.TransitionResult{NextState: state.StateHealthy},
			}, nil
		},
	}

	ms, err := scheduler.NewMultiScheduler(repo, runner)
	if err != nil {
		t.Fatalf("create scheduler: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := ms.Start(ctx); err != nil {
		t.Fatalf("scheduler start: %v", err)
	}
	defer ms.Stop()

	svc := service.NewMonitorService(repo, ms)
	created, err := svc.CreateMonitor(ctx, service.CreateMonitorRequest{
		Name:   "Active Cycle Delete",
		Kind:   monitor.KindHTTP,
		Target: "https://example.com/delete-race",
	})
	if err != nil {
		t.Fatalf("create monitor: %v", err)
	}

	// Wait until cycle is actively executing inside runner
	<-cycleRunning

	deleteDone := make(chan error, 1)
	go func() {
		deleteDone <- svc.DeleteMonitor(ctx, created.ID)
	}()

	// Verify that while runner is blocked, monitor has NOT yet been deleted from DB
	select {
	case <-deleteDone:
		t.Fatal("DeleteMonitor finished before active cycle was completed!")
	case <-time.After(30 * time.Millisecond):
		// Expected: DeleteMonitor is waiting for runner termination
	}

	// Verify row still exists in DB
	if _, err := repo.GetMonitor(ctx, created.ID); err != nil {
		t.Fatalf("monitor should still exist in repo while cycle is executing, got: %v", err)
	}

	// Now allow cycle to finish
	close(allowCycleFinish)

	select {
	case err := <-deleteDone:
		if err != nil {
			t.Fatalf("DeleteMonitor returned error: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("DeleteMonitor timed out waiting for runner completion")
	}

	// Now verify DB deletion has occurred
	if _, err := repo.GetMonitor(ctx, created.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected ErrNoRows after delete, got %v", err)
	}
	if ms.HasRunner(created.ID) {
		t.Fatal("expected no live runner in scheduler after deletion")
	}
}

func TestCriticalRace_PatchIntervalWhileSleeping(t *testing.T) {
	repo := newMockRepo()
	var cycleCount int64
	secondCycleRun := make(chan struct{})

	runner := &raceCycleRunner{
		runFn: func(ctx context.Context, m monitor.Monitor, current state.State) (scheduler.CycleResult, error) {
			count := atomic.AddInt64(&cycleCount, 1)
			if count == 2 {
				close(secondCycleRun)
			}
			return scheduler.CycleResult{
				TransitionResult: state.TransitionResult{NextState: state.StateHealthy},
			}, nil
		},
	}

	ms, err := scheduler.NewMultiScheduler(repo, runner)
	if err != nil {
		t.Fatalf("create scheduler: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := ms.Start(ctx); err != nil {
		t.Fatalf("scheduler start: %v", err)
	}
	defer ms.Stop()

	svc := service.NewMonitorService(repo, ms)
	created, err := svc.CreateMonitor(ctx, service.CreateMonitorRequest{
		Name:   "Patch Sleep",
		Kind:   monitor.KindHTTP,
		Target: "https://example.com/sleep",
	})
	if err != nil {
		t.Fatalf("create monitor: %v", err)
	}

	// Wait briefly for initial cycle
	time.Sleep(30 * time.Millisecond)
	if ms.ActiveRunners() != 1 {
		t.Fatalf("expected 1 active runner, got %d", ms.ActiveRunners())
	}

	// PATCH interval to 1000ms while runner is sleeping
	newInterval := int64(1000)
	patched, err := svc.PatchMonitor(ctx, created.ID, service.PatchMonitorRequest{
		IntervalMs: &newInterval,
	})
	if err != nil {
		t.Fatalf("PatchMonitor failed: %v", err)
	}

	if patched.Interval != 1000*time.Millisecond {
		t.Errorf("expected 1000ms interval, got %v", patched.Interval)
	}

	// Ensure only ONE live runner exists
	if count := ms.ActiveRunners(); count != 1 {
		t.Fatalf("expected exactly 1 live runner after patch, got %d", count)
	}

	// New runner immediately executes
	select {
	case <-secondCycleRun:
		// Success
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for new runner cycle execution")
	}

	if count := ms.ActiveRunners(); count != 1 {
		t.Fatalf("expected exactly 1 live runner after cycle, got %d", count)
	}
}

type raceCycleRunner struct {
	runFn func(ctx context.Context, m monitor.Monitor, current state.State) (scheduler.CycleResult, error)
}

func (r *raceCycleRunner) RunCycle(ctx context.Context, m monitor.Monitor, current state.State) (scheduler.CycleResult, error) {
	if r.runFn != nil {
		return r.runFn(ctx, m, current)
	}
	return scheduler.CycleResult{
		TransitionResult: state.TransitionResult{NextState: state.StateHealthy},
	}, nil
}

