package postgres_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chavaliadi/watchdog/internal/api"
	"github.com/chavaliadi/watchdog/internal/persistence/postgres"
	"github.com/chavaliadi/watchdog/internal/retry"
	"github.com/chavaliadi/watchdog/internal/scheduler"
	"github.com/chavaliadi/watchdog/internal/service"
	"github.com/chavaliadi/watchdog/internal/telemetry"
	"github.com/chavaliadi/watchdog/internal/worker"
)

// TestE2E_ObservabilityAndShutdown performs a full end-to-end integration test
// of Phase 7 observability features against the real PostgreSQL test database:
// - Application component startup & DB connection
// - /livez (200), /readyz (200), /metrics (200)
// - Monitor creation via HTTP API
// - Scheduled execution & state updates
// - Metric counters, gauges, histograms for checks, DB ops, HTTP API, and worker pool
// - Cardinality safeguards: absence of UUIDs, URLs, and error details in metric labels
// - Graceful shutdown and readiness drain transition (503)
func TestE2E_ObservabilityAndShutdown(t *testing.T) {
	cleanTables(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. Create a dummy target HTTP server
	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("healthy probe response"))
	}))
	defer targetServer.Close()

	// 2. Initialize isolated telemetry metrics
	metrics := telemetry.NewMetrics()

	// 3. Initialize PostgreSQL repository with telemetry instrumentation
	repo := postgres.New(testDB, postgres.WithRecorder(metrics))

	// 4. Initialize Orchestrator with default checkers, retry config, and telemetry
	orch := scheduler.NewOrchestrator(
		nil,
		nil,
		retry.Config{MaxAttempts: 2, Recorder: metrics},
		repo,
	).WithRecorder(metrics)

	// 5. Initialize Bounded Worker Pool with telemetry stats hook
	pool, err := worker.NewPool(worker.Config{MaxConcurrency: 3}, orch)
	if err != nil {
		t.Fatalf("failed to create worker pool: %v", err)
	}
	pool.Start(ctx)
	defer func() {
		pool.Stop()
		pool.Wait()
	}()
	metrics.RegisterWorkerPool(pool)

	// 6. Initialize MultiScheduler with telemetry
	multiSched, err := scheduler.NewMultiScheduler(repo, pool)
	if err != nil {
		t.Fatalf("failed to create multi-scheduler: %v", err)
	}
	multiSched.WithRecorder(metrics)

	if err := multiSched.Start(ctx); err != nil {
		t.Fatalf("failed to start multi-scheduler: %v", err)
	}
	defer func() {
		multiSched.Stop()
		multiSched.Wait()
	}()

	// 7. Initialize Service, DrainTracker, Handlers, and HTTP Server
	svc := service.NewMonitorService(repo, multiSched)
	drainTracker := &api.DrainTracker{}

	handlers := api.NewHandlers(
		svc,
		api.WithHealthChecks(testDB, multiSched, drainTracker),
		api.WithMetrics(metrics.Handler()),
		api.WithRecorder(metrics),
	)

	apiServer := api.NewServer(api.Config{}, handlers)
	ts := httptest.NewServer(apiServer.Handler())
	defer ts.Close()

	client := &http.Client{Timeout: 5 * time.Second}

	// 8. Verify /livez returns 200 OK
	livezResp, err := client.Get(ts.URL + "/livez")
	if err != nil {
		t.Fatalf("GET /livez failed: %v", err)
	}
	defer livezResp.Body.Close()
	if livezResp.StatusCode != http.StatusOK {
		t.Errorf("expected /livez status 200, got %d", livezResp.StatusCode)
	}
	livezBody, _ := io.ReadAll(livezResp.Body)
	if string(livezBody) != "ok\n" {
		t.Errorf("expected /livez body 'ok\\n', got %q", string(livezBody))
	}

	// 9. Verify /readyz returns 200 OK when healthy
	readyzResp, err := client.Get(ts.URL + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz failed: %v", err)
	}
	defer readyzResp.Body.Close()
	if readyzResp.StatusCode != http.StatusOK {
		t.Errorf("expected /readyz status 200, got %d", readyzResp.StatusCode)
	}
	readyzBody, _ := io.ReadAll(readyzResp.Body)
	if string(readyzBody) != "ready\n" {
		t.Errorf("expected /readyz body 'ready\\n', got %q", string(readyzBody))
	}

	// 10. Verify /metrics returns 200 OK
	metricsResp, err := client.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics failed: %v", err)
	}
	defer metricsResp.Body.Close()
	if metricsResp.StatusCode != http.StatusOK {
		t.Errorf("expected /metrics status 200, got %d", metricsResp.StatusCode)
	}

	// 11. Create a Monitor via POST /monitors
	createPayload := map[string]any{
		"name":                  "E2E Target Monitor",
		"kind":                  "http",
		"target":                targetServer.URL,
		"method":                "GET",
		"expected_status_range": "200-299",
		"interval_ms":           1000,
		"timeout_ms":            2000,
		"enabled":               true,
	}
	payloadBytes, _ := json.Marshal(createPayload)
	postResp, err := client.Post(ts.URL+"/monitors", "application/json", bytes.NewReader(payloadBytes))
	if err != nil {
		t.Fatalf("POST /monitors failed: %v", err)
	}
	defer postResp.Body.Close()
	if postResp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(postResp.Body)
		t.Fatalf("expected POST /monitors status 201, got %d (body: %s)", postResp.StatusCode, string(respBody))
	}

	var createdMon struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(postResp.Body).Decode(&createdMon)
	if createdMon.ID == "" {
		t.Fatalf("expected created monitor to have valid ID")
	}

	// 12. Wait for scheduled checks to execute against local target
	deadline := time.Now().Add(3 * time.Second)
	var checkCount int
	for time.Now().Before(deadline) {
		_ = testDB.QueryRow(`SELECT COUNT(*) FROM check_results WHERE monitor_id = $1`, createdMon.ID).Scan(&checkCount)
		if checkCount >= 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if checkCount < 1 {
		t.Fatalf("expected at least 1 check executed, got %d", checkCount)
	}

	// 13. Scrape /metrics and verify all expected families and values
	scrapeResp, err := client.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics scrape failed: %v", err)
	}
	defer scrapeResp.Body.Close()
	metricsBytes, _ := io.ReadAll(scrapeResp.Body)
	metricsOutput := string(metricsBytes)

	expectedMetricTokens := []string{
		"watchdog_worker_pool_capacity 3",
		"watchdog_worker_pool_active_workers",
		"watchdog_worker_pool_queued_jobs",
		`watchdog_monitors_active{kind="http"} 1`,
		`watchdog_checks_total{error_class="none",kind="http",status="ok"}`,
		`watchdog_check_duration_seconds_count{kind="http",status="ok"}`,
		`watchdog_http_requests_total{method="POST",route="/monitors",status_code="201"} 1`,
		`watchdog_http_requests_total{method="GET",route="/livez",status_code="200"} 1`,
		`watchdog_http_requests_total{method="GET",route="/readyz",status_code="200"} 1`,
		`watchdog_db_operations_total{operation="create_monitor",status="ok"} 1`,
		`watchdog_db_operations_total{operation="save_cycle",status="ok"}`,
	}

	for _, token := range expectedMetricTokens {
		if !strings.Contains(metricsOutput, token) {
			t.Errorf("expected /metrics output to contain %q, but was missing.\nOutput:\n%s", token, metricsOutput)
		}
	}

	// 14. Cardinality Safeguard Audit:
	// Verify that monitor UUID, raw URL, and raw error messages NEVER appear in metric labels!
	if strings.Contains(metricsOutput, createdMon.ID) {
		t.Errorf("cardinality violation: monitor UUID %q appeared in metric exposition!", createdMon.ID)
	}
	if strings.Contains(metricsOutput, targetServer.URL) {
		t.Errorf("cardinality violation: raw target URL %q appeared in metric exposition!", targetServer.URL)
	}
	if strings.Contains(metricsOutput, "127.0.0.1:") && strings.Contains(metricsOutput, `route=`) {
		t.Errorf("cardinality violation: raw host:port appeared in route label!")
	}

	// 15. Verify Shutdown & Draining behavior:
	// Enter draining state: /readyz must immediately return 503, while /livez still returns 200
	drainTracker.SetDraining()

	drainingReadyzResp, err := client.Get(ts.URL + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz during draining failed: %v", err)
	}
	defer drainingReadyzResp.Body.Close()
	if drainingReadyzResp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected /readyz status 503 when draining, got %d", drainingReadyzResp.StatusCode)
	}

	drainingLivezResp, err := client.Get(ts.URL + "/livez")
	if err != nil {
		t.Fatalf("GET /livez during draining failed: %v", err)
	}
	defer drainingLivezResp.Body.Close()
	if drainingLivezResp.StatusCode != http.StatusOK {
		t.Errorf("expected /livez status 200 when draining, got %d", drainingLivezResp.StatusCode)
	}

	// Clean component shutdown
	multiSched.Stop()
	multiSched.Wait()
	pool.Stop()
	pool.Wait()
}
