package telemetry

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Recorder defines the telemetry metrics recording interface.
// Subsystems in Phase 7D use this thin contract rather than depending
// directly on Prometheus client libraries.
type Recorder interface {
	RecordCheck(kind string, ok bool, errClass string, duration time.Duration)
	RecordRetry(kind string, outcome string)
	RecordCycleError(stage string)
	RecordDBOperation(op string, ok bool, duration time.Duration)
	RecordHTTPRequest(method, route string, statusCode int, duration time.Duration)
	RecordActiveMonitors(kind string, count int)
}

// NoopRecorder is a no-op implementation of the Recorder interface.
type NoopRecorder struct{}

func (NoopRecorder) RecordCheck(kind string, ok bool, errClass string, duration time.Duration) {}
func (NoopRecorder) RecordRetry(kind string, outcome string)                                   {}
func (NoopRecorder) RecordCycleError(stage string)                                            {}
func (NoopRecorder) RecordDBOperation(op string, ok bool, duration time.Duration)            {}
func (NoopRecorder) RecordHTTPRequest(method, route string, statusCode int, duration time.Duration) {
}
func (NoopRecorder) RecordActiveMonitors(kind string, count int) {}

var _ Recorder = NoopRecorder{}

// WorkerStatsProvider provides dynamic worker pool stats for Prometheus scraping.
type WorkerStatsProvider interface {
	ActiveCount() int
	PendingCount() int
	Capacity() int
}

// workerPoolCollector implements prometheus.Collector to dynamically sample
// worker pool statistics during Prometheus scrapes without contention on the worker hot path.
type workerPoolCollector struct {
	mu           sync.RWMutex
	provider     WorkerStatsProvider
	activeDesc   *prometheus.Desc
	queuedDesc   *prometheus.Desc
	capacityDesc *prometheus.Desc
}

func newWorkerPoolCollector() *workerPoolCollector {
	return &workerPoolCollector{
		activeDesc: prometheus.NewDesc(
			"watchdog_worker_pool_active_workers",
			"Number of worker goroutines currently executing a check cycle.",
			nil, nil,
		),
		queuedDesc: prometheus.NewDesc(
			"watchdog_worker_pool_queued_jobs",
			"Number of jobs in the worker pool queue awaiting an available worker.",
			nil, nil,
		),
		capacityDesc: prometheus.NewDesc(
			"watchdog_worker_pool_capacity",
			"Configured maximum concurrency capacity of the worker pool.",
			nil, nil,
		),
	}
}

func (c *workerPoolCollector) setProvider(p WorkerStatsProvider) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.provider = p
}

func (c *workerPoolCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.activeDesc
	ch <- c.queuedDesc
	ch <- c.capacityDesc
}

func (c *workerPoolCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.RLock()
	p := c.provider
	c.mu.RUnlock()

	var active, queued, capacity float64
	if p != nil {
		active = float64(p.ActiveCount())
		queued = float64(p.PendingCount())
		capacity = float64(p.Capacity())
	}

	ch <- prometheus.MustNewConstMetric(c.activeDesc, prometheus.GaugeValue, active)
	ch <- prometheus.MustNewConstMetric(c.queuedDesc, prometheus.GaugeValue, queued)
	ch <- prometheus.MustNewConstMetric(c.capacityDesc, prometheus.GaugeValue, capacity)
}

// Metrics encapsulates an isolated Prometheus registry and pre-registered
// Deployment Watchdog metric collectors.
type Metrics struct {
	reg             *prometheus.Registry
	workerCollector *workerPoolCollector

	monitorsActive            *prometheus.GaugeVec
	checksTotal               *prometheus.CounterVec
	checkDuration             *prometheus.HistogramVec
	checkRetriesTotal         *prometheus.CounterVec
	schedulerCycleErrorsTotal *prometheus.CounterVec
	httpRequestsTotal         *prometheus.CounterVec
	httpRequestDuration       *prometheus.HistogramVec
	dbOperationsTotal         *prometheus.CounterVec
	dbOperationDuration       *prometheus.HistogramVec
}

var _ Recorder = (*Metrics)(nil)

// NewMetrics instantiates an isolated Prometheus registry and registers the
// core Deployment Watchdog baseline metrics. It does NOT use the global default registry.
func NewMetrics() *Metrics {
	reg := prometheus.NewRegistry()
	workerCollector := newWorkerPoolCollector()

	monitorsActive := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "watchdog_monitors_active",
			Help: "Number of active runner goroutines managing schedules.",
		},
		[]string{"kind"},
	)

	checksTotal := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "watchdog_checks_total",
			Help: "Total check cycles completed.",
		},
		[]string{"kind", "status", "error_class"},
	)

	checkDuration := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "watchdog_check_duration_seconds",
			Help:    "End-to-end latency of checks in seconds.",
			Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0},
		},
		[]string{"kind", "status"},
	)

	checkRetriesTotal := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "watchdog_check_retries_total",
			Help: "Total retry attempts triggered after transient failures.",
		},
		[]string{"kind", "outcome"},
	)

	schedulerCycleErrorsTotal := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "watchdog_scheduler_cycle_errors_total",
			Help: "Unhandled cycle failures in runner loops.",
		},
		[]string{"stage"},
	)

	httpRequestsTotal := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "watchdog_http_requests_total",
			Help: "Total HTTP API requests handled.",
		},
		[]string{"method", "route", "status_code"},
	)

	httpRequestDuration := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "watchdog_http_request_duration_seconds",
			Help:    "Latency of HTTP API requests in seconds.",
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5},
		},
		[]string{"method", "route"},
	)

	dbOperationsTotal := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "watchdog_db_operations_total",
			Help: "Total database queries and transactions.",
		},
		[]string{"operation", "status"},
	)

	dbOperationDuration := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "watchdog_db_operation_duration_seconds",
			Help:    "Latency of database calls in seconds.",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5},
		},
		[]string{"operation"},
	)

	reg.MustRegister(
		workerCollector,
		monitorsActive,
		checksTotal,
		checkDuration,
		checkRetriesTotal,
		schedulerCycleErrorsTotal,
		httpRequestsTotal,
		httpRequestDuration,
		dbOperationsTotal,
		dbOperationDuration,
	)

	return &Metrics{
		reg:                       reg,
		workerCollector:           workerCollector,
		monitorsActive:            monitorsActive,
		checksTotal:               checksTotal,
		checkDuration:             checkDuration,
		checkRetriesTotal:         checkRetriesTotal,
		schedulerCycleErrorsTotal: schedulerCycleErrorsTotal,
		httpRequestsTotal:         httpRequestsTotal,
		httpRequestDuration:       httpRequestDuration,
		dbOperationsTotal:         dbOperationsTotal,
		dbOperationDuration:       dbOperationDuration,
	}
}

// Registry returns the isolated prometheus.Registry owned by this Metrics instance.
func (m *Metrics) Registry() *prometheus.Registry {
	return m.reg
}

// Handler returns an http.Handler that exposes metrics in Prometheus text exposition format.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
}

// RegisterWorkerPool hooks a WorkerStatsProvider into the worker pool dynamic collector.
func (m *Metrics) RegisterWorkerPool(p WorkerStatsProvider) {
	if m.workerCollector != nil {
		m.workerCollector.setProvider(p)
	}
}

// SetMonitorsActive updates the watchdog_monitors_active gauge for a given protocol kind.
func (m *Metrics) SetMonitorsActive(kind string, count float64) {
	m.monitorsActive.WithLabelValues(normalizeKind(kind)).Set(count)
}

// RecordActiveMonitors updates the watchdog_monitors_active gauge for a given protocol kind.
func (m *Metrics) RecordActiveMonitors(kind string, count int) {
	m.monitorsActive.WithLabelValues(normalizeKind(kind)).Set(float64(count))
}

// RecordCheck records a completed monitoring check execution.
func (m *Metrics) RecordCheck(kind string, ok bool, errClass string, duration time.Duration) {
	k := normalizeKind(kind)
	st := "failed"
	if ok {
		st = "ok"
	}
	ec := normalizeErrorClass(errClass)
	m.checksTotal.WithLabelValues(k, st, ec).Inc()
	m.checkDuration.WithLabelValues(k, st).Observe(duration.Seconds())
}

// RecordRetry records a retry outcome (recovered vs exhausted).
func (m *Metrics) RecordRetry(kind string, outcome string) {
	k := normalizeKind(kind)
	oc := normalizeOutcome(outcome)
	m.checkRetriesTotal.WithLabelValues(k, oc).Inc()
}

// RecordCycleError records an unhandled cycle error in runner loops.
func (m *Metrics) RecordCycleError(stage string) {
	stg := normalizeStage(stage)
	m.schedulerCycleErrorsTotal.WithLabelValues(stg).Inc()
}

// RecordDBOperation records a database operation duration and outcome.
func (m *Metrics) RecordDBOperation(op string, ok bool, duration time.Duration) {
	operation := normalizeOperation(op)
	st := "error"
	if ok {
		st = "ok"
	}
	m.dbOperationsTotal.WithLabelValues(operation, st).Inc()
	m.dbOperationDuration.WithLabelValues(operation).Observe(duration.Seconds())
}

// RecordHTTPRequest records an HTTP API request status and duration.
func (m *Metrics) RecordHTTPRequest(method, route string, statusCode int, duration time.Duration) {
	meth := normalizeMethod(method)
	rt := normalizeRoute(route)
	sc := normalizeStatusCode(statusCode)
	m.httpRequestsTotal.WithLabelValues(meth, rt, sc).Inc()
	m.httpRequestDuration.WithLabelValues(meth, rt).Observe(duration.Seconds())
}

// --- Strict Label Normalizers (Cardinality Safeguards) ---

func normalizeKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "http":
		return "http"
	case "tcp":
		return "tcp"
	case "health":
		return "health"
	default:
		return "unknown"
	}
}

func normalizeErrorClass(errClass string) string {
	switch strings.ToLower(strings.TrimSpace(errClass)) {
	case "", "none":
		return "none"
	case "dns":
		return "dns"
	case "conn_refused":
		return "conn_refused"
	case "tls":
		return "tls"
	case "timeout":
		return "timeout"
	case "status":
		return "status"
	case "assertion":
		return "assertion"
	default:
		return "other"
	}
}

func normalizeOutcome(outcome string) string {
	switch strings.ToLower(strings.TrimSpace(outcome)) {
	case "recovered":
		return "recovered"
	case "exhausted":
		return "exhausted"
	default:
		return "other"
	}
}

func normalizeStage(stage string) string {
	switch strings.ToLower(strings.TrimSpace(stage)) {
	case "run_cycle":
		return "run_cycle"
	case "save_cycle":
		return "save_cycle"
	case "alert":
		return "alert"
	default:
		return "other"
	}
}

func normalizeMethod(method string) string {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case "GET":
		return "GET"
	case "POST":
		return "POST"
	case "PATCH":
		return "PATCH"
	case "DELETE":
		return "DELETE"
	case "PUT":
		return "PUT"
	case "HEAD":
		return "HEAD"
	case "OPTIONS":
		return "OPTIONS"
	default:
		return "OTHER"
	}
}

func normalizeRoute(route string) string {
	trimmed := strings.TrimSpace(route)
	switch trimmed {
	case "/monitors", "/monitors/{id}", "/monitors/{id}/status", "/monitors/{id}/checks", "/livez", "/readyz", "/metrics":
		return trimmed
	default:
		return "other"
	}
}

func normalizeOperation(op string) string {
	switch strings.ToLower(strings.TrimSpace(op)) {
	case "save_cycle":
		return "save_cycle"
	case "get_monitor":
		return "get_monitor"
	case "list_monitors":
		return "list_monitors"
	case "create_monitor":
		return "create_monitor"
	case "update_monitor":
		return "update_monitor"
	case "delete_monitor":
		return "delete_monitor"
	case "get_state":
		return "get_state"
	case "get_state_with_timestamp":
		return "get_state_with_timestamp"
	case "list_check_results":
		return "list_check_results"
	case "ping":
		return "ping"
	default:
		return "other"
	}
}

func normalizeStatusCode(code int) string {
	switch code {
	case 200:
		return "200"
	case 201:
		return "201"
	case 204:
		return "204"
	case 400:
		return "400"
	case 401:
		return "401"
	case 403:
		return "403"
	case 404:
		return "404"
	case 405:
		return "405"
	case 409:
		return "409"
	case 422:
		return "422"
	case 429:
		return "429"
	case 500:
		return "500"
	case 502:
		return "502"
	case 503:
		return "503"
	case 504:
		return "504"
	default:
		switch {
		case code >= 100 && code < 200:
			return "1xx"
		case code >= 200 && code < 300:
			return "2xx"
		case code >= 300 && code < 400:
			return "3xx"
		case code >= 400 && code < 500:
			return "4xx"
		case code >= 500 && code < 600:
			return "5xx"
		default:
			return "unknown"
		}
	}
}
