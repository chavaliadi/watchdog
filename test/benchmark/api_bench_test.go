package benchmark_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/chavaliadi/watchdog/internal/api"
	"github.com/chavaliadi/watchdog/internal/checker"
	"github.com/chavaliadi/watchdog/internal/monitor"
	"github.com/chavaliadi/watchdog/internal/persistence/postgres"
	"github.com/chavaliadi/watchdog/internal/retry"
	"github.com/chavaliadi/watchdog/internal/scheduler"
	"github.com/chavaliadi/watchdog/internal/service"
	"github.com/chavaliadi/watchdog/internal/state"
	"github.com/chavaliadi/watchdog/internal/telemetry"
	"github.com/chavaliadi/watchdog/internal/worker"
)

func TestRESTAPI_Performance(t *testing.T) {
	db, _, cleanup, err := StartEphemeralPostgres()
	if err != nil {
		t.Fatalf("start ephemeral postgres: %v", err)
	}
	defer cleanup()

	repo := postgres.New(db)
	orch := scheduler.NewOrchestrator(nil, nil, retry.Config{}, repo)
	pool, _ := worker.NewPool(worker.Config{MaxConcurrency: 5}, orch)
	pool.Start(context.Background())
	defer func() {
		pool.Stop()
		pool.Wait()
	}()

	multiSched, _ := scheduler.NewMultiScheduler(repo, pool)
	_ = multiSched.Start(context.Background())
	defer func() {
		multiSched.Stop()
		multiSched.Wait()
	}()

	monitorSvc := service.NewMonitorService(repo, multiSched)
	metrics := telemetry.NewMetrics()
	handlers := api.NewHandlers(
		monitorSvc,
		api.WithHealthChecks(db, multiSched, nil),
		api.WithMetrics(metrics.Handler()),
		api.WithRecorder(metrics),
	)

	apiServer := api.NewServer(api.Config{}, handlers)
	server := httptest.NewServer(apiServer.Handler())
	defer server.Close()

	client := server.Client()

	// Seed 500 monitors for testing
	ctx := context.Background()
	totalSeeded := 500
	seededIDs := make([]string, totalSeeded)
	for i := 0; i < totalSeeded; i++ {
		id := fmt.Sprintf("10000000-0000-0000-0000-%012d", i+1)
		m := monitor.Monitor{
			ID:                  id,
			Name:                fmt.Sprintf("API Monitor %d", i+1),
			Kind:                monitor.KindHTTP,
			TargetURL:           "http://127.0.0.1:8080/test",
			Method:              "GET",
			ExpectedStatusRange: "200-299",
			Interval:            60 * time.Second,
			Timeout:             5 * time.Second,
			Enabled:             true,
		}
		if err := repo.CreateMonitor(ctx, m); err != nil {
			t.Fatalf("seed monitor: %v", err)
		}
		seededIDs[i] = id

		// Add a check result and healthy state for status endpoint
		res := checker.CheckResult{
			MonitorID:    id,
			CheckedAt:    time.Now().UTC(),
			OK:           true,
			StatusCode:   200,
			Latency:      15 * time.Millisecond,
			AttemptCount: 1,
		}
		_ = repo.SaveCycle(ctx, id, res, state.StateHealthy)
	}

	// 1. GET /monitors scaling test across scales: 10, 50, 100, 250, 500
	monitorScales := []int{10, 50, 100, 250, 500}
	fmt.Println("\n======================= REST API: GET /monitors SCALING =======================")
	fmt.Printf("%-10s | %-12s | %-12s | %-12s | %-12s | %-12s | %-12s\n",
		"Monitors", "Payload Size", "Throughput", "Avg Latency", "P50 Latency", "P95 Latency", "P99 Latency")
	fmt.Println("--------------------------------------------------------------------------------")

	for _, count := range monitorScales {
		// To accurately test GET /monitors at N monitors, truncate and seed exactly N monitors
		_, _ = db.Exec("TRUNCATE monitors CASCADE")
		for i := 0; i < count; i++ {
			id := fmt.Sprintf("10000000-0000-0000-0000-%012d", i+1)
			m := monitor.Monitor{
				ID:                  id,
				Name:                fmt.Sprintf("API Monitor %d", i+1),
				Kind:                monitor.KindHTTP,
				TargetURL:           "http://127.0.0.1:8080/test",
				Method:              "GET",
				ExpectedStatusRange: "200-299",
				Interval:            60 * time.Second,
				Timeout:             5 * time.Second,
				Enabled:             true,
			}
			_ = repo.CreateMonitor(ctx, m)
		}

		iterations := 100
		durations := make([]time.Duration, iterations)
		var payloadLen int
		wallStart := time.Now()

		for i := 0; i < iterations; i++ {
			reqStart := time.Now()
			resp, err := client.Get(server.URL + "/monitors")
			durations[i] = time.Since(reqStart)

			if err != nil {
				t.Fatalf("GET /monitors failed: %v", err)
			}
			var buf bytes.Buffer
			_, _ = buf.ReadFrom(resp.Body)
			_ = resp.Body.Close()
			payloadLen = buf.Len()

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("unexpected status: %d", resp.StatusCode)
			}
		}

		stats := CalculateStats(durations, time.Since(wallStart))
		fmt.Printf("%-10d | %-10d B | %-10.2f/s | %-12v | %-12v | %-12v | %-12v\n",
			count, payloadLen, stats.Throughput, stats.Avg, stats.P50, stats.P95, stats.P99)
	}
	fmt.Println("================================================================================")

	// Re-seed 50 monitors for individual endpoint measurements
	_, _ = db.Exec("TRUNCATE monitors CASCADE")
	testID := "10000000-0000-0000-0000-000000000001"
	m := monitor.Monitor{
		ID:                  testID,
		Name:                "Single Endpoint Monitor",
		Kind:                monitor.KindHTTP,
		TargetURL:           "http://127.0.0.1:8080/health",
		Method:              "GET",
		ExpectedStatusRange: "200-299",
		Interval:            60 * time.Second,
		Timeout:             5 * time.Second,
		Enabled:             true,
	}
	_ = repo.CreateMonitor(ctx, m)
	_ = repo.SaveCycle(ctx, testID, checker.CheckResult{
		MonitorID:    testID,
		CheckedAt:    time.Now().UTC(),
		OK:           true,
		StatusCode:   200,
		Latency:      10 * time.Millisecond,
		AttemptCount: 1,
	}, state.StateHealthy)

	// 2. GET /monitors/{id}
	t.Run("GET_MonitorByID", func(t *testing.T) {
		iterations := 200
		durations := make([]time.Duration, iterations)
		wallStart := time.Now()

		for i := 0; i < iterations; i++ {
			start := time.Now()
			resp, err := client.Get(fmt.Sprintf("%s/monitors/%s", server.URL, testID))
			durations[i] = time.Since(start)
			if err != nil || resp.StatusCode != http.StatusOK {
				t.Fatalf("GET /monitors/{id} failed: %v (status %d)", err, resp.StatusCode)
			}
			_ = resp.Body.Close()
		}

		stats := CalculateStats(durations, time.Since(wallStart))
		fmt.Printf("[API] GET /monitors/{id} (N=%d): Avg=%v, P50=%v, P95=%v, P99=%v, Throughput=%.2f req/s\n",
			stats.Count, stats.Avg, stats.P50, stats.P95, stats.P99, stats.Throughput)
	})

	// 3. GET /monitors/{id}/status
	t.Run("GET_MonitorStatus", func(t *testing.T) {
		iterations := 200
		durations := make([]time.Duration, iterations)
		wallStart := time.Now()

		for i := 0; i < iterations; i++ {
			start := time.Now()
			resp, err := client.Get(fmt.Sprintf("%s/monitors/%s/status", server.URL, testID))
			durations[i] = time.Since(start)
			if err != nil || resp.StatusCode != http.StatusOK {
				t.Fatalf("GET /monitors/{id}/status failed: %v (status %d)", err, resp.StatusCode)
			}
			_ = resp.Body.Close()
		}

		stats := CalculateStats(durations, time.Since(wallStart))
		fmt.Printf("[API] GET /monitors/{id}/status (N=%d): Avg=%v, P50=%v, P95=%v, P99=%v, Throughput=%.2f req/s\n",
			stats.Count, stats.Avg, stats.P50, stats.P95, stats.P99, stats.Throughput)
	})

	// 4. GET /monitors/{id}/checks
	t.Run("GET_MonitorChecks", func(t *testing.T) {
		iterations := 200
		durations := make([]time.Duration, iterations)
		wallStart := time.Now()

		for i := 0; i < iterations; i++ {
			start := time.Now()
			resp, err := client.Get(fmt.Sprintf("%s/monitors/%s/checks?limit=10", server.URL, testID))
			durations[i] = time.Since(start)
			if err != nil || resp.StatusCode != http.StatusOK {
				t.Fatalf("GET /monitors/{id}/checks failed: %v (status %d)", err, resp.StatusCode)
			}
			_ = resp.Body.Close()
		}

		stats := CalculateStats(durations, time.Since(wallStart))
		fmt.Printf("[API] GET /monitors/{id}/checks (N=%d): Avg=%v, P50=%v, P95=%v, P99=%v, Throughput=%.2f req/s\n",
			stats.Count, stats.Avg, stats.P50, stats.P95, stats.P99, stats.Throughput)
	})

	// 5. POST /monitors
	t.Run("POST_Monitors", func(t *testing.T) {
		iterations := 100
		durations := make([]time.Duration, iterations)
		wallStart := time.Now()

		for i := 0; i < iterations; i++ {
			payload := map[string]any{
				"name":                  fmt.Sprintf("Created Monitor %d", i+1),
				"kind":                  "http",
				"target":                "http://127.0.0.1:8080/test",
				"method":                "GET",
				"expected_status_range": "200-299",
				"interval_ms":           60000,
				"timeout_ms":            5000,
				"enabled":               true,
			}
			body, _ := json.Marshal(payload)

			start := time.Now()
			resp, err := client.Post(server.URL+"/monitors", "application/json", bytes.NewReader(body))
			durations[i] = time.Since(start)

			if err != nil || resp.StatusCode != http.StatusCreated {
				t.Fatalf("POST /monitors failed: %v (status %d)", err, resp.StatusCode)
			}
			_ = resp.Body.Close()
		}

		stats := CalculateStats(durations, time.Since(wallStart))
		fmt.Printf("[API] POST /monitors (N=%d): Avg=%v, P50=%v, P95=%v, P99=%v, Throughput=%.2f req/s\n",
			stats.Count, stats.Avg, stats.P50, stats.P95, stats.P99, stats.Throughput)
	})

	// 6. PATCH /monitors/{id}
	t.Run("PATCH_Monitors", func(t *testing.T) {
		iterations := 100
		durations := make([]time.Duration, iterations)
		wallStart := time.Now()

		for i := 0; i < iterations; i++ {
			payload := map[string]any{
				"name": fmt.Sprintf("Updated Monitor Name %d", i+1),
			}
			body, _ := json.Marshal(payload)

			req, _ := http.NewRequest(http.MethodPatch, fmt.Sprintf("%s/monitors/%s", server.URL, testID), bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")

			start := time.Now()
			resp, err := client.Do(req)
			durations[i] = time.Since(start)

			if err != nil || resp.StatusCode != http.StatusOK {
				t.Fatalf("PATCH /monitors/{id} failed: %v (status %d)", err, resp.StatusCode)
			}
			_ = resp.Body.Close()
		}

		stats := CalculateStats(durations, time.Since(wallStart))
		fmt.Printf("[API] PATCH /monitors/{id} (N=%d): Avg=%v, P50=%v, P95=%v, P99=%v, Throughput=%.2f req/s\n",
			stats.Count, stats.Avg, stats.P50, stats.P95, stats.P99, stats.Throughput)
	})
}
