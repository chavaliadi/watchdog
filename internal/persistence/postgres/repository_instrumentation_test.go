package postgres_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/chavaliadi/watchdog/internal/checker"
	"github.com/chavaliadi/watchdog/internal/monitor"
	"github.com/chavaliadi/watchdog/internal/persistence/postgres"
	"github.com/chavaliadi/watchdog/internal/state"
)

type dbOpRecord struct {
	op       string
	ok       bool
	duration time.Duration
}

type mockDBRecorder struct {
	mu  sync.Mutex
	ops []dbOpRecord
}

func (m *mockDBRecorder) RecordCheck(kind string, ok bool, errClass string, duration time.Duration) {}
func (m *mockDBRecorder) RecordRetry(kind string, outcome string)                                     {}
func (m *mockDBRecorder) RecordCycleError(stage string)                                              {}
func (m *mockDBRecorder) RecordHTTPRequest(method, route string, statusCode int, duration time.Duration) {
}
func (m *mockDBRecorder) RecordActiveMonitors(kind string, count int) {}

func (m *mockDBRecorder) RecordDBOperation(op string, ok bool, duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ops = append(m.ops, dbOpRecord{op: op, ok: ok, duration: duration})
}

func (m *mockDBRecorder) getOps() []dbOpRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	res := make([]dbOpRecord, len(m.ops))
	copy(res, m.ops)
	return res
}

func (m *mockDBRecorder) clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ops = nil
}

func TestRepositoryInstrumentation_AllOperations(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()

	rec := &mockDBRecorder{}
	repo := postgres.New(testDB, postgres.WithRecorder(rec))

	id := "22222222-2222-2222-2222-222222222222"
	m := monitor.Monitor{
		ID:                  id,
		Name:                "Instrumentation Test Monitor",
		Kind:                monitor.KindHTTP,
		TargetURL:           "https://example.com/health",
		Method:              "GET",
		ExpectedStatusRange: "200-299",
		Interval:            10 * time.Second,
		Timeout:             2 * time.Second,
		Enabled:             true,
	}

	// 1. CreateMonitor
	rec.clear()
	if err := repo.CreateMonitor(ctx, m); err != nil {
		t.Fatalf("CreateMonitor failed: %v", err)
	}
	ops := rec.getOps()
	if len(ops) != 1 {
		t.Fatalf("expected 1 op for CreateMonitor, got %d", len(ops))
	}
	if ops[0].op != "create_monitor" || !ops[0].ok {
		t.Fatalf("unexpected op record: %+v", ops[0])
	}

	// 2. GetMonitor
	rec.clear()
	gotM, err := repo.GetMonitor(ctx, id)
	if err != nil || gotM.ID != id {
		t.Fatalf("GetMonitor failed: %v", err)
	}
	ops = rec.getOps()
	if len(ops) != 1 || ops[0].op != "get_monitor" || !ops[0].ok {
		t.Fatalf("unexpected op record for GetMonitor: %+v", ops)
	}

	// 3. ListMonitors
	rec.clear()
	list, err := repo.ListMonitors(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListMonitors failed: %v", err)
	}
	ops = rec.getOps()
	if len(ops) != 1 || ops[0].op != "list_monitors" || !ops[0].ok {
		t.Fatalf("unexpected op record for ListMonitors: %+v", ops)
	}

	// 4. GetStateWithTimestamp
	rec.clear()
	st, _, err := repo.GetStateWithTimestamp(ctx, id)
	if err != nil || st != state.StateUnknown {
		t.Fatalf("GetStateWithTimestamp failed: %v", err)
	}
	ops = rec.getOps()
	if len(ops) != 1 || ops[0].op != "get_state_with_timestamp" || !ops[0].ok {
		t.Fatalf("unexpected op record for GetStateWithTimestamp: %+v", ops)
	}

	// 5. GetState (should record ONLY get_state, no double counting)
	rec.clear()
	st, err = repo.GetState(ctx, id)
	if err != nil || st != state.StateUnknown {
		t.Fatalf("GetState failed: %v", err)
	}
	ops = rec.getOps()
	if len(ops) != 1 {
		t.Fatalf("expected exactly 1 op for GetState, got %d: %+v", len(ops), ops)
	}
	if ops[0].op != "get_state" || !ops[0].ok {
		t.Fatalf("unexpected op record for GetState: %+v", ops[0])
	}

	// 6. SaveCycle
	rec.clear()
	result := checker.CheckResult{
		MonitorID:    id,
		CheckedAt:    time.Now().UTC(),
		OK:           true,
		StatusCode:   200,
		Latency:      150 * time.Millisecond,
		AttemptCount: 1,
	}
	if err := repo.SaveCycle(ctx, id, result, state.StateHealthy); err != nil {
		t.Fatalf("SaveCycle failed: %v", err)
	}
	ops = rec.getOps()
	if len(ops) != 1 || ops[0].op != "save_cycle" || !ops[0].ok {
		t.Fatalf("unexpected op record for SaveCycle: %+v", ops)
	}

	// 7. UpdateMonitor
	rec.clear()
	m.Name = "Updated Name"
	if err := repo.UpdateMonitor(ctx, m); err != nil {
		t.Fatalf("UpdateMonitor failed: %v", err)
	}
	ops = rec.getOps()
	if len(ops) != 1 || ops[0].op != "update_monitor" || !ops[0].ok {
		t.Fatalf("unexpected op record for UpdateMonitor: %+v", ops)
	}

	// 8. ListCheckResults
	rec.clear()
	results, err := repo.ListCheckResults(ctx, id, 10)
	if err != nil || len(results) != 1 {
		t.Fatalf("ListCheckResults failed: %v", err)
	}
	ops = rec.getOps()
	if len(ops) != 1 || ops[0].op != "list_check_results" || !ops[0].ok {
		t.Fatalf("unexpected op record for ListCheckResults: %+v", ops)
	}

	// 9. DeleteMonitor
	rec.clear()
	if err := repo.DeleteMonitor(ctx, id); err != nil {
		t.Fatalf("DeleteMonitor failed: %v", err)
	}
	ops = rec.getOps()
	if len(ops) != 1 || ops[0].op != "delete_monitor" || !ops[0].ok {
		t.Fatalf("unexpected op record for DeleteMonitor: %+v", ops)
	}

	// 10. Failed Operation Recording (e.g. Delete non-existent)
	rec.clear()
	err = repo.DeleteMonitor(ctx, "00000000-0000-0000-0000-000000000000")
	if err == nil {
		t.Fatalf("expected error on deleting non-existent monitor")
	}
	ops = rec.getOps()
	if len(ops) != 1 || ops[0].op != "delete_monitor" || ops[0].ok {
		t.Fatalf("expected failed delete_monitor op, got: %+v", ops)
	}
}

func TestRepositoryInstrumentation_ChainingAndNilSafety(t *testing.T) {
	repo := postgres.New(testDB)
	rec := &mockDBRecorder{}
	chained := repo.WithRecorder(rec)
	if chained == nil {
		t.Fatalf("expected chained repo")
	}

	// Nil recorder resets to NoopRecorder safely
	repo.WithRecorder(nil)
	ctx := context.Background()
	_, _ = repo.ListMonitors(ctx) // should not panic
}
