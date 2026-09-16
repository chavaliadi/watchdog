package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/chavaliadi/watchdog/internal/monitor"
	"github.com/chavaliadi/watchdog/internal/persistence"
	"github.com/chavaliadi/watchdog/internal/state"
)

var (
	// ErrMultiSchedulerNotStarted is returned when operations require a started MultiScheduler.
	ErrMultiSchedulerNotStarted = errors.New("multi-scheduler not started")

	// ErrMultiSchedulerStopped is returned when operations are attempted on a stopped MultiScheduler.
	ErrMultiSchedulerStopped = errors.New("multi-scheduler stopped")

	// ErrRunnerAlreadyExists is returned when attempting to start a runner that is already running.
	ErrRunnerAlreadyExists = errors.New("runner already exists")
)

type activeRunner struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// MultiScheduler coordinates recurring, concurrent monitoring cycles for multiple monitors
// using independent per-monitor scheduling loops delegated to a CycleRunner (such as *worker.Pool).
type MultiScheduler struct {
	repo    persistence.Repository
	runner  CycleRunner
	onError func(monitorID string, err error)

	mu      sync.Mutex
	started bool
	stopped bool

	ctx       context.Context
	cancel    context.CancelFunc
	runnersWg sync.WaitGroup

	runners  map[string]*activeRunner
	locksMu  sync.Mutex
	monLocks map[string]*sync.Mutex
}

// NewMultiScheduler constructs a new MultiScheduler.
func NewMultiScheduler(repo persistence.Repository, runner CycleRunner) (*MultiScheduler, error) {
	if repo == nil {
		return nil, errors.New("nil repository provided to MultiScheduler")
	}
	if runner == nil {
		return nil, errors.New("nil runner provided to MultiScheduler")
	}

	return &MultiScheduler{
		repo:     repo,
		runner:   runner,
		runners:  make(map[string]*activeRunner),
		monLocks: make(map[string]*sync.Mutex),
	}, nil
}

// WithErrorHandler registers a callback for observing non-fatal per-monitor operational errors.
// If nil, errors are logged via the standard library logger.
func (s *MultiScheduler) WithErrorHandler(fn func(monitorID string, err error)) *MultiScheduler {
	s.onError = fn
	return s
}

func (s *MultiScheduler) getMonitorLock(id string) *sync.Mutex {
	s.locksMu.Lock()
	defer s.locksMu.Unlock()
	mLock, ok := s.monLocks[id]
	if !ok {
		mLock = &sync.Mutex{}
		s.monLocks[id] = mLock
	}
	return mLock
}

// Start discovers all monitors from the repository, filters enabled monitors, loads their
// initial state, and launches an independent scheduling runner for each enabled monitor.
func (s *MultiScheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return nil
	}
	if s.stopped {
		s.mu.Unlock()
		return ErrMultiSchedulerStopped
	}
	s.started = true
	s.ctx, s.cancel = context.WithCancel(ctx)
	s.mu.Unlock()

	// 1. Initial Monitor Discovery
	allMonitors, err := s.repo.ListMonitors(s.ctx)
	if err != nil {
		s.Stop()
		return fmt.Errorf("multi-scheduler discover monitors: %w", err)
	}

	// 2. Filter enabled monitors
	enabledMonitors := make([]monitor.Monitor, 0, len(allMonitors))
	for _, m := range allMonitors {
		if m.Enabled {
			enabledMonitors = append(enabledMonitors, m)
		}
	}

	// 3. Hydrate initial state for each enabled monitor
	type runnerTarget struct {
		m  monitor.Monitor
		st state.State
	}
	targets := make([]runnerTarget, 0, len(enabledMonitors))

	for _, m := range enabledMonitors {
		st, err := s.repo.GetState(s.ctx, m.ID)
		if err != nil {
			s.Stop()
			return fmt.Errorf("multi-scheduler load state for monitor %q: %w", m.ID, err)
		}
		targets = append(targets, runnerTarget{m: m, st: st})
	}

	// 4. Launch per-monitor runner loops
	s.mu.Lock()
	for _, t := range targets {
		runnerCtx, runnerCancel := context.WithCancel(s.ctx)
		done := make(chan struct{})

		runner := &monitorRunner{
			monitor:      t.m,
			currentState: t.st,
			runner:       s.runner,
			onError:      s.onError,
		}

		s.runners[t.m.ID] = &activeRunner{
			cancel: runnerCancel,
			done:   done,
		}
		s.runnersWg.Add(1)

		go func(r *monitorRunner, rCtx context.Context, d chan struct{}) {
			defer s.runnersWg.Done()
			defer close(d)
			r.run(rCtx)
		}(runner, runnerCtx, done)
	}
	s.mu.Unlock()

	return nil
}

// stopRunner terminates the runner for id if one exists and waits until it completes.
// Assumes per-monitor lock is already held by the caller.
func (s *MultiScheduler) stopRunner(id string) {
	s.mu.Lock()
	ar, exists := s.runners[id]
	if !exists {
		s.mu.Unlock()
		return
	}
	delete(s.runners, id)
	s.mu.Unlock()

	ar.cancel()
	<-ar.done
}

// StartMonitor starts a new runner for an enabled monitor.
// Returns ErrRunnerAlreadyExists if a runner for this monitor is already active.
func (s *MultiScheduler) StartMonitor(ctx context.Context, m monitor.Monitor) error {
	mLock := s.getMonitorLock(m.ID)
	mLock.Lock()
	defer mLock.Unlock()

	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return ErrMultiSchedulerNotStarted
	}
	if s.stopped {
		s.mu.Unlock()
		return ErrMultiSchedulerStopped
	}
	if _, exists := s.runners[m.ID]; exists {
		s.mu.Unlock()
		return ErrRunnerAlreadyExists
	}
	s.mu.Unlock()

	if !m.Enabled {
		return errors.New("cannot start disabled monitor")
	}

	st, err := s.repo.GetState(ctx, m.ID)
	if err != nil {
		return fmt.Errorf("load state for monitor %q: %w", m.ID, err)
	}

	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return ErrMultiSchedulerStopped
	}
	if _, exists := s.runners[m.ID]; exists {
		s.mu.Unlock()
		return ErrRunnerAlreadyExists
	}

	runnerCtx, runnerCancel := context.WithCancel(s.ctx)
	done := make(chan struct{})

	runner := &monitorRunner{
		monitor:      m,
		currentState: st,
		runner:       s.runner,
		onError:      s.onError,
	}

	s.runners[m.ID] = &activeRunner{
		cancel: runnerCancel,
		done:   done,
	}
	s.runnersWg.Add(1)
	s.mu.Unlock()

	go func() {
		defer s.runnersWg.Done()
		defer close(done)
		runner.run(runnerCtx)
	}()

	return nil
}

// StopMonitor stops any active runner for id and waits synchronously for its termination.
// If no runner exists, StopMonitor returns nil without error.
func (s *MultiScheduler) StopMonitor(ctx context.Context, id string) error {
	mLock := s.getMonitorLock(id)
	mLock.Lock()
	defer mLock.Unlock()

	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return ErrMultiSchedulerNotStarted
	}
	s.mu.Unlock()

	s.stopRunner(id)
	return nil
}

// UpdateMonitor reconciles the scheduler for a monitor whose configuration or enabled state changed.
// It stops the existing runner (if any), waits for its termination, and if enabled starts a new runner
// with freshly loaded authoritative state from the repository.
func (s *MultiScheduler) UpdateMonitor(ctx context.Context, m monitor.Monitor) error {
	mLock := s.getMonitorLock(m.ID)
	mLock.Lock()
	defer mLock.Unlock()

	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return ErrMultiSchedulerNotStarted
	}
	if s.stopped {
		s.mu.Unlock()
		return ErrMultiSchedulerStopped
	}
	s.mu.Unlock()

	// 1. Stop old runner if present and wait synchronously for termination
	s.stopRunner(m.ID)

	// 2. If enabled, start new runner with authoritative state
	if m.Enabled {
		st, err := s.repo.GetState(ctx, m.ID)
		if err != nil {
			return fmt.Errorf("load state for monitor %q: %w", m.ID, err)
		}

		s.mu.Lock()
		if s.stopped {
			s.mu.Unlock()
			return ErrMultiSchedulerStopped
		}

		runnerCtx, runnerCancel := context.WithCancel(s.ctx)
		done := make(chan struct{})

		runner := &monitorRunner{
			monitor:      m,
			currentState: st,
			runner:       s.runner,
			onError:      s.onError,
		}

		s.runners[m.ID] = &activeRunner{
			cancel: runnerCancel,
			done:   done,
		}
		s.runnersWg.Add(1)
		s.mu.Unlock()

		go func() {
			defer s.runnersWg.Done()
			defer close(done)
			runner.run(runnerCtx)
		}()
	}

	return nil
}

// HasRunner returns whether an active runner currently exists for id.
func (s *MultiScheduler) HasRunner(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, exists := s.runners[id]
	return exists
}

// Stop initiates graceful shutdown of all monitor runners.
func (s *MultiScheduler) Stop() {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.stopped = true
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
}

// Wait blocks until all monitor runners have completely exited.
func (s *MultiScheduler) Wait() {
	s.runnersWg.Wait()
}

// Run starts the MultiScheduler and blocks until the context is canceled and all runners exit.
func (s *MultiScheduler) Run(ctx context.Context) error {
	if err := s.Start(ctx); err != nil {
		return err
	}
	s.Wait()
	if s.ctx != nil && s.ctx.Err() != nil {
		return s.ctx.Err()
	}
	return nil
}

// ActiveRunners returns the number of active monitor runner loops managed by the scheduler.
func (s *MultiScheduler) ActiveRunners() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.runners)
}

// monitorRunner manages the independent recurring fixed-delay lifecycle for a single monitor.
// Each runner strictly owns its monitor's authoritative currentState locally on its goroutine stack.
type monitorRunner struct {
	monitor      monitor.Monitor
	currentState state.State
	runner       CycleRunner
	onError      func(monitorID string, err error)
}

func (r *monitorRunner) run(ctx context.Context) {
	interval := r.monitor.Interval
	if interval <= 0 {
		interval = 60 * time.Second
	}

	executeCycle := func() {
		cycleRes, err := r.runner.RunCycle(ctx, r.monitor, r.currentState)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if r.onError != nil {
				r.onError(r.monitor.ID, err)
			} else {
				log.Printf("multi-scheduler: cycle error for monitor %q: %v", r.monitor.ID, err)
			}
			// Invariant: On operational error, previous currentState remains authoritative.
			return
		}

		// Invariant: On success, NextState becomes the authoritative local state for subsequent cycles.
		r.currentState = cycleRes.TransitionResult.NextState
	}

	// 1. Immediate initial execution (Phase 4A semantics)
	executeCycle()
	if ctx.Err() != nil {
		return
	}

	// 2. Fixed-delay recurring execution
	timer := time.NewTimer(interval)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			executeCycle()
			if ctx.Err() != nil {
				return
			}
			// Fixed-delay: reset timer only after cycle completes
			timer.Reset(interval)
		}
	}
}
