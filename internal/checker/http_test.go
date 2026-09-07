package checker

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/chavaliadi/watchdog/internal/monitor"
)

func TestHTTPChecker_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"healthy"}`))
	}))
	defer ts.Close()

	c := NewHTTPChecker(ts.Client())
	m := monitor.Monitor{
		ID:                  "mon-1",
		Kind:                monitor.KindHTTP,
		TargetURL:           ts.URL,
		Method:              "GET",
		ExpectedStatusRange: "200-299",
	}

	res, err := c.Check(context.Background(), m)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	if !res.OK {
		t.Errorf("expected res.OK to be true, got false")
	}
	if res.StatusCode != http.StatusOK {
		t.Errorf("expected status code %d, got %d", http.StatusOK, res.StatusCode)
	}
	if res.ErrorClass != ErrorClassNone {
		t.Errorf("expected ErrorClassNone, got %q", res.ErrorClass)
	}
	if res.MonitorID != "mon-1" {
		t.Errorf("expected MonitorID 'mon-1', got %q", res.MonitorID)
	}
	if res.AttemptCount != 1 {
		t.Errorf("expected AttemptCount 1, got %d", res.AttemptCount)
	}
	if res.Latency <= 0 {
		t.Errorf("expected positive latency, got %v", res.Latency)
	}
}

func TestHTTPChecker_UnexpectedStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	c := NewHTTPChecker(ts.Client())
	m := monitor.Monitor{
		ID:                  "mon-2",
		Kind:                monitor.KindHTTP,
		TargetURL:           ts.URL,
		Method:              "GET",
		ExpectedStatusRange: "200-299",
	}

	res, err := c.Check(context.Background(), m)
	if err != nil {
		t.Fatalf("expected nil error for monitor-level failure, got: %v", err)
	}

	if res.OK {
		t.Errorf("expected res.OK to be false for 500 status")
	}
	if res.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected status code %d, got %d", http.StatusInternalServerError, res.StatusCode)
	}
	if res.ErrorClass != ErrorClassStatus {
		t.Errorf("expected ErrorClassStatus, got %q", res.ErrorClass)
	}
	if res.AttemptCount != 1 {
		t.Errorf("expected AttemptCount 1, got %d", res.AttemptCount)
	}
	if !strings.Contains(res.ErrorDetail, "500") {
		t.Errorf("expected error detail to mention 500, got %q", res.ErrorDetail)
	}
}

func TestHTTPChecker_ConfiguredMethod(t *testing.T) {
	methods := []string{"POST", "PUT", "DELETE", "HEAD", "OPTIONS", "GET"}

	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			var recordedMethod string
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				recordedMethod = r.Method
				w.WriteHeader(http.StatusOK)
			}))
			defer ts.Close()

			c := NewHTTPChecker(ts.Client())
			m := monitor.Monitor{
				ID:                  "mon-method",
				Kind:                monitor.KindHTTP,
				TargetURL:           ts.URL,
				Method:              method,
				ExpectedStatusRange: "200",
			}

			res, err := c.Check(context.Background(), m)
			if err != nil {
				t.Fatalf("expected nil error, got: %v", err)
			}
			if !res.OK {
				t.Errorf("expected check to pass, got false")
			}
			if recordedMethod != method {
				t.Errorf("expected server to receive %q, received %q", method, recordedMethod)
			}
		})
	}
}

func TestHTTPChecker_ContextCancellation(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	c := NewHTTPChecker(ts.Client())
	m := monitor.Monitor{
		ID:                  "mon-timeout",
		Kind:                monitor.KindHTTP,
		TargetURL:           ts.URL,
		Method:              "GET",
		ExpectedStatusRange: "200",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	res, err := c.Check(ctx, m)
	if err != nil {
		t.Fatalf("expected nil error for network timeout observation, got: %v", err)
	}

	if res.OK {
		t.Errorf("expected res.OK to be false on timeout")
	}
	if res.ErrorClass != ErrorClassTimeout {
		t.Errorf("expected ErrorClassTimeout, got %q", res.ErrorClass)
	}
	if res.AttemptCount != 1 {
		t.Errorf("expected AttemptCount 1, got %d", res.AttemptCount)
	}
}

func TestHTTPChecker_InvalidConfiguration(t *testing.T) {
	c := NewHTTPChecker(nil)

	tests := []struct {
		name    string
		ctx     context.Context
		monitor monitor.Monitor
	}{
		{
			name: "nil context",
			ctx:  nil,
			monitor: monitor.Monitor{
				Kind:                monitor.KindHTTP,
				TargetURL:           "http://localhost:8080",
				Method:              "GET",
				ExpectedStatusRange: "200",
			},
		},
		{
			name: "wrong kind",
			ctx:  context.Background(),
			monitor: monitor.Monitor{
				Kind:                monitor.KindTCP,
				TargetURL:           "http://localhost:8080",
				Method:              "GET",
				ExpectedStatusRange: "200",
			},
		},
		{
			name: "empty target URL",
			ctx:  context.Background(),
			monitor: monitor.Monitor{
				Kind:                monitor.KindHTTP,
				TargetURL:           "",
				Method:              "GET",
				ExpectedStatusRange: "200",
			},
		},
		{
			name: "malformed target URL",
			ctx:  context.Background(),
			monitor: monitor.Monitor{
				Kind:                monitor.KindHTTP,
				TargetURL:           "://invalid-url",
				Method:              "GET",
				ExpectedStatusRange: "200",
			},
		},
		{
			name: "unsupported scheme",
			ctx:  context.Background(),
			monitor: monitor.Monitor{
				Kind:                monitor.KindHTTP,
				TargetURL:           "ftp://example.com",
				Method:              "GET",
				ExpectedStatusRange: "200",
			},
		},
		{
			name: "empty method",
			ctx:  context.Background(),
			monitor: monitor.Monitor{
				Kind:                monitor.KindHTTP,
				TargetURL:           "http://localhost:8080",
				Method:              "",
				ExpectedStatusRange: "200",
			},
		},
		{
			name: "unsupported method",
			ctx:  context.Background(),
			monitor: monitor.Monitor{
				Kind:                monitor.KindHTTP,
				TargetURL:           "http://localhost:8080",
				Method:              "INVALID_METHOD",
				ExpectedStatusRange: "200",
			},
		},
		{
			name: "empty status range",
			ctx:  context.Background(),
			monitor: monitor.Monitor{
				Kind:                monitor.KindHTTP,
				TargetURL:           "http://localhost:8080",
				Method:              "GET",
				ExpectedStatusRange: "",
			},
		},
		{
			name: "malformed status range",
			ctx:  context.Background(),
			monitor: monitor.Monitor{
				Kind:                monitor.KindHTTP,
				TargetURL:           "http://localhost:8080",
				Method:              "GET",
				ExpectedStatusRange: "not-a-range",
			},
		},
		{
			name: "inverted status range",
			ctx:  context.Background(),
			monitor: monitor.Monitor{
				Kind:                monitor.KindHTTP,
				TargetURL:           "http://localhost:8080",
				Method:              "GET",
				ExpectedStatusRange: "500-200",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := c.Check(tt.ctx, tt.monitor)
			if err == nil {
				t.Fatalf("expected error for case %q, got nil", tt.name)
			}
		})
	}
}

type trackCloseReader struct {
	io.Reader
	closed atomic.Bool
}

func (r *trackCloseReader) Close() error {
	r.closed.Store(true)
	return nil
}

type mockRoundTripper struct {
	roundTripFunc func(req *http.Request) (*http.Response, error)
}

func (m *mockRoundTripper) Do(req *http.Request) (*http.Response, error) {
	return m.roundTripFunc(req)
}

func TestHTTPChecker_ResponseBodyClosed(t *testing.T) {
	bodyTracker := &trackCloseReader{
		Reader: strings.NewReader("sample response body"),
	}

	mockClient := &mockRoundTripper{
		roundTripFunc: func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       bodyTracker,
				Header:     make(http.Header),
			}, nil
		},
	}

	c := NewHTTPChecker(mockClient)
	m := monitor.Monitor{
		ID:                  "mon-body",
		Kind:                monitor.KindHTTP,
		TargetURL:           "http://localhost/test",
		Method:              "GET",
		ExpectedStatusRange: "200",
	}

	res, err := c.Check(context.Background(), m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.OK {
		t.Errorf("expected res.OK to be true")
	}
	if !bodyTracker.closed.Load() {
		t.Errorf("expected response body to be closed")
	}
}

func TestHTTPChecker_NetworkErrorClassifications(t *testing.T) {
	tests := []struct {
		name          string
		returnErr     error
		expectedClass ErrorClass
	}{
		{
			name: "dns error",
			returnErr: &net.DNSError{
				Err:  "no such host",
				Name: "invalid.domain.local",
			},
			expectedClass: ErrorClassDNS,
		},
		{
			name:          "connection refused",
			returnErr:     syscall.ECONNREFUSED,
			expectedClass: ErrorClassConnRefused,
		},
		{
			name: "connection refused wrapped in net.OpError",
			returnErr: &net.OpError{
				Op:  "dial",
				Net: "tcp",
				Err: syscall.ECONNREFUSED,
			},
			expectedClass: ErrorClassConnRefused,
		},
		{
			name:          "tls record header error",
			returnErr:     tls.RecordHeaderError{Msg: "bad record mac"},
			expectedClass: ErrorClassTLS,
		},
		{
			name:          "tls generic handshake error",
			returnErr:     errors.New("tls: handshake failure"),
			expectedClass: ErrorClassTLS,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &mockRoundTripper{
				roundTripFunc: func(req *http.Request) (*http.Response, error) {
					return nil, tt.returnErr
				},
			}

			c := NewHTTPChecker(mockClient)
			m := monitor.Monitor{
				ID:                  "mon-err",
				Kind:                monitor.KindHTTP,
				TargetURL:           "http://localhost:8080/check",
				Method:              "GET",
				ExpectedStatusRange: "200",
			}

			res, err := c.Check(context.Background(), m)
			if err != nil {
				t.Fatalf("expected nil error (monitor observation), got: %v", err)
			}
			if res.OK {
				t.Errorf("expected res.OK to be false")
			}
			if res.ErrorClass != tt.expectedClass {
				t.Errorf("expected ErrorClass %q, got %q", tt.expectedClass, res.ErrorClass)
			}
			if res.AttemptCount != 1 {
				t.Errorf("expected AttemptCount 1, got %d", res.AttemptCount)
			}
		})
	}
}

func TestHTTPChecker_ExpectedStatusRanges(t *testing.T) {
	tests := []struct {
		name       string
		spec       string
		statusCode int
		expectedOK bool
	}{
		{"exact match", "200", 200, true},
		{"exact mismatch", "200", 201, false},
		{"range match lower", "200-299", 200, true},
		{"range match upper", "200-299", 299, true},
		{"range match middle", "200-299", 204, true},
		{"range mismatch above", "200-299", 300, false},
		{"range mismatch below", "200-299", 199, false},
		{"comma list match first", "200, 204, 301", 200, true},
		{"comma list match middle", "200, 204, 301", 204, true},
		{"comma list match last", "200, 204, 301", 301, true},
		{"comma list mismatch", "200, 204, 301", 404, false},
		{"mixed range and code", "200-204, 301-302", 301, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inRange, err := isInStatusRange(tt.spec, tt.statusCode)
			if err != nil {
				t.Fatalf("unexpected error parsing range %q: %v", tt.spec, err)
			}
			if inRange != tt.expectedOK {
				t.Errorf("range %q for status %d: expected %v, got %v", tt.spec, tt.statusCode, tt.expectedOK, inRange)
			}
		})
	}
}
