package benchmark_test

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
	"github.com/chavaliadi/watchdog/internal/scheduler"
	"github.com/chavaliadi/watchdog/internal/state"
	"github.com/chavaliadi/watchdog/internal/worker"
)

type mockCycleRunner struct {
	runFn func(ctx context.Context, m monitor.Monitor, current state.State) (scheduler.CycleResult, error)
}

func (m *mockCycleRunner) RunCycle(ctx context.Context, mon monitor.Monitor, current state.State) (scheduler.CycleResult, error) {
	if m.runFn != nil {
		return m.runFn(ctx, mon, current)
	}
	return scheduler.CycleResult{
		Monitor:     mon,
		CheckResult: checker.CheckResult{OK: true},
	}, nil
}

func TestWorkerPool_ScalingAndInvariants(t *testing.T) {
	ctx := context.Background()

	// 1. Invariant Verification: SAME MONITOR -> NO OVERLAPPING CHECKS
	t.Run("Invariant_SameMonitor_NoOverlap", func(t *testing.T) {
		var monitorInFlight int64
		var maxInFlight int64

		runner := &mockCycleRunner{
			runFn: func(ctx context.Context, m monitor.Monitor, current state.State) (scheduler.CycleResult, error) {
				in := atomic.AddInt64(&monitorInFlight, 1)
				// Track peak concurrency for this monitor
				for {
					currMax := atomic.LoadInt64(&maxInFlight)
					if in <= currMax || atomic.CompareAndSwapInt64(&maxInFlight, currMax, in) {
						break
					}
				}

				time.Sleep(5 * time.Millisecond)
				atomic.AddInt64(&monitorInFlight, -1)
				return scheduler.CycleResult{Monitor: m}, nil
			},
		}

		pool, err := worker.NewPool(worker.Config{MaxConcurrency: 10}, runner)
		if err != nil {
			t.Fatalf("failed to create pool: %v", err)
		}
		pool.Start(ctx)
		defer func() {
			pool.Stop()
			pool.Wait()
		}()

		sameMon := monitor.Monitor{ID: "same-monitor-123"}
		jobCount := 30
		resChans := make([]chan worker.Result, jobCount)

		for i := 0; i < jobCount; i++ {
			resCh := make(chan worker.Result, 1)
			resChans[i] = resCh
			err := pool.Submit(worker.Job{
				Monitor:    sameMon,
				ResultChan: resCh,
			})
			if err != nil {
				t.Fatalf("submit failed: %v", err)
			}
		}

		// Wait for all to complete
		for _, ch := range resChans {
			res := <-ch
			if res.Err != nil {
				t.Fatalf("job returned error: %v", res.Err)
			}
		}

		if maxInFlight != 1 {
			t.Fatalf("VIOLATION: same monitor had %d concurrent executions (expected strictly 1)", maxInFlight)
		}
		fmt.Printf("[WORKER POOL] Invariant Verified: Same monitor peak concurrency = %d (Serialized)\n", maxInFlight)
	})

	// 2. Invariant Verification: DIFFERENT MONITORS -> CONCURRENT EXECUTION OCCURS
	t.Run("Invariant_DifferentMonitors_Concurrent", func(t *testing.T) {
		numMonitors := 10
		var activeCount int64
		var maxConcurrent int64

		runner := &mockCycleRunner{
			runFn: func(ctx context.Context, m monitor.Monitor, current state.State) (scheduler.CycleResult, error) {
				in := atomic.AddInt64(&activeCount, 1)
				for {
					curr := atomic.LoadInt64(&maxConcurrent)
					if in <= curr || atomic.CompareAndSwapInt64(&maxConcurrent, curr, in) {
						break
					}
				}
				time.Sleep(40 * time.Millisecond)
				atomic.AddInt64(&activeCount, -1)
				return scheduler.CycleResult{Monitor: m}, nil
			},
		}

		pool, err := worker.NewPool(worker.Config{MaxConcurrency: 10}, runner)
		if err != nil {
			t.Fatalf("failed to create pool: %v", err)
		}
		pool.Start(ctx)
		defer func() {
			pool.Stop()
			pool.Wait()
		}()

		start := time.Now()
		var wg sync.WaitGroup
		for i := 0; i < numMonitors; i++ {
			wg.Add(1)
			mID := fmt.Sprintf("diff-mon-%d", i)
			go func(id string) {
				defer wg.Done()
				_, _ = pool.RunCycle(ctx, monitor.Monitor{ID: id}, state.StateHealthy)
			}(mID)
		}
		wg.Wait()
		elapsed := time.Since(start)

		if maxConcurrent < 5 {
			t.Fatalf("expected concurrent execution, but peak concurrency was only %d", maxConcurrent)
		}
		// If sequential, 10 * 40ms = 400ms. Since concurrent, should complete in ~40-80ms.
		if elapsed > 200*time.Millisecond {
			t.Fatalf("expected concurrent execution in <200ms, took %v", elapsed)
		}
		fmt.Printf("[WORKER POOL] Invariant Verified: Different monitors peak concurrency = %d / 10, Total Time = %v\n",
			maxConcurrent, elapsed)
	})

	// 3. Queue Capacity & Rejection Behavior
	t.Run("QueueCapacity_Rejections", func(t *testing.T) {
		queueCap := 5
		blockCh := make(chan struct{})
		runner := &mockCycleRunner{
			runFn: func(ctx context.Context, m monitor.Monitor, current state.State) (scheduler.CycleResult, error) {
				<-blockCh
				return scheduler.CycleResult{}, nil
			},
		}

		pool, err := worker.NewPool(worker.Config{
			MaxConcurrency: 1,
			QueueCapacity:  queueCap,
		}, runner)
		if err != nil {
			t.Fatalf("failed to create pool: %v", err)
		}
		pool.Start(ctx)
		defer func() {
			close(blockCh)
			pool.Stop()
			pool.Wait()
		}()

		// Submit 1 job to occupy the 1 worker
		_ = pool.Submit(worker.Job{Monitor: monitor.Monitor{ID: "mon-occupy"}})
		time.Sleep(5 * time.Millisecond) // ensure it's picked up

		rejected := 0
		accepted := 0
		for i := 0; i < 20; i++ {
			err := pool.Submit(worker.Job{Monitor: monitor.Monitor{ID: fmt.Sprintf("mon-fill-%d", i)}})
			if errors.Is(err, worker.ErrQueueFull) {
				rejected++
			} else if err == nil {
				accepted++
			}
		}

		fmt.Printf("[WORKER POOL] Queue Capacity=%d: Submitted=20, AcceptedInQueue=%d, RejectedErrQueueFull=%d\n",
			queueCap, accepted, rejected)
		if rejected == 0 {
			t.Fatalf("expected rejections when submitting 20 jobs to queue capacity %d", queueCap)
		}
	})

	// 4. Concurrency Sweep: 1, 5, 10, 25, 50 workers
	concurrencyLevels := []int{1, 5, 10, 25, 50}
	totalJobs := 500
	simulatedWorkDuration := 2 * time.Millisecond

	fmt.Println("\n=================== WORKER POOL SCALING SWEEP ===================")
	fmt.Printf("%-10s | %-12s | %-10s | %-10s | %-10s | %-10s | %-12s | %-12s\n",
		"Workers", "Throughput", "Avg Latency", "P50 Latency", "P95 Latency", "P99 Latency", "Peak Active", "Peak Queue")
	fmt.Println("------------------------------------------------------------------------------------------------------")

	for _, c := range concurrencyLevels {
		var activeCount int64
		var peakActive int64

		runner := &mockCycleRunner{
			runFn: func(ctx context.Context, m monitor.Monitor, current state.State) (scheduler.CycleResult, error) {
				in := atomic.AddInt64(&activeCount, 1)
				for {
					curr := atomic.LoadInt64(&peakActive)
					if in <= curr || atomic.CompareAndSwapInt64(&peakActive, curr, in) {
						break
					}
				}
				time.Sleep(simulatedWorkDuration)
				atomic.AddInt64(&activeCount, -1)
				return scheduler.CycleResult{Monitor: m}, nil
			},
		}

		pool, err := worker.NewPool(worker.Config{MaxConcurrency: c}, runner)
		if err != nil {
			t.Fatalf("create pool: %v", err)
		}
		pool.Start(ctx)

		latencies := make([]time.Duration, totalJobs)
		var peakQueue int64

		wallStart := time.Now()
		var wg sync.WaitGroup
		for j := 0; j < totalJobs; j++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				m := monitor.Monitor{ID: fmt.Sprintf("mon-scaled-%d", idx)}
				q := int64(pool.PendingCount())
				for {
					curr := atomic.LoadInt64(&peakQueue)
					if q <= curr || atomic.CompareAndSwapInt64(&peakQueue, curr, q) {
						break
					}
				}

				jStart := time.Now()
				_, _ = pool.RunCycle(ctx, m, state.StateHealthy)
				latencies[idx] = time.Since(jStart)
			}(j)
		}
		wg.Wait()
		wallElapsed := time.Since(wallStart)

		pool.Stop()
		pool.Wait()

		stats := CalculateStats(latencies, wallElapsed)
		fmt.Printf("%-10d | %-10.2f/s | %-11v | %-11v | %-11v | %-11v | %-12d | %-12d\n",
			c, stats.Throughput, stats.Avg, stats.P50, stats.P95, stats.P99, peakActive, peakQueue)
	}
	fmt.Println("==================================================================")
}
