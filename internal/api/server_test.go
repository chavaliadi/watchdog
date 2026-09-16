package api_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/chavaliadi/watchdog/internal/api"
	"github.com/chavaliadi/watchdog/internal/checker"
	"github.com/chavaliadi/watchdog/internal/monitor"
	"github.com/chavaliadi/watchdog/internal/persistence"
	"github.com/chavaliadi/watchdog/internal/service"
	"github.com/chavaliadi/watchdog/internal/state"
)

type mockAPIRepo struct {
	mu           sync.Mutex
	monitors     map[string]monitor.Monitor
	states       map[string]state.State
	timestamps   map[string]time.Time
	checkResults map[string][]checker.CheckResult
}

var _ persistence.Repository = (*mockAPIRepo)(nil)

func newMockAPIRepo() *mockAPIRepo {
	return &mockAPIRepo{
		monitors:     make(map[string]monitor.Monitor),
		states:       make(map[string]state.State),
		timestamps:   make(map[string]time.Time),
		checkResults: make(map[string][]checker.CheckResult),
	}
}

func (m *mockAPIRepo) GetMonitor(ctx context.Context, id string) (monitor.Monitor, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	mon, ok := m.monitors[id]
	if !ok {
		return monitor.Monitor{}, sql.ErrNoRows
	}
	return mon, nil
}

func (m *mockAPIRepo) ListMonitors(ctx context.Context) ([]monitor.Monitor, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	list := make([]monitor.Monitor, 0, len(m.monitors))
	for _, mon := range m.monitors {
		list = append(list, mon)
	}
	return list, nil
}

func (m *mockAPIRepo) GetState(ctx context.Context, monitorID string) (state.State, error) {
	st, _, err := m.GetStateWithTimestamp(ctx, monitorID)
	return st, err
}

func (m *mockAPIRepo) GetStateWithTimestamp(ctx context.Context, monitorID string) (state.State, time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.monitors[monitorID]; !ok {
		return "", time.Time{}, sql.ErrNoRows
	}
	st, ok := m.states[monitorID]
	if !ok {
		st = state.StateUnknown
	}
	ts := m.timestamps[monitorID]
	return st, ts, nil
}

func (m *mockAPIRepo) CreateMonitor(ctx context.Context, mon monitor.Monitor) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.monitors[mon.ID] = mon
	m.states[mon.ID] = state.StateUnknown
	m.timestamps[mon.ID] = time.Now().UTC()
	return nil
}

func (m *mockAPIRepo) UpdateMonitor(ctx context.Context, mon monitor.Monitor) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.monitors[mon.ID]; !ok {
		return sql.ErrNoRows
	}
	m.monitors[mon.ID] = mon
	return nil
}

func (m *mockAPIRepo) DeleteMonitor(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.monitors[id]; !ok {
		return sql.ErrNoRows
	}
	delete(m.monitors, id)
	delete(m.states, id)
	delete(m.timestamps, id)
	delete(m.checkResults, id)
	return nil
}

func (m *mockAPIRepo) ListCheckResults(ctx context.Context, monitorID string, limit int) ([]checker.CheckResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	results := m.checkResults[monitorID]
	if len(results) > limit {
		results = results[:limit]
	}
	res := make([]checker.CheckResult, len(results))
	copy(res, results)
	return res, nil
}

func (m *mockAPIRepo) SaveCycle(ctx context.Context, monitorID string, result checker.CheckResult, nextState state.State) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states[monitorID] = nextState
	m.timestamps[monitorID] = result.CheckedAt
	m.checkResults[monitorID] = append([]checker.CheckResult{result}, m.checkResults[monitorID]...)
	return nil
}

type mockAPIScheduler struct {
	mu         sync.Mutex
	startCalls []string
	stopCalls  []string
}

func (m *mockAPIScheduler) StartMonitor(ctx context.Context, mon monitor.Monitor) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.startCalls = append(m.startCalls, mon.ID)
	return nil
}

func (m *mockAPIScheduler) StopMonitor(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopCalls = append(m.stopCalls, id)
	return nil
}

func (m *mockAPIScheduler) UpdateMonitor(ctx context.Context, mon monitor.Monitor) error {
	return nil
}

func setupAPITestServer(t *testing.T) (*httptest.Server, *mockAPIRepo, *mockAPIScheduler) {
	t.Helper()
	repo := newMockAPIRepo()
	sched := &mockAPIScheduler{}
	svc := service.NewMonitorService(repo, sched)
	handlers := api.NewHandlers(svc)
	srv := api.NewServer(api.Config{}, handlers)
	ts := httptest.NewServer(srv.Handler())
	return ts, repo, sched
}

func TestAPI_CreateMonitor(t *testing.T) {
	ts, _, sched := setupAPITestServer(t)
	defer ts.Close()

	t.Run("valid HTTP monitor", func(t *testing.T) {
		body := `{"name":"API Test","kind":"http","target":"https://api.example.com/health"}`
		resp, err := http.Post(ts.URL+"/monitors", "application/json", bytes.NewBufferString(body))
		if err != nil {
			t.Fatalf("POST /monitors failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201 Created, got %d", resp.StatusCode)
		}

		location := resp.Header.Get("Location")
		if location == "" {
			t.Error("expected Location header in 201 response")
		}

		var m api.MonitorResponse
		if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
			t.Fatalf("decode response failed: %v", err)
		}

		if m.ID == "" || m.Name != "API Test" || m.Method != "GET" || m.IntervalMs != 60000 {
			t.Errorf("unexpected monitor response: %+v", m)
		}

		if len(sched.startCalls) != 1 || sched.startCalls[0] != m.ID {
			t.Errorf("expected scheduler StartMonitor called for %s", m.ID)
		}
	})

	t.Run("valid TCP monitor", func(t *testing.T) {
		body := `{"name":"DB Port","kind":"tcp","target":"10.0.0.1:5432"}`
		resp, err := http.Post(ts.URL+"/monitors", "application/json", bytes.NewBufferString(body))
		if err != nil {
			t.Fatalf("POST /monitors failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201 Created, got %d", resp.StatusCode)
		}
	})

	t.Run("malformed JSON", func(t *testing.T) {
		body := `{bad json}`
		resp, err := http.Post(ts.URL+"/monitors", "application/json", bytes.NewBufferString(body))
		if err != nil {
			t.Fatalf("POST /monitors failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400 Bad Request, got %d", resp.StatusCode)
		}

		var errEnv api.ErrorEnvelope
		_ = json.NewDecoder(resp.Body).Decode(&errEnv)
		if errEnv.Error.Code != "MALFORMED_JSON" {
			t.Errorf("expected code MALFORMED_JSON, got %q", errEnv.Error.Code)
		}
	})

	t.Run("invalid arguments", func(t *testing.T) {
		body := `{"name":"","kind":"http","target":"https://example.com"}`
		resp, err := http.Post(ts.URL+"/monitors", "application/json", bytes.NewBufferString(body))
		if err != nil {
			t.Fatalf("POST failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400 Bad Request, got %d", resp.StatusCode)
		}

		var errEnv api.ErrorEnvelope
		_ = json.NewDecoder(resp.Body).Decode(&errEnv)
		if errEnv.Error.Code != "INVALID_ARGUMENT" {
			t.Errorf("expected code INVALID_ARGUMENT, got %q", errEnv.Error.Code)
		}
	})
}

func TestAPI_ListAndGet(t *testing.T) {
	ts, _, _ := setupAPITestServer(t)
	defer ts.Close()

	t.Run("empty list returns JSON array", func(t *testing.T) {
		resp, err := http.Get(ts.URL + "/monitors")
		if err != nil {
			t.Fatalf("GET /monitors failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
		}

		var list []api.MonitorResponse
		if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
			t.Fatalf("decode failed: %v", err)
		}
		if list == nil || len(list) != 0 {
			t.Fatalf("expected non-nil empty slice, got %+v", list)
		}
	})

	t.Run("create and get by ID", func(t *testing.T) {
		createBody := `{"name":"Get Test","kind":"http","target":"https://example.com"}`
		createResp, err := http.Post(ts.URL+"/monitors", "application/json", bytes.NewBufferString(createBody))
		if err != nil {
			t.Fatalf("POST failed: %v", err)
		}
		defer createResp.Body.Close()

		var created api.MonitorResponse
		_ = json.NewDecoder(createResp.Body).Decode(&created)

		getResp, err := http.Get(ts.URL + "/monitors/" + created.ID)
		if err != nil {
			t.Fatalf("GET failed: %v", err)
		}
		defer getResp.Body.Close()

		if getResp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d", getResp.StatusCode)
		}

		var got api.MonitorResponse
		_ = json.NewDecoder(getResp.Body).Decode(&got)
		if got.ID != created.ID || got.Name != "Get Test" {
			t.Errorf("unexpected monitor: %+v", got)
		}
	})

	t.Run("invalid UUID", func(t *testing.T) {
		resp, err := http.Get(ts.URL + "/monitors/not-a-uuid")
		if err != nil {
			t.Fatalf("GET failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400 Bad Request, got %d", resp.StatusCode)
		}
		var errEnv api.ErrorEnvelope
		_ = json.NewDecoder(resp.Body).Decode(&errEnv)
		if errEnv.Error.Code != "INVALID_ID" {
			t.Errorf("expected INVALID_ID, got %q", errEnv.Error.Code)
		}
	})

	t.Run("missing monitor", func(t *testing.T) {
		resp, err := http.Get(ts.URL + "/monitors/00000000-0000-0000-0000-000000000000")
		if err != nil {
			t.Fatalf("GET failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("expected 404 Not Found, got %d", resp.StatusCode)
		}
	})
}

func TestAPI_PatchAndDelete(t *testing.T) {
	ts, _, sched := setupAPITestServer(t)
	defer ts.Close()

	// Create monitor
	createBody := `{"name":"Patch Test","kind":"http","target":"https://example.com"}`
	createResp, err := http.Post(ts.URL+"/monitors", "application/json", bytes.NewBufferString(createBody))
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer createResp.Body.Close()

	var created api.MonitorResponse
	_ = json.NewDecoder(createResp.Body).Decode(&created)

	t.Run("valid patch", func(t *testing.T) {
		patchBody := `{"name":"Updated Name","interval_ms":10000,"enabled":false}`
		req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/monitors/"+created.ID, bytes.NewBufferString(patchBody))
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("PATCH failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
		}

		var updated api.MonitorResponse
		_ = json.NewDecoder(resp.Body).Decode(&updated)
		if updated.Name != "Updated Name" || updated.IntervalMs != 10000 || updated.Enabled != false {
			t.Errorf("unexpected patched monitor: %+v", updated)
		}
	})

	t.Run("immutable kind patch rejected", func(t *testing.T) {
		patchBody := `{"kind":"tcp"}`
		req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/monitors/"+created.ID, bytes.NewBufferString(patchBody))
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("PATCH failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400 Bad Request, got %d", resp.StatusCode)
		}
	})

	t.Run("delete monitor", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/monitors/"+created.ID, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("DELETE failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("expected 204 No Content, got %d", resp.StatusCode)
		}

		if len(sched.stopCalls) != 1 || sched.stopCalls[0] != created.ID {
			t.Errorf("expected StopMonitor called before deletion")
		}

		// Verify GET returns 404
		getResp, err := http.Get(ts.URL + "/monitors/" + created.ID)
		if err != nil {
			t.Fatalf("GET failed: %v", err)
		}
		defer getResp.Body.Close()
		if getResp.StatusCode != http.StatusNotFound {
			t.Fatalf("expected 404 Not Found after delete, got %d", getResp.StatusCode)
		}
	})
}

func TestAPI_StatusAndChecks(t *testing.T) {
	ts, repo, _ := setupAPITestServer(t)
	defer ts.Close()

	// Create monitor
	createBody := `{"name":"Status History Test","kind":"http","target":"https://example.com"}`
	createResp, err := http.Post(ts.URL+"/monitors", "application/json", bytes.NewBufferString(createBody))
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer createResp.Body.Close()

	var created api.MonitorResponse
	_ = json.NewDecoder(createResp.Body).Decode(&created)

	t.Run("initial status is UNKNOWN", func(t *testing.T) {
		resp, err := http.Get(ts.URL + "/monitors/" + created.ID + "/status")
		if err != nil {
			t.Fatalf("GET status failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
		}

		var st api.MonitorStatusResponse
		_ = json.NewDecoder(resp.Body).Decode(&st)
		if st.State != "UNKNOWN" || st.MonitorID != created.ID {
			t.Errorf("unexpected status response: %+v", st)
		}
	})

	t.Run("health failure still returns 200 OK with UNHEALTHY state", func(t *testing.T) {
		failCycle := checker.CheckResult{
			MonitorID:    created.ID,
			OK:           false,
			StatusCode:   500,
			Latency:      120 * time.Millisecond,
			ErrorClass:   checker.ErrorClassStatus,
			ErrorDetail:  "status code 500",
			AttemptCount: 3,
			CheckedAt:    time.Now().UTC(),
		}
		_ = repo.SaveCycle(context.Background(), created.ID, failCycle, state.StateUnhealthy)

		resp, err := http.Get(ts.URL + "/monitors/" + created.ID + "/status")
		if err != nil {
			t.Fatalf("GET status failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
		}

		var st api.MonitorStatusResponse
		_ = json.NewDecoder(resp.Body).Decode(&st)
		if st.State != "UNHEALTHY" {
			t.Errorf("expected state UNHEALTHY, got %q", st.State)
		}
	})

	t.Run("checks history and limit", func(t *testing.T) {
		resp, err := http.Get(ts.URL + "/monitors/" + created.ID + "/checks?limit=10")
		if err != nil {
			t.Fatalf("GET checks failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
		}

		var checks []api.CheckResultResponse
		_ = json.NewDecoder(resp.Body).Decode(&checks)
		if len(checks) != 1 {
			t.Fatalf("expected 1 check, got %d", len(checks))
		}
		if checks[0].StatusCode != 500 || checks[0].OK != false {
			t.Errorf("unexpected check result: %+v", checks[0])
		}
	})
}
