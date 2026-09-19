package api_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chavaliadi/watchdog/internal/api"
	"github.com/chavaliadi/watchdog/internal/monitor"
	"github.com/chavaliadi/watchdog/internal/service"
)

type mockDBChecker struct {
	pingFn func(ctx context.Context) error
	calls  atomic.Int64
}

func (m *mockDBChecker) PingContext(ctx context.Context) error {
	m.calls.Add(1)
	if m.pingFn != nil {
		return m.pingFn(ctx)
	}
	return nil
}

type mockSchedulerChecker struct {
	running atomic.Bool
	calls   atomic.Int64
}

func (m *mockSchedulerChecker) IsRunning() bool {
	m.calls.Add(1)
	return m.running.Load()
}

func (m *mockSchedulerChecker) StartMonitor(ctx context.Context, mon monitor.Monitor) error { return nil }
func (m *mockSchedulerChecker) StopMonitor(ctx context.Context, id string) error           { return nil }
func (m *mockSchedulerChecker) UpdateMonitor(ctx context.Context, mon monitor.Monitor) error { return nil }

func TestLivez(t *testing.T) {
	// /livez must be shallow, deterministic, and work even with nil dependencies
	handlers := api.NewHandlers(nil)
	srv := api.NewServer(api.Config{}, handlers)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/livez")
	if err != nil {
		t.Fatalf("GET /livez failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}
	if string(body) != "ok\n" {
		t.Errorf("expected body 'ok\\n', got %q", string(body))
	}
}

func TestReadyz_Healthy(t *testing.T) {
	db := &mockDBChecker{}
	sched := &mockSchedulerChecker{}
	sched.running.Store(true)
	drain := &api.DrainTracker{}

	handlers := api.NewHandlers(nil, api.WithHealthChecks(db, sched, drain))
	srv := api.NewServer(api.Config{}, handlers)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}
	if string(body) != "ready\n" {
		t.Errorf("expected body 'ready\\n', got %q", string(body))
	}
}

func TestReadyz_DrainingTakesPrecedence(t *testing.T) {
	db := &mockDBChecker{}
	sched := &mockSchedulerChecker{}
	sched.running.Store(true)
	drain := &api.DrainTracker{}
	drain.SetDraining() // Marked as draining

	handlers := api.NewHandlers(nil, api.WithHealthChecks(db, sched, drain))
	srv := api.NewServer(api.Config{}, handlers)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected status 503 when draining, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}
	if string(body) != "not ready\n" {
		t.Errorf("expected body 'not ready\\n', got %q", string(body))
	}

	// Invariant: Draining must return 503 immediately without touching DB or scheduler
	if db.calls.Load() != 0 {
		t.Errorf("expected 0 DB calls when draining, got %d", db.calls.Load())
	}
	if sched.calls.Load() != 0 {
		t.Errorf("expected 0 scheduler calls when draining, got %d", sched.calls.Load())
	}
}

func TestReadyz_SchedulerNotReady(t *testing.T) {
	db := &mockDBChecker{}
	sched := &mockSchedulerChecker{}
	sched.running.Store(false) // Scheduler not ready
	drain := &api.DrainTracker{}

	handlers := api.NewHandlers(nil, api.WithHealthChecks(db, sched, drain))
	srv := api.NewServer(api.Config{}, handlers)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected status 503 when scheduler is not running, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}
	if string(body) != "not ready\n" {
		t.Errorf("expected body 'not ready\\n', got %q", string(body))
	}

	// Scheduler failure should exit before doing DB ping
	if db.calls.Load() != 0 {
		t.Errorf("expected 0 DB calls when scheduler is not running, got %d", db.calls.Load())
	}
}

func TestReadyz_DBFailure(t *testing.T) {
	db := &mockDBChecker{
		pingFn: func(ctx context.Context) error {
			return errors.New("connection to server on socket failed")
		},
	}
	sched := &mockSchedulerChecker{}
	sched.running.Store(true)
	drain := &api.DrainTracker{}

	handlers := api.NewHandlers(nil, api.WithHealthChecks(db, sched, drain))
	srv := api.NewServer(api.Config{}, handlers)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected status 503 when DB fails, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}
	// Never leak raw error to client
	if string(body) != "not ready\n" {
		t.Errorf("expected body 'not ready\\n', got %q", string(body))
	}
}

func TestReadyz_DBTimeout(t *testing.T) {
	db := &mockDBChecker{
		pingFn: func(ctx context.Context) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(2 * time.Second): // Exceeds 1s readiness timeout
				return nil
			}
		},
	}
	sched := &mockSchedulerChecker{}
	sched.running.Store(true)
	drain := &api.DrainTracker{}

	handlers := api.NewHandlers(nil, api.WithHealthChecks(db, sched, drain))
	srv := api.NewServer(api.Config{}, handlers)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	start := time.Now()
	resp, err := http.Get(ts.URL + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz failed: %v", err)
	}
	defer resp.Body.Close()
	elapsed := time.Since(start)

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected status 503 on DB timeout, got %d", resp.StatusCode)
	}
	// Bounded timeout should cut off around 1s, well before 2s
	if elapsed >= 1800*time.Millisecond {
		t.Errorf("readiness DB check took too long: %v", elapsed)
	}
}

func TestHealth_ConcurrentRaceSafety(t *testing.T) {
	db := &mockDBChecker{}
	sched := &mockSchedulerChecker{}
	sched.running.Store(true)
	drain := &api.DrainTracker{}

	handlers := api.NewHandlers(service.NewMonitorService(newMockAPIRepo(), sched), api.WithHealthChecks(db, sched, drain))
	srv := api.NewServer(api.Config{}, handlers)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	const numGoroutines = 30
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			client := &http.Client{Timeout: 2 * time.Second}

			for j := 0; j < 20; j++ {
				if idx == 0 && j == 10 {
					drain.SetDraining()
				}
				// Call livez
				resp1, err := client.Get(ts.URL + "/livez")
				if err == nil {
					_, _ = io.ReadAll(resp1.Body)
					resp1.Body.Close()
				}

				// Call readyz
				resp2, err := client.Get(ts.URL + "/readyz")
				if err == nil {
					_, _ = io.ReadAll(resp2.Body)
					resp2.Body.Close()
				}
			}
		}(i)
	}

	wg.Wait()
}
