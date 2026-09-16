package worker_test

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
	"github.com/chavaliadi/watchdog/internal/retry"
	"github.com/chavaliadi/watchdog/internal/scheduler"
	"github.com/chavaliadi/watchdog/internal/state"
	"github.com/chavaliadi/watchdog/internal/worker"
)

// fakeRunner is a controllable CycleRunner implementation for unit testing.
type fakeRunner struct {
	mu           sync.Mutex
	runCycleFunc func(ctx context.Context, m monitor.Monitor, current state.State) (scheduler.CycleResult, error)
	calls        int
}

func (f *fakeRunner) RunCycle(ctx context.Context, m monitor.Monitor, current state.State) (scheduler.CycleResult, error) {
	f.mu.Lock()
	f.calls++
	fn := f.runCycleFunc
	f.mu.Unlock()

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

func TestPool_Construction(t *testing.T) {
	t.Run("Invalid MaxConcurrency returns error", func(t *testing.T) {
		runner := &fakeRunner{}
		for _, invalidLimit := range []int{0, -1, -10} {
			_, err := worker.NewPool(worker.Config{MaxConcurrency: invalidLimit}, runner)
			if err == nil {
				t.Errorf("expected error for MaxConcurrency %d, got nil", invalidLimit)
			}
			if !errors.Is(err, worker.ErrInvalidConfig) {
				t.Errorf("expected ErrInvalidConfig, got %v", err)
			}
		}
	})

	t.Run("Nil runner returns error", func(t *testing.T) {
		_, err := worker.NewPool(worker.Config{MaxConcurrency: 2}, nil)
		if err == nil {
			t.Fatal("expected error for nil runner, got nil")
		}
		if !errors.Is(err, worker.ErrInvalidConfig) {
			t.Errorf("expected ErrInvalidConfig, got %v", err)
		}
	})

	t.Run("Valid config returns pool", func(t *testing.T) {
		p, err := worker.NewPool(worker.Config{MaxConcurrency: 4, QueueCapacity: 10}, &fakeRunner{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p == nil {
			t.Fatal("expected non-nil pool")
		}
	})
}

func TestPool_LifecycleAndSubmissions(t *testing.T) {
	runner := &fakeRunner{}
	pool, err := worker.NewPool(worker.Config{MaxConcurrency: 2}, runner)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	m := monitor.Monitor{ID: "mon-1", Name: "Test Monitor"}

	t.Run("Submit on unstarted pool returns ErrPoolNotStarted", func(t *testing.T) {
		err := pool.Submit(worker.Job{Monitor: m, CurrentState: state.StateUnknown})
		if !errors.Is(err, worker.ErrPoolNotStarted) {
			t.Errorf("expected ErrPoolNotStarted, got %v", err)
		}
	})

	t.Run("Empty monitor ID returns error", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		pool.Start(ctx)

		err := pool.Submit(worker.Job{Monitor: monitor.Monitor{}, CurrentState: state.StateUnknown})
		if err == nil {
			t.Fatal("expected error submitting empty monitor ID, got nil")
		}
	})

	t.Run("Submit after Stop returns ErrPoolStopped", func(t *testing.T) {
		pool.Stop()
		pool.Wait()

		err := pool.Submit(worker.Job{Monitor: m, CurrentState: state.StateUnknown})
		if !errors.Is(err, worker.ErrPoolStopped) {
			t.Errorf("expected ErrPoolStopped, got %v", err)
		}
	})
}

func TestPool_Execution(t *testing.T) {
	runner := &fakeRunner{}
	pool, err := worker.NewPool(worker.Config{MaxConcurrency: 2}, runner)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)
	defer func() {
		pool.Stop()
		pool.Wait()
	}()

	t.Run("Single job executes and returns result via ResultChan", func(t *testing.T) {
		m := monitor.Monitor{ID: "mon-exec-1", Name: "Single Execution"}
		resCh := make(chan worker.Result, 1)

		err := pool.Submit(worker.Job{
			Monitor:      m,
			CurrentState: state.StateUnknown,
			ResultChan:   resCh,
		})
		if err != nil {
			t.Fatalf("Submit failed: %v", err)
		}

		select {
		case res := <-resCh:
			if res.Err != nil {
				t.Fatalf("unexpected job error: %v", res.Err)
			}
			if res.MonitorID != m.ID {
				t.Errorf("monitor ID mismatch: got %q, want %q", res.MonitorID, m.ID)
			}
			if res.CycleResult.TransitionResult.NextState != state.StateHealthy {
				t.Errorf("expected NextState HEALTHY, got %v", res.CycleResult.TransitionResult.NextState)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for job completion")
		}
	})

	t.Run("SubmitWait executes synchronously", func(t *testing.T) {
		m := monitor.Monitor{ID: "mon-wait-1", Name: "SubmitWait Execution"}
		cycleRes, err := pool.SubmitWait(ctx, worker.Job{
			Monitor:      m,
			CurrentState: state.StateHealthy,
		})
		if err != nil {
			t.Fatalf("SubmitWait failed: %v", err)
		}
		if cycleRes.Monitor.ID != m.ID {
			t.Errorf("monitor ID mismatch: got %q, want %q", cycleRes.Monitor.ID, m.ID)
		}
	})
}

func TestPool_BoundedConcurrency(t *testing.T) {
	const maxConcurrency = 3
	const totalJobs = 20

	var (
		currentActive int64
		peakActive    int64
		completed     int64
	)

	runner := &fakeRunner{
		runCycleFunc: func(ctx context.Context, m monitor.Monitor, current state.State) (scheduler.CycleResult, error) {
			active := atomic.AddInt64(&currentActive, 1)

			// Track maximum peak concurrency safely
			for {
				peak := atomic.LoadInt64(&peakActive)
				if active <= peak || atomic.CompareAndSwapInt64(&peakActive, peak, active) {
					break
				}
			}

			// Simulate work
			select {
			case <-ctx.Done():
				atomic.AddInt64(&currentActive, -1)
				return scheduler.CycleResult{}, ctx.Err()
			case <-time.After(20 * time.Millisecond):
			}

			atomic.AddInt64(&currentActive, -1)
			atomic.AddInt64(&completed, 1)

			return scheduler.CycleResult{
				Monitor: m,
				TransitionResult: state.TransitionResult{
					CurrentState: current,
					NextState:    state.StateHealthy,
				},
			}, nil
		},
	}

	pool, err := worker.NewPool(worker.Config{MaxConcurrency: maxConcurrency}, runner)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)

	resCh := make(chan worker.Result, totalJobs)

	for i := 0; i < totalJobs; i++ {
		m := monitor.Monitor{ID: fmt.Sprintf("mon-bound-%02d", i)}
		err := pool.Submit(worker.Job{
			Monitor:      m,
			CurrentState: state.StateUnknown,
			ResultChan:   resCh,
		})
		if err != nil {
			t.Fatalf("submit job %d failed: %v", i, err)
		}
	}

	// Wait for all jobs to complete
	for i := 0; i < totalJobs; i++ {
		select {
		case res := <-resCh:
			if res.Err != nil {
				t.Fatalf("job %d failed: %v", i, res.Err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for job %d to finish", i)
		}
	}

	pool.Stop()
	pool.Wait()

	peak := atomic.LoadInt64(&peakActive)
	if peak > maxConcurrency {
		t.Errorf("EXCEEDED MAX CONCURRENCY: peak active was %d, configured limit was %d", peak, maxConcurrency)
	}
	if peak <= 0 {
		t.Errorf("invalid peak concurrency: %d", peak)
	}
	if atomic.LoadInt64(&completed) != totalJobs {
		t.Errorf("expected %d completed jobs, got %d", totalJobs, completed)
	}
}

func TestPool_SameMonitorSerialization(t *testing.T) {
	const maxConcurrency = 3
	var (
		activeMonitors   = make(map[string]int)
		activeMonitorsMu sync.Mutex
		overlapDetected  bool
	)

	a1Started := make(chan struct{})
	a1Proceed := make(chan struct{})

	runner := &fakeRunner{
		runCycleFunc: func(ctx context.Context, m monitor.Monitor, current state.State) (scheduler.CycleResult, error) {
			activeMonitorsMu.Lock()
			activeMonitors[m.ID]++
			if activeMonitors[m.ID] > 1 {
				overlapDetected = true
			}
			count := activeMonitors[m.ID]
			activeMonitorsMu.Unlock()

			if m.Name == "Job-A1" {
				close(a1Started)
				<-a1Proceed
			}

			activeMonitorsMu.Lock()
			activeMonitors[m.ID]--
			activeMonitorsMu.Unlock()

			if count > 1 {
				return scheduler.CycleResult{}, errors.New("concurrent execution of same monitor detected")
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

	pool, err := worker.NewPool(worker.Config{MaxConcurrency: maxConcurrency}, runner)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)

	mA1 := monitor.Monitor{ID: "same-mon-1", Name: "Job-A1"}
	mA2 := monitor.Monitor{ID: "same-mon-1", Name: "Job-A2"}

	resChA1 := make(chan worker.Result, 1)
	resChA2 := make(chan worker.Result, 1)

	// Submit Job A1
	if err := pool.Submit(worker.Job{Monitor: mA1, CurrentState: state.StateUnknown, ResultChan: resChA1}); err != nil {
		t.Fatalf("Submit A1 failed: %v", err)
	}

	// Wait until A1 is actively running inside RunCycle
	<-a1Started

	// Submit Job A2 for the same monitor while A1 is actively executing
	if err := pool.Submit(worker.Job{Monitor: mA2, CurrentState: state.StateHealthy, ResultChan: resChA2}); err != nil {
		t.Fatalf("Submit A2 failed: %v", err)
	}

	// Ensure A2 has not started while A1 is running
	select {
	case <-resChA2:
		t.Fatal("A2 completed before A1 finished!")
	default:
	}

	// Allow A1 to complete
	close(a1Proceed)

	// A1 must complete successfully
	select {
	case res := <-resChA1:
		if res.Err != nil {
			t.Fatalf("A1 failed: %v", res.Err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for A1")
	}

	// Now A2 must execute and complete
	select {
	case res := <-resChA2:
		if res.Err != nil {
			t.Fatalf("A2 failed: %v", res.Err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for A2")
	}

	pool.Stop()
	pool.Wait()

	if overlapDetected {
		t.Fatal("OVERLAP DETECTED: two cycles for the same monitor ran concurrently!")
	}
}

func TestPool_DifferentMonitorConcurrency(t *testing.T) {
	const maxConcurrency = 2

	a1Started := make(chan struct{})
	b1Started := make(chan struct{})
	barrier := make(chan struct{})

	runner := &fakeRunner{
		runCycleFunc: func(ctx context.Context, m monitor.Monitor, current state.State) (scheduler.CycleResult, error) {
			if m.ID == "mon-diff-A" {
				close(a1Started)
			} else if m.ID == "mon-diff-B" {
				close(b1Started)
			}

			// Block until both monitors have signaled that they are concurrently inside RunCycle
			<-barrier

			return scheduler.CycleResult{
				Monitor: m,
				TransitionResult: state.TransitionResult{
					CurrentState: current,
					NextState:    state.StateHealthy,
				},
			}, nil
		},
	}

	pool, err := worker.NewPool(worker.Config{MaxConcurrency: maxConcurrency}, runner)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)

	resChA := make(chan worker.Result, 1)
	resChB := make(chan worker.Result, 1)

	if err := pool.Submit(worker.Job{
		Monitor:      monitor.Monitor{ID: "mon-diff-A"},
		CurrentState: state.StateUnknown,
		ResultChan:   resChA,
	}); err != nil {
		t.Fatalf("Submit A failed: %v", err)
	}

	if err := pool.Submit(worker.Job{
		Monitor:      monitor.Monitor{ID: "mon-diff-B"},
		CurrentState: state.StateUnknown,
		ResultChan:   resChB,
	}); err != nil {
		t.Fatalf("Submit B failed: %v", err)
	}

	// Verify both monitors start running at the same time
	select {
	case <-a1Started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for A to start")
	}

	select {
	case <-b1Started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for B to start concurrently with A")
	}

	// Release both
	close(barrier)

	<-resChA
	<-resChB

	pool.Stop()
	pool.Wait()
}

func TestPool_NoHeadOfLineBlocking(t *testing.T) {
	// Invariant: A pending job for a monitor that is already running (A2) must not
	// prevent runnable jobs for other monitors (B1) from using available worker capacity.
	const maxConcurrency = 2

	a1Started := make(chan struct{})
	a1Proceed := make(chan struct{})
	b1Started := make(chan struct{})
	a2Started := make(chan struct{})

	runner := &fakeRunner{
		runCycleFunc: func(ctx context.Context, m monitor.Monitor, current state.State) (scheduler.CycleResult, error) {
			switch m.Name {
			case "Job-A1":
				close(a1Started)
				<-a1Proceed
			case "Job-B1":
				close(b1Started)
			case "Job-A2":
				close(a2Started)
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

	pool, err := worker.NewPool(worker.Config{MaxConcurrency: maxConcurrency}, runner)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)

	monA := monitor.Monitor{ID: "mon-block-A"}
	monB := monitor.Monitor{ID: "mon-block-B"}

	resChA1 := make(chan worker.Result, 1)
	resChA2 := make(chan worker.Result, 1)
	resChB1 := make(chan worker.Result, 1)

	// 1. Submit A1
	monA1 := monA
	monA1.Name = "Job-A1"
	if err := pool.Submit(worker.Job{Monitor: monA1, CurrentState: state.StateUnknown, ResultChan: resChA1}); err != nil {
		t.Fatalf("Submit A1 failed: %v", err)
	}

	// Wait until A1 is actively executing and holding 1 worker
	select {
	case <-a1Started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for A1 to start")
	}

	// 2. Submit A2 (for same monitor A) -> this enters pending queue ahead of B1
	monA2 := monA
	monA2.Name = "Job-A2"
	if err := pool.Submit(worker.Job{Monitor: monA2, CurrentState: state.StateHealthy, ResultChan: resChA2}); err != nil {
		t.Fatalf("Submit A2 failed: %v", err)
	}

	// 3. Submit B1 (for different monitor B) -> this enters pending queue behind A2
	monB1 := monB
	monB1.Name = "Job-B1"
	if err := pool.Submit(worker.Job{Monitor: monB1, CurrentState: state.StateUnknown, ResultChan: resChB1}); err != nil {
		t.Fatalf("Submit B1 failed: %v", err)
	}

	// 4. Verify B1 starts executing on available worker 2 WHILE A1 is still running!
	select {
	case <-b1Started:
		// SUCCESS: B1 executed even though A2 was ahead of it in the queue and monitor A was busy!
	case <-time.After(2 * time.Second):
		t.Fatal("HEAD-OF-LINE BLOCKING DETECTED: B1 failed to execute while A1 was running and A2 was pending!")
	}

	// 5. Allow A1 to complete
	close(a1Proceed)

	select {
	case <-resChA1:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for A1 to finish")
	}

	select {
	case <-resChB1:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for B1 to finish")
	}

	// 6. After A1 completed, A2 should have run and finished
	select {
	case <-a2Started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for A2 to start")
	}

	select {
	case <-resChA2:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for A2 to finish")
	}

	pool.Stop()
	pool.Wait()
}

func TestPool_Cancellation(t *testing.T) {
	t.Run("Submit on pre-canceled context returns error", func(t *testing.T) {
		runner := &fakeRunner{}
		pool, err := worker.NewPool(worker.Config{MaxConcurrency: 1}, runner)
		if err != nil {
			t.Fatalf("NewPool failed: %v", err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel before starting
		pool.Start(ctx)

		err = pool.Submit(worker.Job{
			Monitor:      monitor.Monitor{ID: "mon-cancel-1"},
			CurrentState: state.StateUnknown,
		})
		if err == nil {
			t.Fatal("expected error submitting to pre-canceled pool, got nil")
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", err)
		}

		pool.Wait()
	})

	t.Run("Cancellation while jobs are queued drains pending jobs with context error", func(t *testing.T) {
		job1Started := make(chan struct{})
		job1Block := make(chan struct{})

		runner := &fakeRunner{
			runCycleFunc: func(ctx context.Context, m monitor.Monitor, current state.State) (scheduler.CycleResult, error) {
				if m.ID == "mon-job-1" {
					close(job1Started)
					select {
					case <-ctx.Done():
						return scheduler.CycleResult{}, ctx.Err()
					case <-job1Block:
						return scheduler.CycleResult{Monitor: m}, nil
					}
				}
				return scheduler.CycleResult{Monitor: m}, nil
			},
		}

		pool, err := worker.NewPool(worker.Config{MaxConcurrency: 1}, runner)
		if err != nil {
			t.Fatalf("NewPool failed: %v", err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		pool.Start(ctx)

		resCh1 := make(chan worker.Result, 1)
		resCh2 := make(chan worker.Result, 1)

		// Submit job 1 (will occupy the single worker)
		if err := pool.Submit(worker.Job{
			Monitor:      monitor.Monitor{ID: "mon-job-1"},
			CurrentState: state.StateUnknown,
			ResultChan:   resCh1,
		}); err != nil {
			t.Fatalf("Submit job 1 failed: %v", err)
		}

		<-job1Started

		// Submit job 2 (will sit in pending)
		if err := pool.Submit(worker.Job{
			Monitor:      monitor.Monitor{ID: "mon-job-2"},
			CurrentState: state.StateUnknown,
			ResultChan:   resCh2,
		}); err != nil {
			t.Fatalf("Submit job 2 failed: %v", err)
		}

		// Cancel parent context
		cancel()

		// Job 2 must be rejected with cancellation error without executing
		select {
		case res := <-resCh2:
			if !errors.Is(res.Err, context.Canceled) {
				t.Errorf("expected job 2 to receive context.Canceled, got %v", res.Err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for pending job 2 to be cancelled")
		}

		// Job 1 should also complete with context cancellation
		select {
		case res := <-resCh1:
			if !errors.Is(res.Err, context.Canceled) {
				t.Errorf("expected job 1 to receive context.Canceled, got %v", res.Err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for active job 1 to exit")
		}

		pool.Wait()
	})

	t.Run("Cancellation interrupts retry backoff cleanly", func(t *testing.T) {
		failingChecker := &fakeCheckerFail{
			attempted: make(chan struct{}),
		}
		retryCfg := retry.Config{
			MaxAttempts: 5,
			BaseDelay:   500 * time.Millisecond, // Long backoff so we cancel during sleep
			MaxDelay:    1 * time.Second,
		}

		orch := scheduler.NewOrchestrator(failingChecker, nil, retryCfg)

		pool, err := worker.NewPool(worker.Config{MaxConcurrency: 1}, orch)
		if err != nil {
			t.Fatalf("NewPool failed: %v", err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		pool.Start(ctx)

		resCh := make(chan worker.Result, 1)
		m := monitor.Monitor{
			ID:        "mon-retry-cancel",
			Kind:      monitor.KindHTTP,
			TargetURL: "https://fail.example.com",
		}

		if err := pool.Submit(worker.Job{
			Monitor:      m,
			CurrentState: state.StateUnknown,
			ResultChan:   resCh,
		}); err != nil {
			t.Fatalf("Submit failed: %v", err)
		}

		// Wait briefly until first attempt fails and retrier enters backoff
		select {
		case <-failingChecker.attempted:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for first check attempt")
		}

		// Cancel during retry backoff
		cancel()

		select {
		case res := <-resCh:
			if !errors.Is(res.Err, context.Canceled) {
				t.Errorf("expected retry to terminate with context.Canceled, got %v", res.Err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for retry cancellation")
		}

		pool.Wait()
	})
}

type fakeCheckerFail struct {
	once      sync.Once
	attempted chan struct{}
}

func (f *fakeCheckerFail) Check(ctx context.Context, m monitor.Monitor) (checker.CheckResult, error) {
	f.once.Do(func() {
		close(f.attempted)
	})

	return checker.CheckResult{
		OK:         false,
		StatusCode: 500,
		ErrorClass: checker.ErrorClassStatus,
	}, nil
}

func TestPool_ErrorIsolation(t *testing.T) {
	runner := &fakeRunner{
		runCycleFunc: func(ctx context.Context, m monitor.Monitor, current state.State) (scheduler.CycleResult, error) {
			if m.ID == "mon-failing" {
				return scheduler.CycleResult{}, errors.New("simulated operational repository failure")
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

	pool, err := worker.NewPool(worker.Config{MaxConcurrency: 2}, runner)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)

	resChFail := make(chan worker.Result, 1)
	resChSuccess := make(chan worker.Result, 1)

	// Submit failing job
	if err := pool.Submit(worker.Job{
		Monitor:      monitor.Monitor{ID: "mon-failing"},
		CurrentState: state.StateUnknown,
		ResultChan:   resChFail,
	}); err != nil {
		t.Fatalf("Submit failing job failed: %v", err)
	}

	// Submit succeeding job
	if err := pool.Submit(worker.Job{
		Monitor:      monitor.Monitor{ID: "mon-succeeding"},
		CurrentState: state.StateUnknown,
		ResultChan:   resChSuccess,
	}); err != nil {
		t.Fatalf("Submit succeeding job failed: %v", err)
	}

	// Verify fail result
	select {
	case res := <-resChFail:
		if res.Err == nil {
			t.Fatal("expected error for mon-failing, got nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for mon-failing")
	}

	// Verify success result was NOT impacted
	select {
	case res := <-resChSuccess:
		if res.Err != nil {
			t.Fatalf("expected mon-succeeding to succeed, got %v", res.Err)
		}
		if res.CycleResult.TransitionResult.NextState != state.StateHealthy {
			t.Errorf("expected HEALTHY, got %v", res.CycleResult.TransitionResult.NextState)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for mon-succeeding")
	}

	pool.Stop()
	pool.Wait()
}

func TestPool_QueueCapacity(t *testing.T) {
	job1Started := make(chan struct{})
	job1Block := make(chan struct{})

	runner := &fakeRunner{
		runCycleFunc: func(ctx context.Context, m monitor.Monitor, current state.State) (scheduler.CycleResult, error) {
			if m.ID == "mon-q-1" {
				close(job1Started)
				<-job1Block
			}
			return scheduler.CycleResult{Monitor: m}, nil
		},
	}

	// QueueCapacity = 2, MaxConcurrency = 1
	pool, err := worker.NewPool(worker.Config{MaxConcurrency: 1, QueueCapacity: 2}, runner)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)

	// Job 1 occupies the worker
	if err := pool.Submit(worker.Job{Monitor: monitor.Monitor{ID: "mon-q-1"}, CurrentState: state.StateUnknown}); err != nil {
		t.Fatalf("Submit job 1 failed: %v", err)
	}
	<-job1Started

	// Job 2 fills queue slot 1
	if err := pool.Submit(worker.Job{Monitor: monitor.Monitor{ID: "mon-q-2"}, CurrentState: state.StateUnknown}); err != nil {
		t.Fatalf("Submit job 2 failed: %v", err)
	}

	// Job 3 fills queue slot 2 (queue now at capacity = 2)
	if err := pool.Submit(worker.Job{Monitor: monitor.Monitor{ID: "mon-q-3"}, CurrentState: state.StateUnknown}); err != nil {
		t.Fatalf("Submit job 3 failed: %v", err)
	}

	// Job 4 must be rejected with ErrQueueFull
	err = pool.Submit(worker.Job{Monitor: monitor.Monitor{ID: "mon-q-4"}, CurrentState: state.StateUnknown})
	if !errors.Is(err, worker.ErrQueueFull) {
		t.Fatalf("expected ErrQueueFull, got %v", err)
	}

	close(job1Block)
	pool.Stop()
	pool.Wait()
}

func TestPool_ShutdownDeterminism(t *testing.T) {
	runner := &fakeRunner{}
	pool, err := worker.NewPool(worker.Config{MaxConcurrency: 4}, runner)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	pool.Start(ctx)

	for i := 0; i < 10; i++ {
		_ = pool.Submit(worker.Job{
			Monitor:      monitor.Monitor{ID: fmt.Sprintf("mon-shut-%d", i)},
			CurrentState: state.StateUnknown,
		})
	}

	// Stop pool
	cancel()
	pool.Stop()

	// Wait must return deterministically without hanging
	done := make(chan struct{})
	go func() {
		pool.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Clean exit
	case <-time.After(3 * time.Second):
		t.Fatal("pool.Wait() deadlocked or took too long to return")
	}
}
