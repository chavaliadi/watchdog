package worker

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/chavaliadi/watchdog/internal/monitor"
	"github.com/chavaliadi/watchdog/internal/scheduler"
	"github.com/chavaliadi/watchdog/internal/state"
)

var (
	// ErrInvalidConfig is returned when pool configuration parameters are invalid.
	ErrInvalidConfig = errors.New("invalid worker pool configuration")

	// ErrPoolNotStarted is returned when work is submitted before the pool has been started.
	ErrPoolNotStarted = errors.New("worker pool not started")

	// ErrPoolStopped is returned when work is submitted after the pool has been stopped.
	ErrPoolStopped = errors.New("worker pool stopped")

	// ErrQueueFull is returned when work is submitted to a pool whose queue is at capacity.
	ErrQueueFull = errors.New("worker pool job queue is full")
)

// Config defines configuration parameters for a bounded worker pool.
type Config struct {
	// MaxConcurrency is the maximum number of worker goroutines executing cycles simultaneously.
	// Must be greater than zero.
	MaxConcurrency int

	// QueueCapacity is the maximum number of pending jobs allowed in the submission queue.
	// If QueueCapacity <= 0, the queue is unbounded.
	QueueCapacity int
}

// Job represents an individual unit of monitoring cycle work.
type Job struct {
	Monitor      monitor.Monitor
	CurrentState state.State

	// ResultChan is an optional channel where the completion Result will be delivered.
	// Callers should typically buffer this channel by at least 1.
	ResultChan chan<- Result
}

// Result represents the outcome of executing a Job.
type Result struct {
	MonitorID   string
	CycleResult scheduler.CycleResult
	Err         error
}

// workerTask wraps a Job with its execution context for a worker.
type workerTask struct {
	job Job
	ctx context.Context
}

// workerCompletion is sent by a worker to the coordinator upon completing a task.
type workerCompletion struct {
	job workerTask
	res Result
}

// submitReq encapsulates a Job submission request to the coordinator.
type submitReq struct {
	job   Job
	errCh chan error
}

// Pool provides bounded concurrent execution of monitoring cycles.
type Pool struct {
	cfg    Config
	runner scheduler.CycleRunner

	mu      sync.RWMutex
	started bool
	stopped bool

	ctx    context.Context
	cancel context.CancelFunc

	submitCh chan submitReq
	taskCh   chan workerTask
	doneCh   chan workerCompletion

	workersWg sync.WaitGroup
	coordWg   sync.WaitGroup
	stopOnce  sync.Once

	activeCount  int64
	pendingCount int64
}

// NewPool constructs and validates a new worker pool.
func NewPool(cfg Config, runner scheduler.CycleRunner) (*Pool, error) {
	if cfg.MaxConcurrency <= 0 {
		return nil, fmt.Errorf("%w: MaxConcurrency must be > 0 (got %d)", ErrInvalidConfig, cfg.MaxConcurrency)
	}
	if runner == nil {
		return nil, fmt.Errorf("%w: CycleRunner cannot be nil", ErrInvalidConfig)
	}

	return &Pool{
		cfg:      cfg,
		runner:   runner,
		submitCh: make(chan submitReq),
		taskCh:   make(chan workerTask),
		doneCh:   make(chan workerCompletion),
	}, nil
}

// Start initializes the pool and starts the coordinator and worker goroutines.
func (p *Pool) Start(ctx context.Context) {
	p.mu.Lock()
	if p.started {
		p.mu.Unlock()
		return
	}
	p.started = true
	p.ctx, p.cancel = context.WithCancel(ctx)
	p.mu.Unlock()

	// Start N worker goroutines
	p.workersWg.Add(p.cfg.MaxConcurrency)
	for i := 0; i < p.cfg.MaxConcurrency; i++ {
		go p.workerLoop(i)
	}

	// Start coordinator goroutine
	p.coordWg.Add(1)
	go p.coordinatorLoop()
}

// Submit submits a job to the worker pool for execution.
func (p *Pool) Submit(job Job) error {
	p.mu.RLock()
	started := p.started
	stopped := p.stopped
	p.mu.RUnlock()

	if !started {
		return ErrPoolNotStarted
	}
	if stopped {
		return ErrPoolStopped
	}
	if job.Monitor.ID == "" {
		return errors.New("cannot submit job with empty monitor ID")
	}

	errCh := make(chan error, 1)
	req := submitReq{
		job:   job,
		errCh: errCh,
	}

	select {
	case <-p.ctx.Done():
		return p.ctx.Err()
	case p.submitCh <- req:
	}

	select {
	case <-p.ctx.Done():
		return p.ctx.Err()
	case err := <-errCh:
		return err
	}
}

// SubmitWait submits a job and blocks until the cycle finishes, returning the CycleResult or error.
func (p *Pool) SubmitWait(ctx context.Context, job Job) (scheduler.CycleResult, error) {
	resCh := make(chan Result, 1)
	job.ResultChan = resCh

	if err := p.Submit(job); err != nil {
		return scheduler.CycleResult{}, err
	}

	select {
	case <-ctx.Done():
		return scheduler.CycleResult{}, ctx.Err()
	case <-p.ctx.Done():
		return scheduler.CycleResult{}, p.ctx.Err()
	case res := <-resCh:
		return res.CycleResult, res.Err
	}
}

// RunCycle executes a cycle through the worker pool by submitting a Job and waiting for its result.
// It allows *Pool to directly satisfy scheduler.CycleRunner without an intermediate adapter.
func (p *Pool) RunCycle(ctx context.Context, m monitor.Monitor, current state.State) (scheduler.CycleResult, error) {
	return p.SubmitWait(ctx, Job{
		Monitor:      m,
		CurrentState: current,
	})
}

// Stop initiates graceful shutdown of the worker pool and cancels running jobs.
func (p *Pool) Stop() {
	p.stopOnce.Do(func() {
		p.mu.Lock()
		p.stopped = true
		p.mu.Unlock()

		if p.cancel != nil {
			p.cancel()
		}
	})
}

// Wait blocks until all coordinator and worker goroutines have exited.
func (p *Pool) Wait() {
	p.coordWg.Wait()
	p.workersWg.Wait()
}

// ActiveCount returns the number of workers currently executing a cycle.
func (p *Pool) ActiveCount() int {
	return int(atomic.LoadInt64(&p.activeCount))
}

// PendingCount returns the number of jobs currently queued and awaiting an available worker.
func (p *Pool) PendingCount() int {
	return int(atomic.LoadInt64(&p.pendingCount))
}

func (p *Pool) workerLoop(workerID int) {
	defer p.workersWg.Done()

	for task := range p.taskCh {
		atomic.AddInt64(&p.activeCount, 1)
		res, err := p.runner.RunCycle(task.ctx, task.job.Monitor, task.job.CurrentState)
		atomic.AddInt64(&p.activeCount, -1)

		p.doneCh <- workerCompletion{
			job: task,
			res: Result{
				MonitorID:   task.job.Monitor.ID,
				CycleResult: res,
				Err:         err,
			},
		}
	}
}

func (p *Pool) coordinatorLoop() {
	defer p.coordWg.Done()

	pending := make([]Job, 0)
	runningMonitors := make(map[string]struct{})
	activeWorkers := 0
	maxWorkers := p.cfg.MaxConcurrency

	trySchedule := func() {
		if activeWorkers >= maxWorkers || len(pending) == 0 {
			return
		}

		for i := 0; i < len(pending); {
			if activeWorkers >= maxWorkers {
				break
			}

			job := pending[i]
			if _, running := runningMonitors[job.Monitor.ID]; running {
				// Same-monitor serialization: monitor is currently running on another worker.
				// Skip this job, but keep checking remaining pending jobs to prevent head-of-line blocking!
				i++
				continue
			}

			// Job is runnable. Remove from pending slice.
			pending = append(pending[:i], pending[i+1:]...)
			atomic.AddInt64(&p.pendingCount, -1)

			runningMonitors[job.Monitor.ID] = struct{}{}
			activeWorkers++

			// Dispatch to worker. Since activeWorkers <= maxWorkers, at least one worker is waiting on taskCh.
			p.taskCh <- workerTask{job: job, ctx: p.ctx}
		}
	}

	for {
		select {
		case <-p.ctx.Done():
			// Context canceled: reject queued jobs
			for _, job := range pending {
				if job.ResultChan != nil {
					job.ResultChan <- Result{
						MonitorID: job.Monitor.ID,
						Err:       p.ctx.Err(),
					}
				}
			}
			atomic.AddInt64(&p.pendingCount, -int64(len(pending)))
			pending = nil

			// Drain completions from in-flight workers
			for activeWorkers > 0 {
				comp := <-p.doneCh
				activeWorkers--
				delete(runningMonitors, comp.job.job.Monitor.ID)
				if comp.job.job.ResultChan != nil {
					comp.job.job.ResultChan <- comp.res
				}
			}

			// Close task channel so workers terminate
			close(p.taskCh)
			return

		case req := <-p.submitCh:
			if p.cfg.QueueCapacity > 0 && len(pending) >= p.cfg.QueueCapacity {
				req.errCh <- ErrQueueFull
				continue
			}

			pending = append(pending, req.job)
			atomic.AddInt64(&p.pendingCount, 1)
			req.errCh <- nil
			trySchedule()

		case comp := <-p.doneCh:
			activeWorkers--
			delete(runningMonitors, comp.job.job.Monitor.ID)
			if comp.job.job.ResultChan != nil {
				comp.job.job.ResultChan <- comp.res
			}
			trySchedule()
		}
	}
}
