package telemetry

import (
	"context"
)

type contextKey string

const (
	requestIDKey contextKey = "watchdog_req_id"
	monitorIDKey contextKey = "watchdog_monitor_id"
	cycleIDKey   contextKey = "watchdog_cycle_id"
)

// WithRequestID stores an HTTP request correlation ID in the context.
func WithRequestID(ctx context.Context, id string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestIDFromContext extracts the HTTP request correlation ID from the context.
// Returns an empty string if not present.
func RequestIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	val, _ := ctx.Value(requestIDKey).(string)
	return val
}

// WithCycleContext stores the monitor ID and cycle ID in the context for monitoring cycle executions.
func WithCycleContext(ctx context.Context, monitorID, cycleID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = context.WithValue(ctx, monitorIDKey, monitorID)
	return context.WithValue(ctx, cycleIDKey, cycleID)
}

// CycleInfoFromContext extracts the monitor ID and cycle ID from the context.
// Returns empty strings for any values not present.
func CycleInfoFromContext(ctx context.Context) (monitorID string, cycleID string) {
	if ctx == nil {
		return "", ""
	}
	mID, _ := ctx.Value(monitorIDKey).(string)
	cID, _ := ctx.Value(cycleIDKey).(string)
	return mID, cID
}
