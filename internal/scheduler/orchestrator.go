package scheduler

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/chavaliadi/watchdog/internal/checker"
	"github.com/chavaliadi/watchdog/internal/monitor"
	"github.com/chavaliadi/watchdog/internal/persistence"
	"github.com/chavaliadi/watchdog/internal/retry"
	"github.com/chavaliadi/watchdog/internal/state"
	"github.com/chavaliadi/watchdog/internal/telemetry"
)

// CycleResult represents the outcome of one completed monitoring cycle.
type CycleResult struct {
	Monitor          monitor.Monitor
	CheckResult      checker.CheckResult
	TransitionResult state.TransitionResult
}

// Orchestrator coordinates the execution of a single monitoring cycle.
type Orchestrator struct {
	httpRetrier checker.Checker
	tcpRetrier  checker.Checker
	repository  persistence.Repository
	recorder    telemetry.Recorder
}

// NewOrchestrator creates a new Orchestrator with the supplied checkers, retry configuration,
// and optional repository.
// If httpChecker or tcpChecker is nil, standard default implementations are used.
// Each checker is wrapped exactly once with the retry layer.
func NewOrchestrator(
	httpChecker checker.Checker,
	tcpChecker checker.Checker,
	retryCfg retry.Config,
	repo ...persistence.Repository,
) *Orchestrator {
	if httpChecker == nil {
		httpChecker = checker.NewHTTPChecker(nil)
	}
	if tcpChecker == nil {
		tcpChecker = checker.NewTCPChecker(nil)
	}

	var r persistence.Repository
	if len(repo) > 0 {
		r = repo[0]
	}

	return &Orchestrator{
		httpRetrier: retry.New(httpChecker, retryCfg),
		tcpRetrier:  retry.New(tcpChecker, retryCfg),
		repository:  r,
		recorder:    telemetry.NoopRecorder{},
	}
}

// WithRecorder configures the telemetry metrics recorder on the Orchestrator.
func (o *Orchestrator) WithRecorder(r telemetry.Recorder) *Orchestrator {
	if r == nil {
		r = telemetry.NoopRecorder{}
	}
	o.recorder = r
	return o
}

// RunCycle coordinates checking, retrying, state evaluation, and optional persistence
// for a single monitor.
//
// The caller is responsible for supplying the authoritative current persisted state of
// the monitor (e.g. obtained from Repository.GetState). If a repository is configured,
// the check result and transitioned next state are persisted atomically via SaveCycle.
// If persistence fails, the error is returned and no successful CycleResult is returned.
func (o *Orchestrator) RunCycle(
	ctx context.Context,
	m monitor.Monitor,
	current state.State,
) (CycleResult, error) {
	var rec telemetry.Recorder = telemetry.NoopRecorder{}
	if o != nil && o.recorder != nil {
		rec = o.recorder
	}

	if ctx == nil {
		rec.RecordCycleError("run_cycle")
		return CycleResult{}, errors.New("nil context provided to Orchestrator")
	}

	if err := ctx.Err(); err != nil {
		rec.RecordCycleError("run_cycle")
		return CycleResult{}, err
	}

	if o == nil || o.httpRetrier == nil || o.tcpRetrier == nil {
		rec.RecordCycleError("run_cycle")
		return CycleResult{}, errors.New("uninitialized Orchestrator")
	}

	var retrier checker.Checker
	switch m.Kind {
	case monitor.KindHTTP:
		retrier = o.httpRetrier
	case monitor.KindTCP:
		retrier = o.tcpRetrier
	default:
		rec.RecordCycleError("run_cycle")
		return CycleResult{}, fmt.Errorf("unsupported monitor kind %q", m.Kind)
	}

	start := time.Now()
	checkResult, err := retrier.Check(ctx, m)
	duration := time.Since(start)
	if err != nil {
		rec.RecordCycleError("run_cycle")
		return CycleResult{}, err
	}

	rec.RecordCheck(string(m.Kind), checkResult.OK, string(checkResult.ErrorClass), duration)

	transitionResult, err := state.TransitionFromCheckResult(current, checkResult)
	if err != nil {
		rec.RecordCycleError("run_cycle")
		return CycleResult{}, err
	}

	if o.repository != nil {
		if err := o.repository.SaveCycle(ctx, m.ID, checkResult, transitionResult.NextState); err != nil {
			rec.RecordCycleError("save_cycle")
			return CycleResult{}, fmt.Errorf("save cycle for monitor %q: %w", m.ID, err)
		}
	}

	return CycleResult{
		Monitor:          m,
		CheckResult:      checkResult,
		TransitionResult: transitionResult,
	}, nil
}
