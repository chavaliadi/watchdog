package telemetry

import (
	"strings"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
)

type mockWorkerProvider struct {
	active   int
	pending  int
	capacity int
}

func (m *mockWorkerProvider) ActiveCount() int  { return m.active }
func (m *mockWorkerProvider) PendingCount() int { return m.pending }
func (m *mockWorkerProvider) Capacity() int     { return m.capacity }

func TestMetrics_Construction(t *testing.T) {
	m := NewMetrics()
	if m == nil {
		t.Fatal("expected NewMetrics() to return non-nil Metrics")
	}
	if m.Registry() == nil {
		t.Fatal("expected Metrics to have an isolated non-nil Registry")
	}
	if m.Handler() == nil {
		t.Fatal("expected Metrics to provide an http.Handler")
	}
}

func TestMetrics_ExpectedMetricsRegistered(t *testing.T) {
	m := NewMetrics()

	// Seed some values so all vector collectors have at least one sample gathered
	m.RecordCheck("http", true, "none", 50*time.Millisecond)
	m.RecordRetry("http", "recovered")
	m.RecordCycleError("run_cycle")
	m.RecordHTTPRequest("GET", "/monitors", 200, 10*time.Millisecond)
	m.RecordDBOperation("get_monitor", true, 5*time.Millisecond)
	m.SetMonitorsActive("http", 3)

	expectedMetricNames := []string{
		"watchdog_worker_pool_active_workers",
		"watchdog_worker_pool_queued_jobs",
		"watchdog_worker_pool_capacity",
		"watchdog_monitors_active",
		"watchdog_checks_total",
		"watchdog_check_duration_seconds",
		"watchdog_check_retries_total",
		"watchdog_scheduler_cycle_errors_total",
		"watchdog_http_requests_total",
		"watchdog_http_request_duration_seconds",
		"watchdog_db_operations_total",
		"watchdog_db_operation_duration_seconds",
	}

	metricFamilies, err := m.Registry().Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	found := make(map[string]bool)
	for _, mf := range metricFamilies {
		found[mf.GetName()] = true
	}

	for _, expected := range expectedMetricNames {
		if !found[expected] {
			t.Errorf("expected registered metric %q not found in gathered metrics", expected)
		}
	}
}

func TestMetrics_LabelCardinalityAndForbiddenLabels(t *testing.T) {
	m := NewMetrics()

	// Record with varying values including attempts to supply unbounded/dangerous inputs
	m.RecordCheck("http", false, "timeout", 100*time.Millisecond)
	m.RecordRetry("tcp", "exhausted")
	m.RecordCycleError("save_cycle")
	m.RecordHTTPRequest("POST", "/monitors", 201, 20*time.Millisecond)
	m.RecordDBOperation("save_cycle", false, 15*time.Millisecond)
	m.SetMonitorsActive("tcp", 2)

	// Permitted label names according to design specification
	allowedLabelNames := map[string]bool{
		"kind":        true,
		"status":      true,
		"error_class": true,
		"method":      true,
		"route":       true,
		"status_code": true,
		"stage":       true,
		"operation":   true,
		"outcome":     true,
	}

	forbiddenSubstrings := []string{
		"monitor_id",
		"cycle_id",
		"request_id",
		"req_id",
		"url",
		"target",
		"error_detail",
		"exception",
		"host",
		"timestamp",
	}

	metricFamilies, err := m.Registry().Gather()
	if err != nil {
		t.Fatalf("Gather failed: %v", err)
	}

	for _, mf := range metricFamilies {
		for _, metric := range mf.GetMetric() {
			for _, label := range metric.GetLabel() {
				name := label.GetName()
				val := label.GetValue()

				// 1. Check label name against allowlist
				if !allowedLabelNames[name] {
					t.Errorf("metric %q has unauthorized label name %q", mf.GetName(), name)
				}

				// 2. Check label name against forbidden substrings
				for _, forbidden := range forbiddenSubstrings {
					if strings.Contains(strings.ToLower(name), forbidden) {
						t.Errorf("metric %q label name %q contains forbidden token %q", mf.GetName(), name, forbidden)
					}
				}

				// 3. Check label value does not look like raw URL or UUID
				if strings.HasPrefix(val, "http://") || strings.HasPrefix(val, "https://") {
					t.Errorf("metric %q label %q contains raw URL value: %q", mf.GetName(), name, val)
				}
				if len(val) == 36 && strings.Count(val, "-") == 4 {
					t.Errorf("metric %q label %q appears to be a UUID: %q", mf.GetName(), name, val)
				}
			}
		}
	}
}

func TestMetrics_MultipleRegistriesIsolation(t *testing.T) {
	// Creating multiple Metrics instances should never panic or cause
	// duplicate-registration errors because each instance owns an isolated registry.
	for i := 0; i < 5; i++ {
		m := NewMetrics()
		if m == nil {
			t.Fatalf("iteration %d: NewMetrics returned nil", i)
		}
		m.RecordCheck("http", true, "none", 10*time.Millisecond)
		fams, err := m.Registry().Gather()
		if err != nil {
			t.Fatalf("iteration %d: Gather failed: %v", i, err)
		}
		if len(fams) == 0 {
			t.Fatalf("iteration %d: expected gathered metrics", i)
		}
	}
}

func TestMetrics_WorkerPoolCollector(t *testing.T) {
	m := NewMetrics()
	provider := &mockWorkerProvider{
		active:   3,
		pending:  7,
		capacity: 10,
	}
	m.RegisterWorkerPool(provider)

	fams, err := m.Registry().Gather()
	if err != nil {
		t.Fatalf("Gather failed: %v", err)
	}

	var foundActive, foundPending, foundCapacity bool
	for _, mf := range fams {
		switch mf.GetName() {
		case "watchdog_worker_pool_active_workers":
			foundActive = true
			if len(mf.GetMetric()) != 1 || mf.GetMetric()[0].GetGauge().GetValue() != 3 {
				t.Errorf("expected active workers 3, got %v", mf.GetMetric())
			}
		case "watchdog_worker_pool_queued_jobs":
			foundPending = true
			if len(mf.GetMetric()) != 1 || mf.GetMetric()[0].GetGauge().GetValue() != 7 {
				t.Errorf("expected queued jobs 7, got %v", mf.GetMetric())
			}
		case "watchdog_worker_pool_capacity":
			foundCapacity = true
			if len(mf.GetMetric()) != 1 || mf.GetMetric()[0].GetGauge().GetValue() != 10 {
				t.Errorf("expected capacity 10, got %v", mf.GetMetric())
			}
		}
	}

	if !foundActive || !foundPending || !foundCapacity {
		t.Errorf("missing worker pool metrics: active=%v, pending=%v, capacity=%v",
			foundActive, foundPending, foundCapacity)
	}
}

func TestMetrics_StatusCodeNormalization(t *testing.T) {
	tests := []struct {
		code int
		want string
	}{
		{200, "200"},
		{201, "201"},
		{204, "204"},
		{400, "400"},
		{404, "404"},
		{500, "500"},
		{503, "503"},
		{299, "2xx"},
		{307, "3xx"},
		{418, "4xx"},
		{599, "5xx"},
		{999, "unknown"},
		{-1, "unknown"},
	}

	for _, tt := range tests {
		got := normalizeStatusCode(tt.code)
		if got != tt.want {
			t.Errorf("normalizeStatusCode(%d) = %q, want %q", tt.code, got, tt.want)
		}
	}
}

func TestNoopRecorder(t *testing.T) {
	var rec Recorder = NoopRecorder{}
	// Calling methods on NoopRecorder must not panic
	rec.RecordCheck("http", true, "none", 10*time.Millisecond)
	rec.RecordRetry("tcp", "recovered")
	rec.RecordCycleError("run_cycle")
	rec.RecordDBOperation("save_cycle", true, 10*time.Millisecond)
	rec.RecordHTTPRequest("GET", "/monitors", 200, 10*time.Millisecond)
	rec.RecordActiveMonitors("http", 5)
}

func TestMetrics_RecorderMethodsGatherValues(t *testing.T) {
	m := NewMetrics()

	m.RecordCheck("http", true, "", 25*time.Millisecond)
	m.RecordCheck("http", false, "timeout", 100*time.Millisecond)
	m.RecordRetry("http", "recovered")
	m.RecordCycleError("run_cycle")
	m.RecordDBOperation("save_cycle", true, 5*time.Millisecond)
	m.RecordHTTPRequest("GET", "/monitors/{id}", 200, 12*time.Millisecond)
	m.RecordActiveMonitors("http", 3)

	fams, err := m.Registry().Gather()
	if err != nil {
		t.Fatalf("Gather failed: %v", err)
	}

	famMap := make(map[string]*dto.MetricFamily)
	for _, f := range fams {
		famMap[f.GetName()] = f
	}

	// Verify checks total counter has 2 series
	checks := famMap["watchdog_checks_total"]
	if checks == nil || len(checks.GetMetric()) != 2 {
		t.Fatalf("expected 2 checks_total series, got %v", checks)
	}

	// Verify check retries has 1 series
	retries := famMap["watchdog_check_retries_total"]
	if retries == nil || len(retries.GetMetric()) != 1 {
		t.Fatalf("expected 1 check_retries_total series, got %v", retries)
	}

	// Verify cycle errors has 1 series
	cycleErrors := famMap["watchdog_scheduler_cycle_errors_total"]
	if cycleErrors == nil || len(cycleErrors.GetMetric()) != 1 {
		t.Fatalf("expected 1 scheduler_cycle_errors_total series, got %v", cycleErrors)
	}

	// Verify http requests total has 1 series
	httpReqs := famMap["watchdog_http_requests_total"]
	if httpReqs == nil || len(httpReqs.GetMetric()) != 1 {
		t.Fatalf("expected 1 http_requests_total series, got %v", httpReqs)
	}

	// Verify db operations total has 1 series
	dbOps := famMap["watchdog_db_operations_total"]
	if dbOps == nil || len(dbOps.GetMetric()) != 1 {
		t.Fatalf("expected 1 db_operations_total series, got %v", dbOps)
	}
}
