package telemetry

import (
	"context"
	"testing"
)

func TestRequestIDContext(t *testing.T) {
	// 1. Nil context handling
	if id := RequestIDFromContext(nil); id != "" {
		t.Errorf("expected empty string for nil context, got %q", id)
	}

	// 2. Empty context
	ctx := context.Background()
	if id := RequestIDFromContext(ctx); id != "" {
		t.Errorf("expected empty string for empty context, got %q", id)
	}

	// 3. Context with request ID
	reqID := "req-abc-123"
	ctx = WithRequestID(ctx, reqID)
	if id := RequestIDFromContext(ctx); id != reqID {
		t.Errorf("expected %q, got %q", reqID, id)
	}

	// 4. WithRequestID with nil parent context
	nilParentCtx := WithRequestID(nil, "fallback-id")
	if id := RequestIDFromContext(nilParentCtx); id != "fallback-id" {
		t.Errorf("expected fallback-id, got %q", id)
	}
}

func TestCycleContext(t *testing.T) {
	// 1. Nil context handling
	mID, cID := CycleInfoFromContext(nil)
	if mID != "" || cID != "" {
		t.Errorf("expected empty strings for nil context, got %q, %q", mID, cID)
	}

	// 2. Empty context
	ctx := context.Background()
	mID, cID = CycleInfoFromContext(ctx)
	if mID != "" || cID != "" {
		t.Errorf("expected empty strings for empty context, got %q, %q", mID, cID)
	}

	// 3. Context with cycle info
	expectedMonitorID := "mon-uuid-789"
	expectedCycleID := "cycle-uuid-456"
	ctx = WithCycleContext(ctx, expectedMonitorID, expectedCycleID)

	mID, cID = CycleInfoFromContext(ctx)
	if mID != expectedMonitorID {
		t.Errorf("expected monitor ID %q, got %q", expectedMonitorID, mID)
	}
	if cID != expectedCycleID {
		t.Errorf("expected cycle ID %q, got %q", expectedCycleID, cID)
	}

	// 4. WithCycleContext with nil parent context
	nilParentCtx := WithCycleContext(nil, "m1", "c1")
	mID, cID = CycleInfoFromContext(nilParentCtx)
	if mID != "m1" || cID != "c1" {
		t.Errorf("expected m1, c1, got %q, %q", mID, cID)
	}
}
