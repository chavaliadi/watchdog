package api_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chavaliadi/watchdog/internal/api"
	"github.com/chavaliadi/watchdog/internal/monitor"
	"github.com/chavaliadi/watchdog/internal/service"
	"github.com/chavaliadi/watchdog/internal/telemetry"
)

type failingDBChecker struct{}

func (f *failingDBChecker) PingContext(ctx context.Context) error {
	return errors.New("database connection refused")
}

func TestMetricsEndpoint_Returns200AndValidExposition(t *testing.T) {
	metrics := telemetry.NewMetrics()
	handlers := api.NewHandlers(nil, api.WithMetrics(metrics.Handler()))
	server := api.NewServer(api.Config{}, handlers)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()

	server.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200 OK, got %d", rr.Code)
	}

	contentType := rr.Header().Get("Content-Type")
	if !strings.Contains(contentType, "text/plain") || !strings.Contains(contentType, "version=0.0.4") {
		t.Errorf("expected Prometheus text exposition Content-Type, got %q", contentType)
	}

	body := rr.Body.String()
	expectedMetrics := []string{
		"watchdog_worker_pool_active_workers",
		"watchdog_worker_pool_queued_jobs",
		"watchdog_worker_pool_capacity",
	}

	for _, expected := range expectedMetrics {
		if !strings.Contains(body, expected) {
			t.Errorf("expected response body to contain %q, but was missing", expected)
		}
	}
}

func TestMetricsEndpoint_NoDatabaseAccessRequired(t *testing.T) {
	metrics := telemetry.NewMetrics()
	failingDB := &failingDBChecker{}
	handlers := api.NewHandlers(nil,
		api.WithHealthChecks(failingDB, nil, nil),
		api.WithMetrics(metrics.Handler()),
	)
	server := api.NewServer(api.Config{}, handlers)

	// Confirm that /readyz fails due to DB
	readyReq := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	readyRR := httptest.NewRecorder()
	server.Handler().ServeHTTP(readyRR, readyReq)
	if readyRR.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected /readyz to return 503, got %d", readyRR.Code)
	}

	// Confirm that /metrics still succeeds with 200
	metricsReq := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	metricsRR := httptest.NewRecorder()
	server.Handler().ServeHTTP(metricsRR, metricsReq)

	if metricsRR.Code != http.StatusOK {
		t.Fatalf("expected /metrics to return 200 even when database is down, got %d", metricsRR.Code)
	}
}

func TestMetricsEndpoint_NotFoundWhenUnconfigured(t *testing.T) {
	handlers := api.NewHandlers(nil)
	server := api.NewServer(api.Config{}, handlers)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()

	server.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 Not Found when unconfigured, got %d", rr.Code)
	}
}

func TestMetricsEndpoint_ExistingRoutesUnaffected(t *testing.T) {
	metrics := telemetry.NewMetrics()
	repo := newMockAPIRepo()
	_ = repo.CreateMonitor(context.Background(), monitor.Monitor{
		ID:        "mon-1",
		Name:      "M1",
		Kind:      monitor.KindHTTP,
		TargetURL: "http://example.com",
		Interval:  time.Minute,
		Timeout:   5 * time.Second,
		Enabled:   true,
	})

	svc := service.NewMonitorService(repo, nil)
	handlers := api.NewHandlers(svc, api.WithMetrics(metrics.Handler()))
	server := api.NewServer(api.Config{}, handlers)

	// 1. Check /livez still returns 200
	liveReq := httptest.NewRequest(http.MethodGet, "/livez", nil)
	liveRR := httptest.NewRecorder()
	server.Handler().ServeHTTP(liveRR, liveReq)
	if liveRR.Code != http.StatusOK {
		t.Errorf("expected /livez to return 200, got %d", liveRR.Code)
	}

	// 2. Check /monitors still returns 200 with JSON
	monReq := httptest.NewRequest(http.MethodGet, "/monitors", nil)
	monRR := httptest.NewRecorder()
	server.Handler().ServeHTTP(monRR, monReq)
	if monRR.Code != http.StatusOK {
		t.Errorf("expected /monitors to return 200, got %d", monRR.Code)
	}

	// 3. Check /metrics returns 200 with text/plain
	metricsReq := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	metricsRR := httptest.NewRecorder()
	server.Handler().ServeHTTP(metricsRR, metricsReq)
	if metricsRR.Code != http.StatusOK {
		t.Errorf("expected /metrics to return 200, got %d", metricsRR.Code)
	}
}
