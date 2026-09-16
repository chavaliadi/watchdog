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
)

// MultiScheduler coordinates recurring, concurrent monitoring cycles for multiple monitors
// using independent per-monitor scheduling loops delegated to a CycleRunner (such as *worker.Pool).
type MultiScheduler struct {
	repo    persistence.Repository
	runner  CycleRunner
	onError func(monitorID string, err error)

	mu      sync.RWMutex
	started bool
	stopped bool

	ctx       context.Context
	cancel    context.CancelFunc
	runnersWg sync.WaitGroup

	activeRunnersCount int
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
		repo:   repo,
		runner: runner,
	}, nil
}

// WithErrorHandler registers a callback for observing non-fatal per-monitor operational errors.
// If nil, errors are logged via the standard library logger.
func (s *MultiScheduler) WithErrorHandler(fn func(monitorID string, err error)) *MultiScheduler {
	s.onError = fn
	return s
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
	s.activeRunnersCount = len(targets)
	s.mu.Unlock()

	for _, t := range targets {
		runner := &monitorRunner{
			monitor:      t.m,
			currentState: t.st,
			runner:       s.runner,
			onError:      s.onError,
		}
		s.runnersWg.Add(1)
		go func(r *monitorRunner) {
			defer s.runnersWg.Done()
			r.run(s.ctx)
		}(runner)
	}

	return nil
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
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.activeRunnersCount
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
