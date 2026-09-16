package scheduler_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chavaliadi/watchdog/internal/checker"
	"github.com/chavaliadi/watchdog/internal/monitor"
	"github.com/chavaliadi/watchdog/internal/scheduler"
	"github.com/chavaliadi/watchdog/internal/state"
)

type fakeCycleRunner struct {
	mu           sync.Mutex
	calls        int
	activeCycles int32
	maxActive    int32

	statesReceived []state.State
	runFn          func(ctx context.Context, m monitor.Monitor, current state.State) (scheduler.CycleResult, error)
}

func (f *fakeCycleRunner) RunCycle(ctx context.Context, m monitor.Monitor, current state.State) (scheduler.CycleResult, error) {
	active := atomic.AddInt32(&f.activeCycles, 1)
	defer atomic.AddInt32(&f.activeCycles, -1)

	// Update peak active cycles observed
	for {
		curMax := atomic.LoadInt32(&f.maxActive)
		if active <= curMax {
			break
		}
		if atomic.CompareAndSwapInt32(&f.maxActive, curMax, active) {
			break
		}
	}

	f.mu.Lock()
	f.calls++
	f.statesReceived = append(f.statesReceived, current)
	fn := f.runFn
	f.mu.Unlock()

	if fn != nil {
		return fn(ctx, m, current)
	}

	return scheduler.CycleResult{
		Monitor: m,
		CheckResult: checker.CheckResult{
			OK: true,
		},
		TransitionResult: state.TransitionResult{
			CurrentState: current,
			NextState:    state.StateHealthy,
			Transitioned: current != state.StateHealthy,
		},
	}, nil
}

func (f *fakeCycleRunner) getCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeCycleRunner) getStates() []state.State {
	f.mu.Lock()
	defer f.mu.Unlock()
	copied := make([]state.State, len(f.statesReceived))
	copy(copied, f.statesReceived)
	return copied
}

func TestScheduler_InvalidInterval(t *testing.T) {
	runner := &fakeCycleRunner{}
	s := scheduler.NewScheduler(runner)
	m := monitor.Monitor{ID: "mon-1"}

	tests := []struct {
		name     string
		interval time.Duration
	}{
		{"zero interval", 0},
		{"negative interval", -1 * time.Second},
		{"negative millisecond", -5 * time.Millisecond},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := s.Run(context.Background(), m, state.StateUnknown, tt.interval)
			if err == nil {
				t.Fatalf("expected error for interval %v, got nil", tt.interval)
			}
			if runner.getCalls() != 0 {
				t.Errorf("expected 0 cycles executed on invalid interval, got %d", runner.getCalls())
			}
		})
	}
}

func TestScheduler_Uninitialized(t *testing.T) {
	var s *scheduler.Scheduler
	m := monitor.Monitor{ID: "mon-1"}

	if err := s.Run(context.Background(), m, state.StateUnknown, time.Second); err == nil {
		t.Fatal("expected error on nil Scheduler, got nil")
	}

	s2 := scheduler.NewScheduler(nil)
	if err := s2.Run(context.Background(), m, state.StateUnknown, time.Second); err == nil {
		t.Fatal("expected error on Scheduler with nil runner, got nil")
	}

	s3 := scheduler.NewScheduler(&fakeCycleRunner{})
	if err := s3.Run(nil, m, state.StateUnknown, time.Second); err == nil {
		t.Fatal("expected error on nil context, got nil")
	}
}

func TestScheduler_ImmediateFirstCycle(t *testing.T) {
	runner := &fakeCycleRunner{}
	s := scheduler.NewScheduler(runner)
	m := monitor.Monitor{ID: "mon-immediate"}

	ctx, cancel := context.WithCancel(context.Background())

	// Use an extremely long interval (1 hour) to prove the first cycle does NOT wait for the ticker.
	done := make(chan error, 1)
	go func() {
		done <- s.Run(ctx, m, state.StateUnknown, time.Hour)
	}()

	// Wait briefly for the immediate cycle to run
	time.Sleep(50 * time.Millisecond)

	if runner.getCalls() != 1 {
		t.Errorf("expected immediate first cycle, got %d calls", runner.getCalls())
	}

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled error on cancel, got: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for scheduler to exit after cancellation")
	}
}

func TestScheduler_RepeatedExecution(t *testing.T) {
	runner := &fakeCycleRunner{}
	s := scheduler.NewScheduler(runner)
	m := monitor.Monitor{ID: "mon-repeat"}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const targetCycles = 4
	interval := 15 * time.Millisecond

	done := make(chan error, 1)
	go func() {
		done <- s.Run(ctx, m, state.StateUnknown, interval)
	}()

	// Wait until runner has executed targetCycles
	deadline := time.After(2 * time.Second)
	for runner.getCalls() < targetCycles {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for %d cycles, only got %d", targetCycles, runner.getCalls())
		case <-time.After(5 * time.Millisecond):
		}
	}

	cancel()
	err := <-done
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got: %v", err)
	}

	if runner.getCalls() < targetCycles {
		t.Errorf("expected at least %d cycles, got %d", targetCycles, runner.getCalls())
	}
}

func TestScheduler_NoOverlap(t *testing.T) {
	interval := 10 * time.Millisecond
	cycleDuration := 40 * time.Millisecond // Each cycle takes 4x longer than interval

	runner := &fakeCycleRunner{
		runFn: func(ctx context.Context, m monitor.Monitor, current state.State) (scheduler.CycleResult, error) {
			select {
			case <-time.After(cycleDuration):
			case <-ctx.Done():
				return scheduler.CycleResult{}, ctx.Err()
			}
			return scheduler.CycleResult{
				Monitor: m,
				TransitionResult: state.TransitionResult{
					CurrentState: current,
					NextState:    state.StateHealthy,
				},
			}, nil
		},
	}

	s := scheduler.NewScheduler(runner)
	m := monitor.Monitor{ID: "mon-no-overlap"}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- s.Run(ctx, m, state.StateUnknown, interval)
	}()

	// Run for at least 3 cycles
	deadline := time.After(2 * time.Second)
	for runner.getCalls() < 3 {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for 3 cycles, got %d", runner.getCalls())
		case <-time.After(10 * time.Millisecond):
		}
	}

	cancel()
	<-done

	maxActive := atomic.LoadInt32(&runner.maxActive)
	if maxActive != 1 {
		t.Errorf("expected peak concurrent cycles = 1 (no overlap), got %d", maxActive)
	}
}

func TestScheduler_ContextCancellationBeforeStart(t *testing.T) {
	runner := &fakeCycleRunner{}
	s := scheduler.NewScheduler(runner)
	m := monitor.Monitor{ID: "mon-precanceled"}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel prior to Run

	err := s.Run(ctx, m, state.StateUnknown, 50*time.Millisecond)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}

	if runner.getCalls() != 0 {
		t.Errorf("expected 0 cycles executed with pre-canceled context, got %d", runner.getCalls())
	}
}

func TestScheduler_ContextCancellationWhileWaiting(t *testing.T) {
	runner := &fakeCycleRunner{}
	s := scheduler.NewScheduler(runner)
	m := monitor.Monitor{ID: "mon-wait-cancel"}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- s.Run(ctx, m, state.StateUnknown, 200*time.Millisecond)
	}()

	// Wait for the immediate cycle to complete
	deadline := time.After(time.Second)
	for runner.getCalls() < 1 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for initial cycle")
		case <-time.After(5 * time.Millisecond):
		}
	}

	// Cancel while waiting for the second tick
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for scheduler to exit")
	}

	if runner.getCalls() != 1 {
		t.Errorf("expected exactly 1 cycle executed before cancel, got %d", runner.getCalls())
	}
}

func TestScheduler_ContextCancellationDuringCycle(t *testing.T) {
	cycleStarted := make(chan struct{})
	cycleObservedCancel := make(chan struct{})

	runner := &fakeCycleRunner{
		runFn: func(ctx context.Context, m monitor.Monitor, current state.State) (scheduler.CycleResult, error) {
			close(cycleStarted)
			<-ctx.Done()
			close(cycleObservedCancel)
			return scheduler.CycleResult{}, ctx.Err()
		},
	}

	s := scheduler.NewScheduler(runner)
	m := monitor.Monitor{ID: "mon-cancel-during-cycle"}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- s.Run(ctx, m, state.StateUnknown, time.Hour)
	}()

	// Wait until the cycle is in-flight
	select {
	case <-cycleStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for cycle to start")
	}

	// Cancel context while cycle is actively executing
	cancel()

	// Verify cycle observed the cancellation
	select {
	case <-cycleObservedCancel:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for runner to observe context cancellation")
	}

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for scheduler Run to return")
	}
}

func TestScheduler_CycleErrorBehavior(t *testing.T) {
	var errorCount int32
	var recordedErrors []error
	var mu sync.Mutex

	transientErr := errors.New("temporary connection error")

	runner := &fakeCycleRunner{
		runFn: func(ctx context.Context, m monitor.Monitor, current state.State) (scheduler.CycleResult, error) {
			// Fail cycle 1, succeed cycle 2 onwards
			if atomic.AddInt32(&errorCount, 1) == 1 {
				return scheduler.CycleResult{}, transientErr
			}
			return scheduler.CycleResult{
				Monitor: m,
				TransitionResult: state.TransitionResult{
					CurrentState: current,
					NextState:    state.StateHealthy,
				},
			}, nil
		},
	}

	s := scheduler.NewScheduler(runner).WithErrorHandler(func(err error) {
		mu.Lock()
		recordedErrors = append(recordedErrors, err)
		mu.Unlock()
	})

	m := monitor.Monitor{ID: "mon-error-recovery"}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- s.Run(ctx, m, state.StateUnknown, 15*time.Millisecond)
	}()

	// Wait for at least 3 cycles (1 error + 2 successes)
	deadline := time.After(2 * time.Second)
	for runner.getCalls() < 3 {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for cycles, got %d", runner.getCalls())
		case <-time.After(5 * time.Millisecond):
		}
	}

	cancel()
	<-done

	mu.Lock()
	if len(recordedErrors) != 1 {
		t.Errorf("expected 1 recorded cycle error, got %d", len(recordedErrors))
	} else if !errors.Is(recordedErrors[0], transientErr) {
		t.Errorf("expected transientErr, got %v", recordedErrors[0])
	}
	mu.Unlock()

	// Verify scheduler did not terminate prematurely and continued checking
	if runner.getCalls() < 3 {
		t.Errorf("expected scheduler to continue after error, completed %d calls", runner.getCalls())
	}
}

func TestScheduler_StateTracking(t *testing.T) {
	var callNum int32

	runner := &fakeCycleRunner{
		runFn: func(ctx context.Context, m monitor.Monitor, current state.State) (scheduler.CycleResult, error) {
			n := atomic.AddInt32(&callNum, 1)
			var next state.State
			switch n {
			case 1:
				// UNKNOWN -> HEALTHY
				next = state.StateHealthy
			case 2:
				// Return error: state must not update
				return scheduler.CycleResult{}, errors.New("persistence failure")
			case 3:
				// HEALTHY -> UNHEALTHY
				next = state.StateUnhealthy
			default:
				next = current
			}
			return scheduler.CycleResult{
				Monitor: m,
				TransitionResult: state.TransitionResult{
					CurrentState: current,
					NextState:    next,
				},
			}, nil
		},
	}

	s := scheduler.NewScheduler(runner)
	m := monitor.Monitor{ID: "mon-state-track"}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- s.Run(ctx, m, state.StateUnknown, 10*time.Millisecond)
	}()

	deadline := time.After(2 * time.Second)
	for runner.getCalls() < 3 {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for cycles, got %d", runner.getCalls())
		case <-time.After(5 * time.Millisecond):
		}
	}

	cancel()
	<-done

	states := runner.getStates()
	if len(states) < 3 {
		t.Fatalf("expected at least 3 cycle states, got %d", len(states))
	}

	// Cycle 1: started with UNKNOWN
	if states[0] != state.StateUnknown {
		t.Errorf("cycle 1 expected UNKNOWN, got %s", states[0])
	}
	// Cycle 2: advanced to HEALTHY from cycle 1
	if states[1] != state.StateHealthy {
		t.Errorf("cycle 2 expected HEALTHY, got %s", states[1])
	}
	// Cycle 3: cycle 2 failed, so cycle 3 must STILL have HEALTHY
	if states[2] != state.StateHealthy {
		t.Errorf("cycle 3 expected HEALTHY (retained from failed cycle), got %s", states[2])
	}
}
