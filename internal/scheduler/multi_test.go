package scheduler_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chavaliadi/watchdog/internal/checker"
	"github.com/chavaliadi/watchdog/internal/monitor"
	"github.com/chavaliadi/watchdog/internal/persistence"
	"github.com/chavaliadi/watchdog/internal/scheduler"
	"github.com/chavaliadi/watchdog/internal/state"
	"github.com/chavaliadi/watchdog/internal/worker"
)

// mockMultiRepo implements persistence.Repository for MultiScheduler tests.
type mockMultiRepo struct {
	mu           sync.RWMutex
	monitors     []monitor.Monitor
	states       map[string]state.State
	listErr      error
	getStateErrs map[string]error
}

var _ persistence.Repository = (*mockMultiRepo)(nil)

func newMockMultiRepo() *mockMultiRepo {
	return &mockMultiRepo{
		monitors:     make([]monitor.Monitor, 0),
		states:       make(map[string]state.State),
		getStateErrs: make(map[string]error),
	}
}

func (m *mockMultiRepo) addMonitor(mon monitor.Monitor, st state.State) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.monitors = append(m.monitors, mon)
	m.states[mon.ID] = st
}

func (m *mockMultiRepo) GetMonitor(ctx context.Context, id string) (monitor.Monitor, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, mon := range m.monitors {
		if mon.ID == id {
			return mon, nil
		}
	}
	return monitor.Monitor{}, errors.New("not found")
}

func (m *mockMultiRepo) ListMonitors(ctx context.Context) ([]monitor.Monitor, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.listErr != nil {
		return nil, m.listErr
	}
	res := make([]monitor.Monitor, len(m.monitors))
	copy(res, m.monitors)
	return res, nil
}

func (m *mockMultiRepo) GetState(ctx context.Context, monitorID string) (state.State, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if err, ok := m.getStateErrs[monitorID]; ok {
		return "", err
	}
	if st, ok := m.states[monitorID]; ok {
		return st, nil
	}
	return state.StateUnknown, nil
}

func (m *mockMultiRepo) CreateMonitor(ctx context.Context, mon monitor.Monitor) error {
	m.addMonitor(mon, state.StateUnknown)
	return nil
}

func (m *mockMultiRepo) GetStateWithTimestamp(ctx context.Context, monitorID string) (state.State, time.Time, error) {
	st, err := m.GetState(ctx, monitorID)
	return st, time.Now().UTC(), err
}

func (m *mockMultiRepo) UpdateMonitor(ctx context.Context, mon monitor.Monitor) error {
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

func (m *mockMultiRepo) DeleteMonitor(ctx context.Context, id string) error {
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

func (m *mockMultiRepo) ListCheckResults(ctx context.Context, monitorID string, limit int) ([]checker.CheckResult, error) {
	return []checker.CheckResult{}, nil
}

func (m *mockMultiRepo) SaveCycle(ctx context.Context, monitorID string, result checker.CheckResult, nextState state.State) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states[monitorID] = nextState
	return nil
}

// mockCycleRunner implements scheduler.CycleRunner for controlled worker executions.
type mockCycleRunner struct {
	mu       sync.Mutex
	cycleFn  func(ctx context.Context, m monitor.Monitor, current state.State) (scheduler.CycleResult, error)
	calls    map[string][]state.State
	totalRun int64
}

func newMockCycleRunner() *mockCycleRunner {
	return &mockCycleRunner{
		calls: make(map[string][]state.State),
	}
}

func (r *mockCycleRunner) RunCycle(ctx context.Context, m monitor.Monitor, current state.State) (scheduler.CycleResult, error) {
	atomic.AddInt64(&r.totalRun, 1)

	r.mu.Lock()
	r.calls[m.ID] = append(r.calls[m.ID], current)
	fn := r.cycleFn
	r.mu.Unlock()

	if fn != nil {
		return fn(ctx, m, current)
	}

	return scheduler.CycleResult{
		Monitor: m,
		CheckResult: checker.CheckResult{
			OK:           true,
			StatusCode:   200,
			AttemptCount: 1,
		},
		TransitionResult: state.TransitionResult{
			CurrentState: current,
			NextState:    state.StateHealthy,
			Transitioned: current != state.StateHealthy,
		},
	}, nil
}

func (r *mockCycleRunner) getCalls(monitorID string) []state.State {
	r.mu.Lock()
	defer r.mu.Unlock()
	copied := make([]state.State, len(r.calls[monitorID]))
	copy(copied, r.calls[monitorID])
	return copied
}

func TestMultiScheduler_Construction(t *testing.T) {
	repo := newMockMultiRepo()
	runner := newMockCycleRunner()
	pool, err := worker.NewPool(worker.Config{MaxConcurrency: 2}, runner)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}

	t.Run("Nil repository returns error", func(t *testing.T) {
		_, err := scheduler.NewMultiScheduler(nil, pool)
		if err == nil {
			t.Fatal("expected error for nil repo, got nil")
		}
	})

	t.Run("Nil worker pool returns error", func(t *testing.T) {
		_, err := scheduler.NewMultiScheduler(repo, nil)
		if err == nil {
			t.Fatal("expected error for nil pool, got nil")
		}
	})

	t.Run("Valid parameters constructs MultiScheduler", func(t *testing.T) {
		ms, err := scheduler.NewMultiScheduler(repo, pool)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ms == nil {
			t.Fatal("expected non-nil MultiScheduler")
		}
	})
}

func TestMultiScheduler_DiscoveryAndFiltering(t *testing.T) {
	runner := newMockCycleRunner()
	pool, err := worker.NewPool(worker.Config{MaxConcurrency: 2}, runner)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)
	defer func() {
		pool.Stop()
		pool.Wait()
	}()

	t.Run("Zero monitors runs cleanly and exits", func(t *testing.T) {
		repo := newMockMultiRepo()
		ms, err := scheduler.NewMultiScheduler(repo, pool)
		if err != nil {
			t.Fatalf("NewMultiScheduler: %v", err)
		}

		sCtx, sCancel := context.WithCancel(context.Background())
		if err := ms.Start(sCtx); err != nil {
			t.Fatalf("Start failed: %v", err)
		}

		if ms.ActiveRunners() != 0 {
			t.Errorf("expected 0 active runners, got %d", ms.ActiveRunners())
		}

		sCancel()
		ms.Wait()
	})

	t.Run("Disabled monitors are filtered and never execute", func(t *testing.T) {
		repo := newMockMultiRepo()
		mEnabled := monitor.Monitor{
			ID:       "mon-enabled",
			Name:     "Enabled",
			Enabled:  true,
			Interval: 50 * time.Millisecond,
		}
		mDisabled := monitor.Monitor{
			ID:       "mon-disabled",
			Name:     "Disabled",
			Enabled:  false,
			Interval: 10 * time.Millisecond,
		}
		repo.addMonitor(mEnabled, state.StateUnknown)
		repo.addMonitor(mDisabled, state.StateUnknown)

		ms, err := scheduler.NewMultiScheduler(repo, pool)
		if err != nil {
			t.Fatalf("NewMultiScheduler: %v", err)
		}

		sCtx, sCancel := context.WithCancel(context.Background())
		defer sCancel()

		if err := ms.Start(sCtx); err != nil {
			t.Fatalf("Start failed: %v", err)
		}

		if ms.ActiveRunners() != 1 {
			t.Errorf("expected 1 active runner, got %d", ms.ActiveRunners())
		}

		// Allow enabled monitor to run briefly
		time.Sleep(30 * time.Millisecond)

		sCancel()
		ms.Wait()

		if len(runner.getCalls("mon-enabled")) == 0 {
			t.Error("expected mon-enabled to execute at least once")
		}
		if len(runner.getCalls("mon-disabled")) != 0 {
			t.Errorf("expected mon-disabled to NEVER execute, got %d calls", len(runner.getCalls("mon-disabled")))
		}
	})

	t.Run("Database error on monitor listing propagates", func(t *testing.T) {
		repo := newMockMultiRepo()
		expectedErr := errors.New("db connection lost")
		repo.listErr = expectedErr

		ms, err := scheduler.NewMultiScheduler(repo, pool)
		if err != nil {
			t.Fatalf("NewMultiScheduler: %v", err)
		}

		err = ms.Start(context.Background())
		if err == nil {
			t.Fatal("expected error on list failure, got nil")
		}
		if !errors.Is(err, expectedErr) {
			t.Errorf("expected error wrapping %v, got %v", expectedErr, err)
		}
	})

	t.Run("Database error on state loading propagates and halts startup", func(t *testing.T) {
		repo := newMockMultiRepo()
		m := monitor.Monitor{
			ID:       "mon-broken-state",
			Enabled:  true,
			Interval: 50 * time.Millisecond,
		}
		repo.addMonitor(m, state.StateUnknown)
		expectedErr := errors.New("corrupt state table")
		repo.getStateErrs["mon-broken-state"] = expectedErr

		ms, err := scheduler.NewMultiScheduler(repo, pool)
		if err != nil {
			t.Fatalf("NewMultiScheduler: %v", err)
		}

		err = ms.Start(context.Background())
		if err == nil {
			t.Fatal("expected error on state loading failure, got nil")
		}
		if !errors.Is(err, expectedErr) {
			t.Errorf("expected error wrapping %v, got %v", expectedErr, err)
		}
	})
}

func TestMultiScheduler_SchedulingSemantics(t *testing.T) {
	t.Run("Immediate initial execution for all enabled monitors", func(t *testing.T) {
		runner := newMockCycleRunner()
		pool, err := worker.NewPool(worker.Config{MaxConcurrency: 4}, runner)
		if err != nil {
			t.Fatalf("create pool: %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		pool.Start(ctx)
		defer func() {
			pool.Stop()
			pool.Wait()
		}()

		repo := newMockMultiRepo()
		mA := monitor.Monitor{ID: "mon-init-A", Enabled: true, Interval: 1 * time.Hour}
		mB := monitor.Monitor{ID: "mon-init-B", Enabled: true, Interval: 1 * time.Hour}
		repo.addMonitor(mA, state.StateUnknown)
		repo.addMonitor(mB, state.StateUnknown)

		ms, err := scheduler.NewMultiScheduler(repo, pool)
		if err != nil {
			t.Fatalf("NewMultiScheduler: %v", err)
		}

		// Initial cycle should execute immediately even though interval is 1 hour
		if err := ms.Start(ctx); err != nil {
			t.Fatalf("Start failed: %v", err)
		}

		// Wait briefly for both immediate cycles to execute
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if len(runner.getCalls("mon-init-A")) >= 1 && len(runner.getCalls("mon-init-B")) >= 1 {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}

		if len(runner.getCalls("mon-init-A")) < 1 {
			t.Error("expected immediate initial cycle for mon-init-A")
		}
		if len(runner.getCalls("mon-init-B")) < 1 {
			t.Error("expected immediate initial cycle for mon-init-B")
		}

		ms.Stop()
		ms.Wait()
	})

	t.Run("Independent intervals execute at their own cadence", func(t *testing.T) {
		runner := newMockCycleRunner()
		pool, err := worker.NewPool(worker.Config{MaxConcurrency: 4}, runner)
		if err != nil {
			t.Fatalf("create pool: %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		pool.Start(ctx)
		defer func() {
			pool.Stop()
			pool.Wait()
		}()

		repo := newMockMultiRepo()
		// Fast monitor: 15ms
		mFast := monitor.Monitor{ID: "mon-fast", Enabled: true, Interval: 15 * time.Millisecond}
		// Slow monitor: 150ms
		mSlow := monitor.Monitor{ID: "mon-slow", Enabled: true, Interval: 150 * time.Millisecond}
		repo.addMonitor(mFast, state.StateUnknown)
		repo.addMonitor(mSlow, state.StateUnknown)

		ms, err := scheduler.NewMultiScheduler(repo, pool)
		if err != nil {
			t.Fatalf("NewMultiScheduler: %v", err)
		}

		if err := ms.Start(ctx); err != nil {
			t.Fatalf("Start failed: %v", err)
		}

		// Wait until fast monitor runs at least 3 times
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if len(runner.getCalls("mon-fast")) >= 3 {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}

		fastCalls := len(runner.getCalls("mon-fast"))
		slowCalls := len(runner.getCalls("mon-slow"))

		if fastCalls < 3 {
			t.Errorf("expected fast monitor to execute >= 3 times, got %d", fastCalls)
		}
		if slowCalls >= fastCalls {
			t.Errorf("expected fast calls (%d) > slow calls (%d)", fastCalls, slowCalls)
		}

		ms.Stop()
		ms.Wait()
	})

	t.Run("Slow monitor does not block unrelated monitor scheduling", func(t *testing.T) {
		// Deterministic synchronization: Mon A starts long cycle and blocks.
		// Mon B (fast) must be able to start and finish while Mon A is still in-flight!
		aStarted := make(chan struct{})
		aProceed := make(chan struct{})
		bStarted := make(chan struct{})

		runner := newMockCycleRunner()
		runner.cycleFn = func(ctx context.Context, m monitor.Monitor, current state.State) (scheduler.CycleResult, error) {
			if m.ID == "mon-blocker" {
				close(aStarted)
				select {
				case <-ctx.Done():
					return scheduler.CycleResult{}, ctx.Err()
				case <-aProceed:
				}
			} else if m.ID == "mon-independent" {
				select {
				case <-bStarted:
				default:
					close(bStarted)
				}
			}

			return scheduler.CycleResult{
				Monitor: m,
				TransitionResult: state.TransitionResult{
					CurrentState: current,
					NextState:    state.StateHealthy,
				},
			}, nil
		}

		pool, err := worker.NewPool(worker.Config{MaxConcurrency: 2}, runner)
		if err != nil {
			t.Fatalf("create pool: %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		pool.Start(ctx)
		defer func() {
			pool.Stop()
			pool.Wait()
		}()

		repo := newMockMultiRepo()
		mBlocker := monitor.Monitor{ID: "mon-blocker", Enabled: true, Interval: 10 * time.Millisecond}
		mIndep := monitor.Monitor{ID: "mon-independent", Enabled: true, Interval: 10 * time.Millisecond}
		repo.addMonitor(mBlocker, state.StateUnknown)
		repo.addMonitor(mIndep, state.StateUnknown)

		ms, err := scheduler.NewMultiScheduler(repo, pool)
		if err != nil {
			t.Fatalf("NewMultiScheduler: %v", err)
		}

		if err := ms.Start(ctx); err != nil {
			t.Fatalf("Start failed: %v", err)
		}

		// Wait until mon-blocker is actively executing and blocked
		select {
		case <-aStarted:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for mon-blocker to start")
		}

		// Verify mon-independent starts and completes WHILE mon-blocker is still blocked!
		select {
		case <-bStarted:
			// SUCCESS: independent monitor ran concurrently while blocker was blocked
		case <-time.After(2 * time.Second):
			t.Fatal("INDEPENDENT MONITOR BLOCKED: mon-independent failed to execute while mon-blocker was slow!")
		}

		// Release mon-blocker
		close(aProceed)

		ms.Stop()
		ms.Wait()
	})
}

func TestMultiScheduler_StateProgressionAndOwnership(t *testing.T) {
	// Proves:
	// 1. Initial state is passed to Cycle 1 (UNKNOWN)
	// 2. Successful Cycle 1 returns HEALTHY -> Cycle 2 receives HEALTHY
	// 3. Operational error on Cycle 2 -> Cycle 3 still receives HEALTHY (preserved)
	var callCount int64

	runner := newMockCycleRunner()
	runner.cycleFn = func(ctx context.Context, m monitor.Monitor, current state.State) (scheduler.CycleResult, error) {
		count := atomic.AddInt64(&callCount, 1)

		switch count {
		case 1:
			// Cycle 1: UNKNOWN -> HEALTHY
			return scheduler.CycleResult{
				Monitor: m,
				TransitionResult: state.TransitionResult{
					CurrentState: current,
					NextState:    state.StateHealthy,
					Transitioned: true,
				},
			}, nil
		case 2:
			// Cycle 2: Operational error (db failure)
			return scheduler.CycleResult{}, errors.New("simulated operational db error")
		case 3:
			// Cycle 3: Should receive preserved HEALTHY state! Transition to UNHEALTHY
			return scheduler.CycleResult{
				Monitor: m,
				TransitionResult: state.TransitionResult{
					CurrentState: current,
					NextState:    state.StateUnhealthy,
					Transitioned: true,
				},
			}, nil
		default:
			return scheduler.CycleResult{
				Monitor: m,
				TransitionResult: state.TransitionResult{
					CurrentState: current,
					NextState:    current,
				},
			}, nil
		}
	}

	pool, err := worker.NewPool(worker.Config{MaxConcurrency: 2}, runner)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)
	defer func() {
		pool.Stop()
		pool.Wait()
	}()

	repo := newMockMultiRepo()
	m := monitor.Monitor{ID: "mon-state-prog", Enabled: true, Interval: 15 * time.Millisecond}
	repo.addMonitor(m, state.StateUnknown)

	var reportedErrors []error
	var errorsMu sync.Mutex

	ms, err := scheduler.NewMultiScheduler(repo, pool)
	if err != nil {
		t.Fatalf("NewMultiScheduler: %v", err)
	}
	ms.WithErrorHandler(func(monitorID string, err error) {
		errorsMu.Lock()
		defer errorsMu.Unlock()
		reportedErrors = append(reportedErrors, err)
	})

	if err := ms.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Wait for at least 3 cycles
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt64(&callCount) >= 3 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	ms.Stop()
	ms.Wait()

	calls := runner.getCalls("mon-state-prog")
	if len(calls) < 3 {
		t.Fatalf("expected at least 3 calls, got %d", len(calls))
	}

	// Verify state progression:
	// Call 1: received UNKNOWN
	if calls[0] != state.StateUnknown {
		t.Errorf("call 1 state want UNKNOWN, got %v", calls[0])
	}
	// Call 2: received HEALTHY (from Call 1 NextState)
	if calls[1] != state.StateHealthy {
		t.Errorf("call 2 state want HEALTHY, got %v", calls[1])
	}
	// Call 3: received HEALTHY (Call 2 errored, so state must be preserved!)
	if calls[2] != state.StateHealthy {
		t.Errorf("call 3 state want HEALTHY (preserved), got %v", calls[2])
	}

	// Verify error was reported to error handler
	errorsMu.Lock()
	errCount := len(reportedErrors)
	errorsMu.Unlock()
	if errCount < 1 {
		t.Errorf("expected error handler to receive operational error, got %d errors", errCount)
	}
}

func TestMultiScheduler_GlobalConcurrencyBounded(t *testing.T) {
	const maxConcurrency = 2
	const totalMonitors = 6

	var (
		currentActive int64
		peakActive    int64
	)

	runner := newMockCycleRunner()
	runner.cycleFn = func(ctx context.Context, m monitor.Monitor, current state.State) (scheduler.CycleResult, error) {
		active := atomic.AddInt64(&currentActive, 1)

		for {
			peak := atomic.LoadInt64(&peakActive)
			if active <= peak || atomic.CompareAndSwapInt64(&peakActive, peak, active) {
				break
			}
		}

		select {
		case <-ctx.Done():
		case <-time.After(30 * time.Millisecond):
		}

		atomic.AddInt64(&currentActive, -1)
		return scheduler.CycleResult{
			Monitor: m,
			TransitionResult: state.TransitionResult{
				CurrentState: current,
				NextState:    state.StateHealthy,
			},
		}, nil
	}

	pool, err := worker.NewPool(worker.Config{MaxConcurrency: maxConcurrency}, runner)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)
	defer func() {
		pool.Stop()
		pool.Wait()
	}()

	repo := newMockMultiRepo()
	for i := 0; i < totalMonitors; i++ {
		m := monitor.Monitor{
			ID:       fmt.Sprintf("mon-conc-%d", i),
			Enabled:  true,
			Interval: 20 * time.Millisecond,
		}
		repo.addMonitor(m, state.StateUnknown)
	}

	ms, err := scheduler.NewMultiScheduler(repo, pool)
	if err != nil {
		t.Fatalf("NewMultiScheduler: %v", err)
	}

	if err := ms.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Let all monitors run multiple concurrent cycles
	time.Sleep(150 * time.Millisecond)

	ms.Stop()
	ms.Wait()

	peak := atomic.LoadInt64(&peakActive)
	if peak > maxConcurrency {
		t.Errorf("CONCURRENCY LIMIT EXCEEDED: peak active was %d, configured limit was %d", peak, maxConcurrency)
	}
	if peak <= 0 {
		t.Errorf("invalid peak active: %d", peak)
	}
}

func TestMultiScheduler_CancellationAndShutdown(t *testing.T) {
	t.Run("Clean shutdown terminates all runners and workers", func(t *testing.T) {
		runner := newMockCycleRunner()
		pool, err := worker.NewPool(worker.Config{MaxConcurrency: 4}, runner)
		if err != nil {
			t.Fatalf("create pool: %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		pool.Start(ctx)

		repo := newMockMultiRepo()
		for i := 0; i < 5; i++ {
			repo.addMonitor(monitor.Monitor{
				ID:       fmt.Sprintf("mon-shut-%d", i),
				Enabled:  true,
				Interval: 10 * time.Millisecond,
			}, state.StateUnknown)
		}

		ms, err := scheduler.NewMultiScheduler(repo, pool)
		if err != nil {
			t.Fatalf("NewMultiScheduler: %v", err)
		}

		if err := ms.Start(ctx); err != nil {
			t.Fatalf("Start failed: %v", err)
		}

		// Allow cycles to begin
		time.Sleep(30 * time.Millisecond)

		// Cancel root context
		cancel()

		done := make(chan struct{})
		go func() {
			ms.Wait()
			pool.Stop()
			pool.Wait()
			close(done)
		}()

		select {
		case <-done:
			// Clean shutdown
		case <-time.After(3 * time.Second):
			t.Fatal("shutdown deadlocked or took too long to complete")
		}
	})

	t.Run("Active check receives cancellation promptly", func(t *testing.T) {
		checkStarted := make(chan struct{})
		runner := newMockCycleRunner()
		runner.cycleFn = func(ctx context.Context, m monitor.Monitor, current state.State) (scheduler.CycleResult, error) {
			close(checkStarted)
			<-ctx.Done()
			return scheduler.CycleResult{}, ctx.Err()
		}

		pool, err := worker.NewPool(worker.Config{MaxConcurrency: 1}, runner)
		if err != nil {
			t.Fatalf("create pool: %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		pool.Start(ctx)

		repo := newMockMultiRepo()
		repo.addMonitor(monitor.Monitor{
			ID:       "mon-active-cancel",
			Enabled:  true,
			Interval: 1 * time.Hour,
		}, state.StateUnknown)

		ms, err := scheduler.NewMultiScheduler(repo, pool)
		if err != nil {
			t.Fatalf("NewMultiScheduler: %v", err)
		}

		if err := ms.Start(ctx); err != nil {
			t.Fatalf("Start failed: %v", err)
		}

		<-checkStarted
		cancel() // Cancel while check is active

		done := make(chan struct{})
		go func() {
			ms.Wait()
			pool.Stop()
			pool.Wait()
			close(done)
		}()

		select {
		case <-done:
			// Clean exit
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for cancelled active check to abort")
		}
	})
}

func TestMultiScheduler_RuntimeReconciliation(t *testing.T) {
	t.Run("dynamic StartMonitor and StopMonitor", func(t *testing.T) {
		repo := newMockMultiRepo()
		runner := newMockCycleRunner()
		ms, err := scheduler.NewMultiScheduler(repo, runner)
		if err != nil {
			t.Fatalf("NewMultiScheduler failed: %v", err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if err := ms.Start(ctx); err != nil {
			t.Fatalf("Start failed: %v", err)
		}
		defer ms.Stop()

		m := monitor.Monitor{
			ID:       "dyn-1",
			Enabled:  true,
			Interval: 1 * time.Hour,
		}
		repo.addMonitor(m, state.StateHealthy)

		if err := ms.StartMonitor(ctx, m); err != nil {
			t.Fatalf("StartMonitor failed: %v", err)
		}

		if !ms.HasRunner("dyn-1") {
			t.Fatal("expected HasRunner('dyn-1') to be true")
		}
		if count := ms.ActiveRunners(); count != 1 {
			t.Fatalf("expected 1 active runner, got %d", count)
		}

		// Immediate first execution should have run
		time.Sleep(20 * time.Millisecond)
		runner.mu.Lock()
		calls := len(runner.calls["dyn-1"])
		runner.mu.Unlock()
		if calls < 1 {
			t.Fatalf("expected at least 1 cycle run, got %d", calls)
		}

		// Starting duplicate runner returns ErrRunnerAlreadyExists
		if err := ms.StartMonitor(ctx, m); !errors.Is(err, scheduler.ErrRunnerAlreadyExists) {
			t.Fatalf("expected ErrRunnerAlreadyExists, got %v", err)
		}

		// Stop runner synchronously
		if err := ms.StopMonitor(ctx, "dyn-1"); err != nil {
			t.Fatalf("StopMonitor failed: %v", err)
		}
		if ms.HasRunner("dyn-1") {
			t.Fatal("expected HasRunner('dyn-1') to be false after stop")
		}
		if count := ms.ActiveRunners(); count != 0 {
			t.Fatalf("expected 0 active runners, got %d", count)
		}
	})

	t.Run("dynamic UpdateMonitor config change and interval change", func(t *testing.T) {
		repo := newMockMultiRepo()
		var cycleCount int64
		cycleTriggered := make(chan struct{}, 10)

		runner := newMockCycleRunner()
		runner.cycleFn = func(ctx context.Context, m monitor.Monitor, current state.State) (scheduler.CycleResult, error) {
			atomic.AddInt64(&cycleCount, 1)
			select {
			case cycleTriggered <- struct{}{}:
			default:
			}
			return scheduler.CycleResult{
				TransitionResult: state.TransitionResult{NextState: state.StateHealthy},
			}, nil
		}

		ms, err := scheduler.NewMultiScheduler(repo, runner)
		if err != nil {
			t.Fatalf("NewMultiScheduler failed: %v", err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if err := ms.Start(ctx); err != nil {
			t.Fatalf("Start failed: %v", err)
		}
		defer ms.Stop()

		m := monitor.Monitor{
			ID:       "dyn-update",
			Enabled:  true,
			Interval: 1 * time.Hour,
		}
		repo.addMonitor(m, state.StateHealthy)

		if err := ms.StartMonitor(ctx, m); err != nil {
			t.Fatalf("StartMonitor failed: %v", err)
		}
		<-cycleTriggered

		// Update monitor with new interval (and enabled=true)
		mUpdated := monitor.Monitor{
			ID:       "dyn-update",
			Enabled:  true,
			Interval: 20 * time.Millisecond,
		}
		if err := ms.UpdateMonitor(ctx, mUpdated); err != nil {
			t.Fatalf("UpdateMonitor failed: %v", err)
		}

		// New runner immediately runs first cycle
		select {
		case <-cycleTriggered:
		case <-time.After(500 * time.Millisecond):
			t.Fatal("timed out waiting for new runner immediate cycle")
		}

		// And then runs on the new 20ms fixed-delay interval
		select {
		case <-cycleTriggered:
		case <-time.After(500 * time.Millisecond):
			t.Fatal("timed out waiting for second cycle on new interval")
		}

		// Disable via UpdateMonitor
		mDisabled := monitor.Monitor{
			ID:       "dyn-update",
			Enabled:  false,
			Interval: 20 * time.Millisecond,
		}
		if err := ms.UpdateMonitor(ctx, mDisabled); err != nil {
			t.Fatalf("UpdateMonitor disabled failed: %v", err)
		}
		if ms.HasRunner("dyn-update") {
			t.Fatal("expected no runner after disabling via UpdateMonitor")
		}
	})

	t.Run("StopMonitor is synchronous and waits for runner termination", func(t *testing.T) {
		repo := newMockMultiRepo()
		inCycle := make(chan struct{})
		allowCycleFinish := make(chan struct{})

		runner := newMockCycleRunner()
		runner.cycleFn = func(ctx context.Context, m monitor.Monitor, current state.State) (scheduler.CycleResult, error) {
			close(inCycle)
			<-allowCycleFinish
			return scheduler.CycleResult{
				TransitionResult: state.TransitionResult{NextState: state.StateHealthy},
			}, nil
		}

		ms, err := scheduler.NewMultiScheduler(repo, runner)
		if err != nil {
			t.Fatalf("NewMultiScheduler failed: %v", err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if err := ms.Start(ctx); err != nil {
			t.Fatalf("Start failed: %v", err)
		}
		defer ms.Stop()

		m := monitor.Monitor{
			ID:       "sync-stop",
			Enabled:  true,
			Interval: 1 * time.Hour,
		}
		repo.addMonitor(m, state.StateUnknown)

		if err := ms.StartMonitor(ctx, m); err != nil {
			t.Fatalf("StartMonitor failed: %v", err)
		}

		// Wait until runner is inside cycle execution
		<-inCycle

		stopFinished := make(chan struct{})
		go func() {
			if err := ms.StopMonitor(ctx, "sync-stop"); err != nil {
				t.Errorf("StopMonitor failed: %v", err)
			}
			close(stopFinished)
		}()

		// Verify stop has NOT finished yet because runner hasn't terminated
		select {
		case <-stopFinished:
			t.Fatal("StopMonitor finished before runner terminated!")
		case <-time.After(30 * time.Millisecond):
			// Expected
		}

		// Now allow cycle to finish
		close(allowCycleFinish)

		select {
		case <-stopFinished:
			// Success: StopMonitor waited and finished after runner termination
		case <-time.After(1 * time.Second):
			t.Fatal("StopMonitor did not finish after cycle completed")
		}
	})

	t.Run("concurrent lifecycle operations on same monitor", func(t *testing.T) {
		repo := newMockMultiRepo()
		runner := newMockCycleRunner()
		ms, err := scheduler.NewMultiScheduler(repo, runner)
		if err != nil {
			t.Fatalf("NewMultiScheduler failed: %v", err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if err := ms.Start(ctx); err != nil {
			t.Fatalf("Start failed: %v", err)
		}
		defer ms.Stop()

		m := monitor.Monitor{
			ID:       "racing-mon",
			Enabled:  true,
			Interval: 100 * time.Millisecond,
		}
		repo.addMonitor(m, state.StateHealthy)

		const ops = 30
		var wg sync.WaitGroup
		wg.Add(ops)

		for i := 0; i < ops; i++ {
			go func(idx int) {
				defer wg.Done()
				switch idx % 4 {
				case 0:
					_ = ms.StartMonitor(ctx, m)
				case 1:
					_ = ms.StopMonitor(ctx, m.ID)
				case 2:
					_ = ms.UpdateMonitor(ctx, m)
				case 3:
					mDis := m
					mDis.Enabled = false
					_ = ms.UpdateMonitor(ctx, mDis)
				}
			}(i)
		}

		wg.Wait()

		// Final check: active runner count is either 0 or 1, never > 1
		runners := ms.ActiveRunners()
		if runners > 1 {
			t.Fatalf("expected at most 1 runner, got %d", runners)
		}
	})
}

