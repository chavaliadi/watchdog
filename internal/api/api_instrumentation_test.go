package api_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chavaliadi/watchdog/internal/api"
	"github.com/chavaliadi/watchdog/internal/monitor"
	"github.com/chavaliadi/watchdog/internal/service"
)

type recordedHTTPRequest struct {
	method     string
	route      string
	statusCode int
	duration   time.Duration
}

type mockAPIRecorder struct {
	mu           sync.Mutex
	httpRequests []recordedHTTPRequest
}

func (m *mockAPIRecorder) RecordHTTPRequest(method, route string, statusCode int, duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.httpRequests = append(m.httpRequests, recordedHTTPRequest{
		method:     method,
		route:      route,
		statusCode: statusCode,
		duration:   duration,
	})
}

func (m *mockAPIRecorder) RecordCheck(kind string, ok bool, errClass string, duration time.Duration) {}
func (m *mockAPIRecorder) RecordRetry(kind string, outcome string)                                   {}
func (m *mockAPIRecorder) RecordCycleError(stage string)                                            {}
func (m *mockAPIRecorder) RecordDBOperation(op string, ok bool, duration time.Duration)            {}
func (m *mockAPIRecorder) RecordActiveMonitors(kind string, count int)                              {}

func TestAPI_Instrumentation(t *testing.T) {
	recorder := &mockAPIRecorder{}
	repo := newMockAPIRepo()
	_ = repo.CreateMonitor(context.Background(), monitor.Monitor{
		ID:        "550e8400-e29b-41d4-a716-446655440000",
		Name:      "Test",
		Kind:      monitor.KindHTTP,
		TargetURL: "http://example.com",
		Interval:  time.Minute,
		Timeout:   5 * time.Second,
		Enabled:   true,
	})

	svc := service.NewMonitorService(repo, nil)
	handlers := api.NewHandlers(svc, api.WithRecorder(recorder))
	server := api.NewServer(api.Config{}, handlers)

	// 1. GET /monitors
	req1 := httptest.NewRequest(http.MethodGet, "/monitors", nil)
	rr1 := httptest.NewRecorder()
	server.Handler().ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr1.Code)
	}

	// 2. GET /monitors/550e8400-e29b-41d4-a716-446655440000
	req2 := httptest.NewRequest(http.MethodGet, "/monitors/550e8400-e29b-41d4-a716-446655440000", nil)
	rr2 := httptest.NewRecorder()
	server.Handler().ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr2.Code)
	}

	// 3. POST /monitors (invalid JSON -> 400)
	req3 := httptest.NewRequest(http.MethodPost, "/monitors", bytes.NewBufferString("{invalid}"))
	rr3 := httptest.NewRecorder()
	server.Handler().ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr3.Code)
	}

	// 4. GET /livez
	req4 := httptest.NewRequest(http.MethodGet, "/livez", nil)
	rr4 := httptest.NewRecorder()
	server.Handler().ServeHTTP(rr4, req4)
	if rr4.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr4.Code)
	}

	recorder.mu.Lock()
	defer recorder.mu.Unlock()

	if len(recorder.httpRequests) != 4 {
		t.Fatalf("expected 4 recorded HTTP requests, got %d", len(recorder.httpRequests))
	}

	// Verify req 1
	if recorder.httpRequests[0].method != "GET" || recorder.httpRequests[0].route != "/monitors" || recorder.httpRequests[0].statusCode != 200 {
		t.Errorf("req 1 unexpected: %+v", recorder.httpRequests[0])
	}
	if recorder.httpRequests[0].duration <= 0 {
		t.Errorf("req 1 duration must be > 0, got %v", recorder.httpRequests[0].duration)
	}

	// Verify req 2: Route pattern MUST be /monitors/{id}, NEVER the UUID!
	if recorder.httpRequests[1].method != "GET" || recorder.httpRequests[1].route != "/monitors/{id}" || recorder.httpRequests[1].statusCode != 200 {
		t.Errorf("req 2 unexpected: %+v", recorder.httpRequests[1])
	}
	if strings.Contains(recorder.httpRequests[1].route, "550e8400") {
		t.Errorf("route must not contain raw UUID, got %q", recorder.httpRequests[1].route)
	}

	// Verify req 3: POST /monitors with status 400
	if recorder.httpRequests[2].method != "POST" || recorder.httpRequests[2].route != "/monitors" || recorder.httpRequests[2].statusCode != 400 {
		t.Errorf("req 3 unexpected: %+v", recorder.httpRequests[2])
	}

	// Verify req 4: GET /livez with status 200
	if recorder.httpRequests[3].method != "GET" || recorder.httpRequests[3].route != "/livez" || recorder.httpRequests[3].statusCode != 200 {
		t.Errorf("req 4 unexpected: %+v", recorder.httpRequests[3])
	}
}
