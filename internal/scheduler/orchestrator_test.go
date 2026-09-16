package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chavaliadi/watchdog/internal/checker"
	"github.com/chavaliadi/watchdog/internal/monitor"
	"github.com/chavaliadi/watchdog/internal/persistence"
	"github.com/chavaliadi/watchdog/internal/retry"
	"github.com/chavaliadi/watchdog/internal/state"
)

type mockChecker struct {
	results []checker.CheckResult
	errs    []error
	callIdx int
	calls   int
}

func (m *mockChecker) Check(ctx context.Context, mon monitor.Monitor) (checker.CheckResult, error) {
	m.calls++
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

func testRetryConfig() retry.Config {
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

type saveCycleCall struct {
	monitorID   string
	checkResult checker.CheckResult
	nextState   state.State
}

type fakeRepository struct {
	getMonitorCalls    int
	getStateCalls      int
	createMonitorCalls int
	saveCycleCalls     []saveCycleCall

	saveCycleErr error
}

var _ persistence.Repository = (*fakeRepository)(nil)

func (f *fakeRepository) GetMonitor(ctx context.Context, id string) (monitor.Monitor, error) {
	f.getMonitorCalls++
	return monitor.Monitor{ID: id}, nil
}

func (f *fakeRepository) ListMonitors(ctx context.Context) ([]monitor.Monitor, error) {
	return nil, nil
}

func (f *fakeRepository) GetState(ctx context.Context, monitorID string) (state.State, error) {
	f.getStateCalls++
	return state.StateUnknown, nil
}

func (f *fakeRepository) CreateMonitor(ctx context.Context, m monitor.Monitor) error {
	f.createMonitorCalls++
	return nil
}

func (f *fakeRepository) UpdateMonitor(ctx context.Context, m monitor.Monitor) error {
	return nil
}

func (f *fakeRepository) DeleteMonitor(ctx context.Context, id string) error {
	return nil
}

func (f *fakeRepository) ListCheckResults(ctx context.Context, monitorID string, limit int) ([]checker.CheckResult, error) {
	return nil, nil
}

func (f *fakeRepository) GetStateWithTimestamp(ctx context.Context, monitorID string) (state.State, time.Time, error) {
	st, err := f.GetState(ctx, monitorID)
	return st, time.Now().UTC(), err
}

func (f *fakeRepository) SaveCycle(
	ctx context.Context,
	monitorID string,
	result checker.CheckResult,
	nextState state.State,
) error {
	f.saveCycleCalls = append(f.saveCycleCalls, saveCycleCall{
		monitorID:   monitorID,
		checkResult: result,
		nextState:   nextState,
	})
	return f.saveCycleErr
}

func TestOrchestrator_HTTPRouting(t *testing.T) {
	httpMock := &mockChecker{
		results: []checker.CheckResult{{OK: true, StatusCode: 200}},
		errs:    []error{nil},
	}
	tcpMock := &mockChecker{}

	o := NewOrchestrator(httpMock, tcpMock, testRetryConfig())

	m := monitor.Monitor{
		ID:        "mon-http",
		Kind:      monitor.KindHTTP,
		TargetURL: "http://example.com",
	}

	res, err := o.RunCycle(context.Background(), m, state.StateHealthy)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	if httpMock.calls != 1 {
		t.Errorf("expected 1 call to httpMock, got %d", httpMock.calls)
	}
	if tcpMock.calls != 0 {
		t.Errorf("expected 0 calls to tcpMock, got %d", tcpMock.calls)
	}
	if res.CheckResult.StatusCode != 200 {
		t.Errorf("expected status code 200, got %d", res.CheckResult.StatusCode)
	}
	if res.TransitionResult.NextState != state.StateHealthy {
		t.Errorf("expected next state HEALTHY, got %q", res.TransitionResult.NextState)
	}
	if res.TransitionResult.Transitioned {
		t.Errorf("expected Transitioned = false for HEALTHY -> HEALTHY")
	}
}

func TestOrchestrator_TCPRouting(t *testing.T) {
	httpMock := &mockChecker{}
	tcpMock := &mockChecker{
		results: []checker.CheckResult{{OK: true}},
		errs:    []error{nil},
	}

	o := NewOrchestrator(httpMock, tcpMock, testRetryConfig())

	m := monitor.Monitor{
		ID:        "mon-tcp",
		Kind:      monitor.KindTCP,
		TargetURL: "127.0.0.1:8080",
	}

	res, err := o.RunCycle(context.Background(), m, state.StateHealthy)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	if tcpMock.calls != 1 {
		t.Errorf("expected 1 call to tcpMock, got %d", tcpMock.calls)
	}
	if httpMock.calls != 0 {
		t.Errorf("expected 0 calls to httpMock, got %d", httpMock.calls)
	}
	if !res.CheckResult.OK {
		t.Errorf("expected check result OK = true")
	}
	if res.TransitionResult.NextState != state.StateHealthy {
		t.Errorf("expected next state HEALTHY, got %q", res.TransitionResult.NextState)
	}
}

func TestOrchestrator_TargetFailureReachesStateMachine(t *testing.T) {
	// All retries fail -> exhausted target failure
	httpMock := &mockChecker{
		results: []checker.CheckResult{
			{OK: false, ErrorClass: checker.ErrorClassStatus, StatusCode: 503},
			{OK: false, ErrorClass: checker.ErrorClassStatus, StatusCode: 503},
			{OK: false, ErrorClass: checker.ErrorClassStatus, StatusCode: 503},
		},
		errs: []error{nil, nil, nil},
	}
	tcpMock := &mockChecker{}

	o := NewOrchestrator(httpMock, tcpMock, testRetryConfig())

	m := monitor.Monitor{
		ID:        "mon-http-fail",
		Kind:      monitor.KindHTTP,
		TargetURL: "http://example.com",
	}

	res, err := o.RunCycle(context.Background(), m, state.StateHealthy)
	if err != nil {
		t.Fatalf("expected nil error for target failure observation, got: %v", err)
	}

	if res.CheckResult.OK {
		t.Errorf("expected check result OK = false")
	}
	if res.TransitionResult.CurrentState != state.StateHealthy {
		t.Errorf("expected CurrentState HEALTHY, got %q", res.TransitionResult.CurrentState)
	}
	if res.TransitionResult.NextState != state.StateUnhealthy {
		t.Errorf("expected NextState UNHEALTHY, got %q", res.TransitionResult.NextState)
	}
	if !res.TransitionResult.Transitioned {
		t.Errorf("expected Transitioned = true for HEALTHY -> UNHEALTHY")
	}
}

func TestOrchestrator_TargetRecovery(t *testing.T) {
	httpMock := &mockChecker{
		results: []checker.CheckResult{{OK: true, StatusCode: 200}},
		errs:    []error{nil},
	}
	tcpMock := &mockChecker{}

	o := NewOrchestrator(httpMock, tcpMock, testRetryConfig())

	m := monitor.Monitor{
		ID:        "mon-recover",
		Kind:      monitor.KindHTTP,
		TargetURL: "http://example.com",
	}

	res, err := o.RunCycle(context.Background(), m, state.StateUnhealthy)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	if res.TransitionResult.CurrentState != state.StateUnhealthy {
		t.Errorf("expected CurrentState UNHEALTHY, got %q", res.TransitionResult.CurrentState)
	}
	if res.TransitionResult.NextState != state.StateHealthy {
		t.Errorf("expected NextState HEALTHY, got %q", res.TransitionResult.NextState)
	}
	if !res.TransitionResult.Transitioned {
		t.Errorf("expected Transitioned = true for UNHEALTHY -> HEALTHY")
	}
}

func TestOrchestrator_SteadyHealthyState(t *testing.T) {
	httpMock := &mockChecker{
		results: []checker.CheckResult{{OK: true}},
		errs:    []error{nil},
	}
	tcpMock := &mockChecker{}

	o := NewOrchestrator(httpMock, tcpMock, testRetryConfig())

	m := monitor.Monitor{
		ID:        "mon-steady",
		Kind:      monitor.KindHTTP,
		TargetURL: "http://example.com",
	}

	res, err := o.RunCycle(context.Background(), m, state.StateHealthy)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	if res.TransitionResult.NextState != state.StateHealthy {
		t.Errorf("expected NextState HEALTHY, got %q", res.TransitionResult.NextState)
	}
	if res.TransitionResult.Transitioned {
		t.Errorf("expected Transitioned = false for steady state")
	}
}

func TestOrchestrator_UnsupportedMonitorKind(t *testing.T) {
	httpMock := &mockChecker{}
	tcpMock := &mockChecker{}

	o := NewOrchestrator(httpMock, tcpMock, testRetryConfig())

	tests := []struct {
		name string
		kind monitor.Kind
	}{
		{"health monitor kind unsupported in Phase 1", monitor.KindHealth},
		{"empty monitor kind", ""},
		{"unknown monitor kind", "grpc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := monitor.Monitor{
				ID:        "mon-invalid-kind",
				Kind:      tt.kind,
				TargetURL: "http://example.com",
			}

			_, err := o.RunCycle(context.Background(), m, state.StateHealthy)
			if err == nil {
				t.Errorf("expected error for unsupported kind %q, got nil", tt.kind)
			}
			if httpMock.calls != 0 {
				t.Errorf("expected 0 httpMock calls on unsupported kind, got %d", httpMock.calls)
			}
			if tcpMock.calls != 0 {
				t.Errorf("expected 0 tcpMock calls on unsupported kind, got %d", tcpMock.calls)
			}
		})
	}
}

func TestOrchestrator_CheckerGoErrorPropagatesWithoutStateTransition(t *testing.T) {
	configErr := errors.New("invalid checker target configuration")
	httpMock := &mockChecker{
		results: []checker.CheckResult{{}},
		errs:    []error{configErr},
	}
	tcpMock := &mockChecker{}

	o := NewOrchestrator(httpMock, tcpMock, testRetryConfig())

	m := monitor.Monitor{
		ID:        "mon-cfg-err",
		Kind:      monitor.KindHTTP,
		TargetURL: "http://example.com",
	}

	res, err := o.RunCycle(context.Background(), m, state.StateHealthy)
	if !errors.Is(err, configErr) {
		t.Fatalf("expected configErr, got: %v", err)
	}
	if res.TransitionResult.NextState != "" {
		t.Errorf("expected empty TransitionResult on Go error, got: %+v", res.TransitionResult)
	}
}

func TestOrchestrator_ContextCancellationPropagates(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Pre-canceled context

	httpMock := &mockChecker{
		results: []checker.CheckResult{{OK: true}},
		errs:    []error{nil},
	}
	tcpMock := &mockChecker{}

	o := NewOrchestrator(httpMock, tcpMock, testRetryConfig())

	m := monitor.Monitor{
		ID:        "mon-canceled",
		Kind:      monitor.KindHTTP,
		TargetURL: "http://example.com",
	}

	res, err := o.RunCycle(ctx, m, state.StateHealthy)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled error, got: %v", err)
	}
	if httpMock.calls != 0 {
		t.Errorf("expected 0 checker calls when context already canceled, got %d", httpMock.calls)
	}
	if res.TransitionResult.NextState != "" {
		t.Errorf("expected empty TransitionResult on canceled context, got: %+v", res.TransitionResult)
	}
}

func TestOrchestrator_NilContext(t *testing.T) {
	o := NewOrchestrator(nil, nil, testRetryConfig())
	m := monitor.Monitor{
		ID:   "mon-nil-ctx",
		Kind: monitor.KindHTTP,
	}

	_, err := o.RunCycle(nil, m, state.StateHealthy)
	if err == nil {
		t.Fatal("expected error for nil context, got nil")
	}
}

func TestOrchestrator_UninitializedOrchestrator(t *testing.T) {
	var o *Orchestrator
	m := monitor.Monitor{
		ID:   "mon-uninit",
		Kind: monitor.KindHTTP,
	}

	_, err := o.RunCycle(context.Background(), m, state.StateHealthy)
	if err == nil {
		t.Fatal("expected error for nil orchestrator, got nil")
	}
}

func TestOrchestrator_InvalidStateError(t *testing.T) {
	httpMock := &mockChecker{
		results: []checker.CheckResult{{OK: true}},
		errs:    []error{nil},
	}
	tcpMock := &mockChecker{}

	o := NewOrchestrator(httpMock, tcpMock, testRetryConfig())
	m := monitor.Monitor{
		ID:        "mon-inv-state",
		Kind:      monitor.KindHTTP,
		TargetURL: "http://example.com",
	}

	_, err := o.RunCycle(context.Background(), m, state.State("INVALID_STATE"))
	if err == nil {
		t.Fatal("expected error for invalid state, got nil")
	}
}

func TestOrchestrator_RetryAttemptsRespected(t *testing.T) {
	// 1 failure then 1 success -> should make exactly 2 attempts
	httpMock := &mockChecker{
		results: []checker.CheckResult{
			{OK: false, ErrorClass: checker.ErrorClassTimeout},
			{OK: true, StatusCode: 200},
		},
		errs: []error{nil, nil},
	}
	tcpMock := &mockChecker{}

	o := NewOrchestrator(httpMock, tcpMock, testRetryConfig())
	m := monitor.Monitor{
		ID:        "mon-retry-limit",
		Kind:      monitor.KindHTTP,
		TargetURL: "http://example.com",
	}

	res, err := o.RunCycle(context.Background(), m, state.StateHealthy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if httpMock.calls != 2 {
		t.Errorf("expected exactly 2 checker calls, got %d", httpMock.calls)
	}
	if res.CheckResult.AttemptCount != 2 {
		t.Errorf("expected AttemptCount = 2, got %d", res.CheckResult.AttemptCount)
	}
	if res.TransitionResult.NextState != state.StateHealthy {
		t.Errorf("expected NextState HEALTHY, got %q", res.TransitionResult.NextState)
	}
}

func TestOrchestrator_PersistentHTTPCycle_Success(t *testing.T) {
	httpMock := &mockChecker{
		results: []checker.CheckResult{{OK: true, StatusCode: 200, Latency: 45 * time.Millisecond}},
		errs:    []error{nil},
	}
	tcpMock := &mockChecker{}
	repo := &fakeRepository{}

	o := NewOrchestrator(httpMock, tcpMock, testRetryConfig(), repo)

	m := monitor.Monitor{
		ID:        "mon-http-persist",
		Kind:      monitor.KindHTTP,
		TargetURL: "https://example.com",
	}

	res, err := o.RunCycle(context.Background(), m, state.StateUnknown)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	if httpMock.calls != 1 {
		t.Errorf("expected 1 HTTP checker call, got %d", httpMock.calls)
	}
	if res.TransitionResult.NextState != state.StateHealthy {
		t.Errorf("expected next state HEALTHY, got %q", res.TransitionResult.NextState)
	}

	if len(repo.saveCycleCalls) != 1 {
		t.Fatalf("expected 1 SaveCycle call, got %d", len(repo.saveCycleCalls))
	}
	call := repo.saveCycleCalls[0]
	if call.monitorID != m.ID {
		t.Errorf("expected monitor ID %q, got %q", m.ID, call.monitorID)
	}
	if !call.checkResult.OK || call.checkResult.StatusCode != 200 {
		t.Errorf("expected CheckResult OK=true StatusCode=200, got OK=%v StatusCode=%d", call.checkResult.OK, call.checkResult.StatusCode)
	}
	if call.nextState != state.StateHealthy {
		t.Errorf("expected SaveCycle nextState HEALTHY, got %q", call.nextState)
	}
}

func TestOrchestrator_PersistentHTTPCycle_Failure(t *testing.T) {
	httpMock := &mockChecker{
		results: []checker.CheckResult{
			{OK: false, StatusCode: 500, ErrorClass: checker.ErrorClassStatus},
			{OK: false, StatusCode: 500, ErrorClass: checker.ErrorClassStatus},
			{OK: false, StatusCode: 500, ErrorClass: checker.ErrorClassStatus},
		},
		errs: []error{nil, nil, nil},
	}
	tcpMock := &mockChecker{}
	repo := &fakeRepository{}

	o := NewOrchestrator(httpMock, tcpMock, testRetryConfig(), repo)

	m := monitor.Monitor{
		ID:        "mon-http-fail",
		Kind:      monitor.KindHTTP,
		TargetURL: "https://example.com/failing",
	}

	res, err := o.RunCycle(context.Background(), m, state.StateHealthy)
	if err != nil {
		t.Fatalf("expected nil error for target failure, got: %v", err)
	}

	if res.CheckResult.OK {
		t.Errorf("expected CheckResult.OK to be false")
	}
	if res.TransitionResult.NextState != state.StateUnhealthy {
		t.Errorf("expected transition to UNHEALTHY, got %q", res.TransitionResult.NextState)
	}

	if len(repo.saveCycleCalls) != 1 {
		t.Fatalf("expected 1 SaveCycle call, got %d", len(repo.saveCycleCalls))
	}
	call := repo.saveCycleCalls[0]
	if call.monitorID != m.ID {
		t.Errorf("expected monitor ID %q, got %q", m.ID, call.monitorID)
	}
	if call.nextState != state.StateUnhealthy {
		t.Errorf("expected SaveCycle with UNHEALTHY, got %q", call.nextState)
	}
}

func TestOrchestrator_PersistentHTTPCycle_Recovery(t *testing.T) {
	httpMock := &mockChecker{
		results: []checker.CheckResult{{OK: true, StatusCode: 200}},
		errs:    []error{nil},
	}
	tcpMock := &mockChecker{}
	repo := &fakeRepository{}

	o := NewOrchestrator(httpMock, tcpMock, testRetryConfig(), repo)

	m := monitor.Monitor{
		ID:        "mon-http-recover",
		Kind:      monitor.KindHTTP,
		TargetURL: "https://example.com/recovered",
	}

	res, err := o.RunCycle(context.Background(), m, state.StateUnhealthy)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	if res.TransitionResult.NextState != state.StateHealthy {
		t.Errorf("expected next state HEALTHY, got %q", res.TransitionResult.NextState)
	}

	if len(repo.saveCycleCalls) != 1 {
		t.Fatalf("expected 1 SaveCycle call, got %d", len(repo.saveCycleCalls))
	}
	if repo.saveCycleCalls[0].nextState != state.StateHealthy {
		t.Errorf("expected SaveCycle with nextState HEALTHY, got %q", repo.saveCycleCalls[0].nextState)
	}
}

func TestOrchestrator_PersistentTCPCycle_Success(t *testing.T) {
	httpMock := &mockChecker{}
	tcpMock := &mockChecker{
		results: []checker.CheckResult{{OK: true, Latency: 15 * time.Millisecond}},
		errs:    []error{nil},
	}
	repo := &fakeRepository{}

	o := NewOrchestrator(httpMock, tcpMock, testRetryConfig(), repo)

	m := monitor.Monitor{
		ID:        "mon-tcp-persist",
		Kind:      monitor.KindTCP,
		TargetURL: "127.0.0.1:5432",
	}

	res, err := o.RunCycle(context.Background(), m, state.StateUnknown)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	if tcpMock.calls != 1 {
		t.Errorf("expected 1 TCP checker call, got %d", tcpMock.calls)
	}
	if res.TransitionResult.NextState != state.StateHealthy {
		t.Errorf("expected next state HEALTHY, got %q", res.TransitionResult.NextState)
	}

	if len(repo.saveCycleCalls) != 1 {
		t.Fatalf("expected 1 SaveCycle call, got %d", len(repo.saveCycleCalls))
	}
	call := repo.saveCycleCalls[0]
	if call.monitorID != m.ID {
		t.Errorf("expected monitor ID %q, got %q", m.ID, call.monitorID)
	}
	if call.nextState != state.StateHealthy {
		t.Errorf("expected SaveCycle with HEALTHY, got %q", call.nextState)
	}
}

func TestOrchestrator_PersistentCycle_CheckerExecutionError(t *testing.T) {
	execErr := errors.New("network dial failed")
	httpMock := &mockChecker{
		results: []checker.CheckResult{{}},
		errs:    []error{execErr},
	}
	tcpMock := &mockChecker{}
	repo := &fakeRepository{}

	o := NewOrchestrator(httpMock, tcpMock, testRetryConfig(), repo)

	m := monitor.Monitor{
		ID:        "mon-checker-err",
		Kind:      monitor.KindHTTP,
		TargetURL: "https://invalid.example.com",
	}

	_, err := o.RunCycle(context.Background(), m, state.StateHealthy)
	if !errors.Is(err, execErr) {
		t.Fatalf("expected execErr, got: %v", err)
	}

	if len(repo.saveCycleCalls) != 0 {
		t.Errorf("expected 0 SaveCycle calls when checker returns Go error, got %d", len(repo.saveCycleCalls))
	}
}

func TestOrchestrator_PersistentCycle_PersistenceError(t *testing.T) {
	httpMock := &mockChecker{
		results: []checker.CheckResult{{OK: true, StatusCode: 200}},
		errs:    []error{nil},
	}
	tcpMock := &mockChecker{}

	dbErr := errors.New("database connection broken")
	repo := &fakeRepository{
		saveCycleErr: dbErr,
	}

	o := NewOrchestrator(httpMock, tcpMock, testRetryConfig(), repo)

	m := monitor.Monitor{
		ID:        "mon-persist-err",
		Kind:      monitor.KindHTTP,
		TargetURL: "https://example.com",
	}

	res, err := o.RunCycle(context.Background(), m, state.StateUnknown)
	if err == nil {
		t.Fatal("expected error on persistence failure, got nil")
	}
	if !errors.Is(err, dbErr) {
		t.Fatalf("expected error wrapping dbErr, got: %v", err)
	}

	// Must NOT return successful CycleResult
	if res.CheckResult.OK {
		t.Errorf("expected empty/failed CycleResult, got %+v", res)
	}

	// Checker must have run
	if httpMock.calls != 1 {
		t.Errorf("expected 1 checker call, got %d", httpMock.calls)
	}

	// SaveCycle was called exactly once (not retried)
	if len(repo.saveCycleCalls) != 1 {
		t.Errorf("expected exactly 1 SaveCycle call without retry, got %d", len(repo.saveCycleCalls))
	}
}

func TestOrchestrator_PersistentCycle_UnsupportedMonitorKind(t *testing.T) {
	httpMock := &mockChecker{}
	tcpMock := &mockChecker{}
	repo := &fakeRepository{}

	o := NewOrchestrator(httpMock, tcpMock, testRetryConfig(), repo)

	m := monitor.Monitor{
		ID:        "mon-unsupported",
		Kind:      monitor.KindHealth,
		TargetURL: "https://example.com",
	}

	_, err := o.RunCycle(context.Background(), m, state.StateHealthy)
	if err == nil {
		t.Fatal("expected error for unsupported monitor kind, got nil")
	}

	if httpMock.calls != 0 {
		t.Errorf("expected 0 HTTP checker calls, got %d", httpMock.calls)
	}
	if tcpMock.calls != 0 {
		t.Errorf("expected 0 TCP checker calls, got %d", tcpMock.calls)
	}
	if len(repo.saveCycleCalls) != 0 {
		t.Errorf("expected 0 SaveCycle calls, got %d", len(repo.saveCycleCalls))
	}
}

func TestOrchestrator_PersistentCycle_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel before RunCycle

	httpMock := &mockChecker{
		results: []checker.CheckResult{{OK: true}},
		errs:    []error{nil},
	}
	tcpMock := &mockChecker{}
	repo := &fakeRepository{}

	o := NewOrchestrator(httpMock, tcpMock, testRetryConfig(), repo)

	m := monitor.Monitor{
		ID:        "mon-canceled-persist",
		Kind:      monitor.KindHTTP,
		TargetURL: "https://example.com",
	}

	_, err := o.RunCycle(ctx, m, state.StateHealthy)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}

	if httpMock.calls != 0 {
		t.Errorf("expected 0 checker calls on canceled context, got %d", httpMock.calls)
	}
	if len(repo.saveCycleCalls) != 0 {
		t.Errorf("expected 0 SaveCycle calls on canceled context, got %d", len(repo.saveCycleCalls))
	}
}
