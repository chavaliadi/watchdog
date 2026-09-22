package scheduler_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/chavaliadi/watchdog/internal/checker"
	"github.com/chavaliadi/watchdog/internal/monitor"
	"github.com/chavaliadi/watchdog/internal/retry"
	"github.com/chavaliadi/watchdog/internal/scheduler"
	"github.com/chavaliadi/watchdog/internal/state"
)

type recordedCheck struct {
	kind       string
	ok         bool
	errorClass string
	duration   time.Duration
}

type recordedActiveMonitor struct {
	kind  string
	count int
}

type mockSchedulerRecorder struct {
	mu             sync.Mutex
	checks         []recordedCheck
	retries        []string
	cycleErrors    []string
	activeMonitors []recordedActiveMonitor
}

func (m *mockSchedulerRecorder) RecordCheck(kind string, ok bool, errClass string, duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.checks = append(m.checks, recordedCheck{kind: kind, ok: ok, errorClass: errClass, duration: duration})
}

func (m *mockSchedulerRecorder) RecordRetry(kind string, outcome string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.retries = append(m.retries, outcome)
}

func (m *mockSchedulerRecorder) RecordCycleError(stage string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cycleErrors = append(m.cycleErrors, stage)
}

func (m *mockSchedulerRecorder) RecordDBOperation(op string, ok bool, duration time.Duration) {}
func (m *mockSchedulerRecorder) RecordHTTPRequest(method, route string, statusCode int, duration time.Duration) {
}

func (m *mockSchedulerRecorder) RecordActiveMonitors(kind string, count int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.activeMonitors = append(m.activeMonitors, recordedActiveMonitor{kind: kind, count: count})
}

type stubChecker struct {
	res checker.CheckResult
	err error
}

func (s *stubChecker) Check(ctx context.Context, m monitor.Monitor) (checker.CheckResult, error) {
	return s.res, s.err
}

type instrMockRepo struct {
	mu       sync.RWMutex
	monitors []monitor.Monitor
	states   map[string]state.State
}

func newInstrMockRepo() *instrMockRepo {
	return &instrMockRepo{
		states: make(map[string]state.State),
	}
}

func (m *instrMockRepo) GetMonitor(ctx context.Context, id string) (monitor.Monitor, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, mon := range m.monitors {
		if mon.ID == id {
			return mon, nil
		}
	}
	return monitor.Monitor{}, errors.New("not found")
}

func (m *instrMockRepo) ListMonitors(ctx context.Context) ([]monitor.Monitor, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]monitor.Monitor(nil), m.monitors...), nil
}

func (m *instrMockRepo) GetState(ctx context.Context, monitorID string) (state.State, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.states[monitorID], nil
}

func (m *instrMockRepo) GetStateWithTimestamp(ctx context.Context, monitorID string) (state.State, time.Time, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.states[monitorID], time.Now(), nil
}

func (m *instrMockRepo) CreateMonitor(ctx context.Context, mon monitor.Monitor) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.monitors = append(m.monitors, mon)
	m.states[mon.ID] = state.StateUnknown
	return nil
}

func (m *instrMockRepo) UpdateMonitor(ctx context.Context, mon monitor.Monitor) error { return nil }
func (m *instrMockRepo) DeleteMonitor(ctx context.Context, id string) error           { return nil }
func (m *instrMockRepo) ListCheckResults(ctx context.Context, monitorID string, limit int) ([]checker.CheckResult, error) {
	return nil, nil
}
func (m *instrMockRepo) SaveCycle(ctx context.Context, monitorID string, result checker.CheckResult, nextState state.State) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states[monitorID] = nextState
	return nil
}

type instrFailingRepo struct {
	instrMockRepo
}

func (s *instrFailingRepo) SaveCycle(ctx context.Context, monitorID string, result checker.CheckResult, nextState state.State) error {
	return errors.New("db save cycle failure")
}

type instrStubRunner struct{}

func (r *instrStubRunner) RunCycle(ctx context.Context, m monitor.Monitor, current state.State) (scheduler.CycleResult, error) {
	return scheduler.CycleResult{
		Monitor:          m,
		TransitionResult: state.TransitionResult{NextState: current},
	}, nil
}

func TestOrchestrator_CheckRecording_HealthyAndUnhealthy(t *testing.T) {
	// 1. Healthy check: ok=true, error_class=""
	recorder := &mockSchedulerRecorder{}
	cHealthy := &stubChecker{
		res: checker.CheckResult{OK: true, StatusCode: 200, Latency: 15 * time.Millisecond},
	}
	orch := scheduler.NewOrchestrator(cHealthy, nil, retry.Config{}).WithRecorder(recorder)

	m := monitor.Monitor{ID: "mon-h", Kind: monitor.KindHTTP}
	_, err := orch.RunCycle(context.Background(), m, state.StateHealthy)
	if err != nil {
		t.Fatalf("unexpected RunCycle error: %v", err)
	}

	recorder.mu.Lock()
	if len(recorder.checks) != 1 {
		t.Fatalf("expected 1 check recorded, got %d", len(recorder.checks))
	}
	if !recorder.checks[0].ok || recorder.checks[0].kind != "http" {
		t.Errorf("expected ok=true, kind=http, got %+v", recorder.checks[0])
	}
	if len(recorder.cycleErrors) != 0 {
		t.Errorf("expected 0 cycle errors for healthy check, got %d", len(recorder.cycleErrors))
	}
	recorder.mu.Unlock()

	// 2. Unhealthy check: ok=false, error_class="status"
	// MUST still be recorded as a check (ok=false), and MUST NOT be recorded as a cycle error!
	cUnhealthy := &stubChecker{
		res: checker.CheckResult{OK: false, StatusCode: 500, ErrorClass: checker.ErrorClassStatus, Latency: 25 * time.Millisecond},
	}
	orchUnhealthy := scheduler.NewOrchestrator(cUnhealthy, nil, retry.Config{}).WithRecorder(recorder)

	_, err = orchUnhealthy.RunCycle(context.Background(), m, state.StateHealthy)
	if err != nil {
		t.Fatalf("unexpected RunCycle error: %v", err)
	}

	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if len(recorder.checks) != 2 {
		t.Fatalf("expected 2 checks recorded, got %d", len(recorder.checks))
	}
	if recorder.checks[1].ok || recorder.checks[1].errorClass != "status" {
		t.Errorf("expected ok=false, errorClass=status, got %+v", recorder.checks[1])
	}
	if len(recorder.cycleErrors) != 0 {
		t.Errorf("unhealthy check must NOT be recorded as cycle error, got %v", recorder.cycleErrors)
	}
}

func TestOrchestrator_CycleErrors(t *testing.T) {
	// 1. Checker returns genuine system/configuration error (e.g. invalid target)
	recorder := &mockSchedulerRecorder{}
	cFailing := &stubChecker{
		err: errors.New("cannot resolve target DNS config"),
	}
	orch := scheduler.NewOrchestrator(cFailing, nil, retry.Config{}).WithRecorder(recorder)
	m := monitor.Monitor{ID: "mon-fail", Kind: monitor.KindHTTP}

	_, err := orch.RunCycle(context.Background(), m, state.StateUnknown)
	if err == nil {
		t.Fatal("expected error from RunCycle, got nil")
	}

	recorder.mu.Lock()
	if len(recorder.cycleErrors) != 1 || recorder.cycleErrors[0] != "run_cycle" {
		t.Errorf("expected run_cycle error, got %v", recorder.cycleErrors)
	}
	if len(recorder.checks) != 0 {
		t.Errorf("failed cycle that returned error should not record check, got %d", len(recorder.checks))
	}
	recorder.mu.Unlock()

	// 2. Persistence SaveCycle failure -> save_cycle error
	cOK := &stubChecker{
		res: checker.CheckResult{OK: true, StatusCode: 200},
	}
	failingRepo := &instrFailingRepo{instrMockRepo: *newInstrMockRepo()}
	orchSaveFail := scheduler.NewOrchestrator(cOK, nil, retry.Config{}, failingRepo).WithRecorder(recorder)

	_, err = orchSaveFail.RunCycle(context.Background(), m, state.StateHealthy)
	if err == nil {
		t.Fatal("expected save cycle error, got nil")
	}

	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	// Check was recorded before save_cycle failed
	if len(recorder.checks) != 1 {
		t.Errorf("check should be recorded before save cycle error, got %d", len(recorder.checks))
	}
	if len(recorder.cycleErrors) != 2 || recorder.cycleErrors[1] != "save_cycle" {
		t.Errorf("expected save_cycle error, got %v", recorder.cycleErrors)
	}
}

func TestMultiScheduler_ActiveMonitorsGaugeTracking(t *testing.T) {
	repo := newInstrMockRepo()
	_ = repo.CreateMonitor(context.Background(), monitor.Monitor{
		ID:       "mon-1",
		Kind:     monitor.KindHTTP,
		Interval: 50 * time.Millisecond,
		Enabled:  true,
	})
	_ = repo.CreateMonitor(context.Background(), monitor.Monitor{
		ID:       "mon-2",
		Kind:     monitor.KindTCP,
		Interval: 50 * time.Millisecond,
		Enabled:  true,
	})

	runner := &instrStubRunner{}
	ms, err := scheduler.NewMultiScheduler(repo, runner)
	if err != nil {
		t.Fatalf("NewMultiScheduler failed: %v", err)
	}

	recorder := &mockSchedulerRecorder{}
	ms.WithRecorder(recorder)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := ms.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Verify Start emitted active monitors
	recorder.mu.Lock()
	var lastHTTP, lastTCP int
	for _, am := range recorder.activeMonitors {
		if am.kind == "http" {
			lastHTTP = am.count
		} else if am.kind == "tcp" {
			lastTCP = am.count
		}
	}
	recorder.mu.Unlock()

	if lastHTTP != 1 || lastTCP != 1 {
		t.Errorf("expected http=1, tcp=1 on start, got http=%d, tcp=%d", lastHTTP, lastTCP)
	}

	// Stop runner for mon-1
	if err := ms.StopMonitor(ctx, "mon-1"); err != nil {
		t.Fatalf("StopMonitor failed: %v", err)
	}

	recorder.mu.Lock()
	for _, am := range recorder.activeMonitors {
		if am.kind == "http" {
			lastHTTP = am.count
		} else if am.kind == "tcp" {
			lastTCP = am.count
		}
	}
	recorder.mu.Unlock()

	if lastHTTP != 0 || lastTCP != 1 {
		t.Errorf("expected http=0, tcp=1 after stopping mon-1, got http=%d, tcp=%d", lastHTTP, lastTCP)
	}

	// Stop scheduler entirely
	ms.Stop()
	ms.Wait()

	recorder.mu.Lock()
	for _, am := range recorder.activeMonitors {
		if am.kind == "http" {
			lastHTTP = am.count
		} else if am.kind == "tcp" {
			lastTCP = am.count
		}
	}
	recorder.mu.Unlock()

	if lastHTTP != 0 || lastTCP != 0 {
		t.Errorf("expected http=0, tcp=0 after scheduler stop, got http=%d, tcp=%d", lastHTTP, lastTCP)
	}
}
