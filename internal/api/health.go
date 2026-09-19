package api

import (
	"context"
	"log/slog"
	"net/http"
	"regexp"
	"sync/atomic"
	"time"

	"github.com/chavaliadi/watchdog/internal/telemetry"
)

// DBHealthChecker defines the minimal database ping contract for readiness.
type DBHealthChecker interface {
	PingContext(ctx context.Context) error
}

// SchedulerHealthChecker defines the minimal scheduler status contract for readiness.
type SchedulerHealthChecker interface {
	IsRunning() bool
}

// DrainChecker defines the contract for checking if graceful shutdown has been initiated.
type DrainChecker interface {
	IsDraining() bool
}

// DrainTracker provides a thread-safe mechanism to track graceful shutdown state.
type DrainTracker struct {
	draining atomic.Bool
}

// SetDraining marks the application as draining/shutting down.
func (d *DrainTracker) SetDraining() {
	d.draining.Store(true)
}

// IsDraining reports whether the application is currently draining/shutting down.
func (d *DrainTracker) IsDraining() bool {
	return d.draining.Load()
}

// passwordRegex matches password fields in connection strings or DSNs.
var passwordRegex = regexp.MustCompile(`(?i)(password|passwd|pwd)=[^&;\s]+|:[^:@\s]+@`)

// sanitizeDBError strips any potential credentials or connection strings from DB error text.
func sanitizeDBError(msg string) string {
	return passwordRegex.ReplaceAllString(msg, "[REDACTED]")
}

// Livez handles GET /livez requests.
// It confirms that the process is alive and HTTP server is operational.
// It is shallow, deterministic, and performs zero external I/O.
func (h *Handlers) Livez(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

// Readyz handles GET /readyz requests.
// It checks whether the service is currently capable of accepting normal work:
// 1. Draining state: If shutting down, returns HTTP 503 immediately without touching DB or scheduler.
// 2. Scheduler state: If scheduler is not running, returns HTTP 503.
// 3. Database state: Performs a bounded ping with a 1-second timeout. If ping fails, returns HTTP 503.
// If all checks succeed, it returns HTTP 200 OK.
func (h *Handlers) Readyz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")

	// 1. Check draining first. If draining, immediately return 503 without touching scheduler or database.
	if h.drainChecker != nil && h.drainChecker.IsDraining() {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("not ready\n"))
		return
	}

	// 2. Check scheduler lifecycle state.
	if h.schedChecker != nil && !h.schedChecker.IsRunning() {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("not ready\n"))
		return
	}

	// 3. Check database health with a strict 1-second timeout.
	if h.dbChecker != nil {
		dbCtx, cancel := context.WithTimeout(r.Context(), 1*time.Second)
		defer cancel()

		if err := h.dbChecker.PingContext(dbCtx); err != nil {
			slog.Warn("readiness check failed: database ping error",
				telemetry.AttrComponent, "api",
				telemetry.AttrError, sanitizeDBError(err.Error()),
			)
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("not ready\n"))
			return
		}
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ready\n"))
}
