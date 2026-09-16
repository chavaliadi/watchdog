package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/chavaliadi/watchdog/internal/monitor"
	"github.com/chavaliadi/watchdog/internal/state"
)

// CycleRunner defines the contract for executing a single monitoring cycle.
// *Orchestrator implements CycleRunner.
type CycleRunner interface {
	RunCycle(ctx context.Context, m monitor.Monitor, current state.State) (CycleResult, error)
}

// Scheduler coordinates recurring, sequential execution of monitoring cycles for a monitor.
type Scheduler struct {
	runner  CycleRunner
	onError func(err error)
}

// NewScheduler constructs a new recurring Scheduler using the provided CycleRunner.
func NewScheduler(runner CycleRunner) *Scheduler {
	return &Scheduler{
		runner: runner,
	}
}

// WithErrorHandler attaches a callback for observing non-fatal cycle execution errors.
// If not configured, cycle errors are logged via standard library log.
func (s *Scheduler) WithErrorHandler(fn func(err error)) *Scheduler {
	s.onError = fn
	return s
}

// Run starts the recurring monitoring loop for a target monitor:
// 1. Validates the interval (must be > 0).
// 2. Executes the first cycle immediately.
// 3. Waits for the configured interval before executing each subsequent cycle.
//
// State handling:
// Between successful cycles, the scheduler tracks currentState in-memory from
// TransitionResult.NextState. If a cycle fails, currentState remains unchanged
// because the failed cycle was not committed to persistence.
//
// Overlap prevention:
// Cycles execute sequentially on the calling goroutine. If a cycle takes longer
// than the interval, subsequent cycles never execute concurrently. The next interval
// is timed from the completion of the previous cycle.
//
// Error semantics:
// Non-fatal operational cycle errors (checker or persistence failures) are observed
// via the error handler / logging, and scheduling continues for future intervals.
// Fatal errors (e.g. context cancellation or invalid configuration) terminate Run.
func (s *Scheduler) Run(
	ctx context.Context,
	m monitor.Monitor,
	currentState state.State,
	interval time.Duration,
) error {
	if s == nil || s.runner == nil {
		return errors.New("uninitialized Scheduler")
	}
	if ctx == nil {
		return errors.New("nil context provided to Scheduler")
	}
	if interval <= 0 {
		return fmt.Errorf("invalid interval %v: must be greater than zero", interval)
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	runCycle := func() {
		res, err := s.runner.RunCycle(ctx, m, currentState)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if s.onError != nil {
				s.onError(err)
			} else {
				log.Printf("scheduler: cycle error for monitor %q: %v", m.ID, err)
			}
			return
		}
		currentState = res.TransitionResult.NextState
	}

	// 1. Immediate first cycle
	runCycle()
	if err := ctx.Err(); err != nil {
		return err
	}

	// 2. Subsequent recurring cycles
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			runCycle()
			if err := ctx.Err(); err != nil {
				return err
			}
			// Reset ticker to ensure a full interval between the completion of this cycle
			// and the start of the next cycle.
			ticker.Reset(interval)
		}
	}
}
