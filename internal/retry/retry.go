package retry

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/chavaliadi/watchdog/internal/checker"
	"github.com/chavaliadi/watchdog/internal/monitor"
)

// Default Phase 1 implementation parameters.
const (
	defaultMaxAttempts = 3
	defaultBaseDelay   = 100 * time.Millisecond
	defaultMaxDelay    = 2 * time.Second
)

// Sleeper abstracts context-aware waiting between retry attempts, enabling deterministic tests.
type Sleeper func(ctx context.Context, d time.Duration) error

// JitterFn abstracts jitter calculation, enabling deterministic tests.
type JitterFn func(base time.Duration) time.Duration

// Config configures the retry policy.
type Config struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
	Sleeper     Sleeper
	JitterFn    JitterFn
}

// Retrier wraps a checker.Checker with exponential backoff and jitter.
type Retrier struct {
	checker     checker.Checker
	maxAttempts int
	baseDelay   time.Duration
	maxDelay    time.Duration
	sleeper     Sleeper
	jitterFn    JitterFn
}

// Ensure Retrier satisfies the Checker interface at compile time.
var _ checker.Checker = (*Retrier)(nil)

// New creates a new Retrier wrapping the provided Checker.
// Unset or non-positive configuration parameters fall back to Phase 1 defaults.
func New(c checker.Checker, cfg Config) *Retrier {
	maxAttempts := cfg.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = defaultMaxAttempts
	}

	baseDelay := cfg.BaseDelay
	if baseDelay <= 0 {
		baseDelay = defaultBaseDelay
	}

	maxDelay := cfg.MaxDelay
	if maxDelay <= 0 {
		maxDelay = defaultMaxDelay
	}

	sleeper := cfg.Sleeper
	if sleeper == nil {
		sleeper = defaultSleep
	}

	jitterFn := cfg.JitterFn
	if jitterFn == nil {
		jitterFn = defaultJitter
	}

	return &Retrier{
		checker:     c,
		maxAttempts: maxAttempts,
		baseDelay:   baseDelay,
		maxDelay:    maxDelay,
		sleeper:     sleeper,
		jitterFn:    jitterFn,
	}
}

// Check executes the wrapped check with retry logic, satisfying checker.Checker.
func (r *Retrier) Check(ctx context.Context, m monitor.Monitor) (checker.CheckResult, error) {
	if ctx == nil {
		return checker.CheckResult{}, errors.New("nil context provided to Retrier")
	}
	if r == nil || r.checker == nil {
		return checker.CheckResult{}, errors.New("nil checker in Retrier")
	}

	var lastResult checker.CheckResult

	for attempt := 1; attempt <= r.maxAttempts; attempt++ {
		// Check for context cancellation before starting each attempt.
		if err := ctx.Err(); err != nil {
			return checker.CheckResult{}, err
		}

		res, err := r.checker.Check(ctx, m)
		if err != nil {
			// Configuration or system errors are returned immediately without retry.
			return checker.CheckResult{}, err
		}

		res.AttemptCount = attempt
		lastResult = res

		// If the check was successful, return immediately.
		if res.OK {
			return res, nil
		}

		// If this was the last attempt, do not back off; return the failed result.
		if attempt == r.maxAttempts {
			break
		}

		// Calculate exponential backoff before the next attempt.
		shift := attempt - 1
		delay := r.baseDelay
		if shift > 0 {
			if shift >= 30 {
				delay = r.maxDelay
			} else {
				delay = r.baseDelay * (1 << shift)
			}
		}
		if delay > r.maxDelay {
			delay = r.maxDelay
		}

		sleepDuration := r.jitterFn(delay)
		if err := r.sleeper(ctx, sleepDuration); err != nil {
			return checker.CheckResult{}, err
		}
	}

	return lastResult, nil
}

func defaultSleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func defaultJitter(base time.Duration) time.Duration {
	if base <= 0 {
		return 0
	}
	return time.Duration(rand.Int64N(int64(base)))
}
