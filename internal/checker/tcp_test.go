package checker

import (
	"context"
	"errors"
	"io"
	"net"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/chavaliadi/watchdog/internal/monitor"
)

type mockDialer struct {
	dialFunc func(ctx context.Context, network, address string) (net.Conn, error)
	calls    atomic.Int32
}

func (m *mockDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	m.calls.Add(1)
	if m.dialFunc != nil {
		return m.dialFunc(ctx, network, address)
	}
	return nil, errors.New("mockDialer: dialFunc not defined")
}

type trackCloseConn struct {
	net.Conn
	closed atomic.Bool
}

func (c *trackCloseConn) Close() error {
	c.closed.Store(true)
	if c.Conn != nil {
		return c.Conn.Close()
	}
	return nil
}

func TestTCPChecker_SuccessLocalListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start local listener: %v", err)
	}
	defer ln.Close()

	serverConnChan := make(chan net.Conn, 1)
	go func() {
		conn, acceptErr := ln.Accept()
		if acceptErr == nil {
			serverConnChan <- conn
		}
	}()

	c := NewTCPChecker(nil)
	m := monitor.Monitor{
		ID:        "mon-tcp-1",
		Kind:      monitor.KindTCP,
		TargetURL: ln.Addr().String(),
	}

	res, err := c.Check(context.Background(), m)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	if !res.OK {
		t.Errorf("expected res.OK to be true, got false")
	}
	if res.MonitorID != "mon-tcp-1" {
		t.Errorf("expected MonitorID 'mon-tcp-1', got %q", res.MonitorID)
	}
	if res.AttemptCount != 1 {
		t.Errorf("expected AttemptCount 1, got %d", res.AttemptCount)
	}
	if res.Latency <= 0 {
		t.Errorf("expected positive latency, got %v", res.Latency)
	}
	if res.ErrorClass != ErrorClassNone {
		t.Errorf("expected ErrorClassNone, got %q", res.ErrorClass)
	}
	if res.ErrorDetail != "" {
		t.Errorf("expected empty ErrorDetail, got %q", res.ErrorDetail)
	}

	// Verify server accepted the connection and that client closed it (receiving EOF)
	select {
	case serverConn := <-serverConnChan:
		defer serverConn.Close()
		_ = serverConn.SetReadDeadline(time.Now().Add(1 * time.Second))
		buf := make([]byte, 1)
		n, readErr := serverConn.Read(buf)
		if readErr != io.EOF {
			t.Errorf("expected server to read io.EOF due to client close, got n=%d, err=%v", n, readErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for server to accept connection")
	}
}

func TestTCPChecker_ConnectionClosedOnSuccess(t *testing.T) {
	connTracker := &trackCloseConn{}
	mock := &mockDialer{
		dialFunc: func(ctx context.Context, network, address string) (net.Conn, error) {
			return connTracker, nil
		},
	}

	c := NewTCPChecker(mock)
	m := monitor.Monitor{
		ID:        "mon-tcp-close",
		Kind:      monitor.KindTCP,
		TargetURL: "127.0.0.1:8080",
	}

	res, err := c.Check(context.Background(), m)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	if !res.OK {
		t.Errorf("expected res.OK to be true")
	}
	if !connTracker.closed.Load() {
		t.Errorf("expected connection to be closed upon check completion")
	}
}

func TestTCPChecker_ConnectionRefusal(t *testing.T) {
	// 1. Using real local closed port
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to bind port: %v", err)
	}
	closedAddr := ln.Addr().String()
	_ = ln.Close() // Immediately close listener so port refuses connections

	c := NewTCPChecker(nil)
	m := monitor.Monitor{
		ID:        "mon-tcp-refused",
		Kind:      monitor.KindTCP,
		TargetURL: closedAddr,
	}

	res, err := c.Check(context.Background(), m)
	if err != nil {
		t.Fatalf("expected nil Go error for connection refusal observation, got: %v", err)
	}

	if res.OK {
		t.Errorf("expected res.OK to be false for refused connection")
	}
	if res.ErrorClass != ErrorClassConnRefused {
		t.Errorf("expected ErrorClassConnRefused, got %q", res.ErrorClass)
	}
	if res.AttemptCount != 1 {
		t.Errorf("expected AttemptCount 1, got %d", res.AttemptCount)
	}
	if res.ErrorDetail == "" {
		t.Errorf("expected non-empty ErrorDetail on connection refusal")
	}

	// 2. Using mock dialer to test typed error unwrapping
	mock := &mockDialer{
		dialFunc: func(ctx context.Context, network, address string) (net.Conn, error) {
			return nil, &net.OpError{
				Op:  "dial",
				Net: "tcp",
				Err: syscall.ECONNREFUSED,
			}
		},
	}

	cMock := NewTCPChecker(mock)
	resMock, errMock := cMock.Check(context.Background(), m)
	if errMock != nil {
		t.Fatalf("expected nil Go error, got: %v", errMock)
	}
	if resMock.OK {
		t.Errorf("expected res.OK to be false")
	}
	if resMock.ErrorClass != ErrorClassConnRefused {
		t.Errorf("expected ErrorClassConnRefused, got %q", resMock.ErrorClass)
	}
}

type mockTimeoutError struct{}

func (mockTimeoutError) Error() string   { return "i/o timeout" }
func (mockTimeoutError) Timeout() bool   { return true }
func (mockTimeoutError) Temporary() bool { return true }

func TestTCPChecker_ContextCancellationAndTimeout(t *testing.T) {
	tests := []struct {
		name          string
		setupCtx      func() (context.Context, context.CancelFunc)
		dialErr       func(ctx context.Context) error
		expectedClass ErrorClass
	}{
		{
			name: "context canceled",
			setupCtx: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, cancel
			},
			dialErr: func(ctx context.Context) error {
				return ctx.Err()
			},
			expectedClass: ErrorClassTimeout,
		},
		{
			name: "context deadline exceeded",
			setupCtx: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-1*time.Minute))
				return ctx, cancel
			},
			dialErr: func(ctx context.Context) error {
				return ctx.Err()
			},
			expectedClass: ErrorClassTimeout,
		},
		{
			name: "dialer timeout net.Error",
			setupCtx: func() (context.Context, context.CancelFunc) {
				return context.Background(), func() {}
			},
			dialErr: func(ctx context.Context) error {
				return mockTimeoutError{}
			},
			expectedClass: ErrorClassTimeout,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := tt.setupCtx()
			defer cancel()

			var capturedCtx context.Context
			mock := &mockDialer{
				dialFunc: func(dCtx context.Context, network, address string) (net.Conn, error) {
					capturedCtx = dCtx
					return nil, tt.dialErr(dCtx)
				},
			}

			c := NewTCPChecker(mock)
			m := monitor.Monitor{
				ID:        "mon-tcp-timeout",
				Kind:      monitor.KindTCP,
				TargetURL: "127.0.0.1:8080",
			}

			res, err := c.Check(ctx, m)
			if err != nil {
				t.Fatalf("expected nil Go error for timeout observation, got: %v", err)
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
			if capturedCtx != ctx {
				t.Errorf("expected provided context to be propagated to dialer")
			}
		})
	}
}

func TestTCPChecker_InvalidConfiguration(t *testing.T) {
	c := NewTCPChecker(nil)

	tests := []struct {
		name    string
		ctx     context.Context
		monitor monitor.Monitor
	}{
		{
			name: "nil context",
			ctx:  nil,
			monitor: monitor.Monitor{
				Kind:      monitor.KindTCP,
				TargetURL: "127.0.0.1:8080",
			},
		},
		{
			name: "wrong kind http",
			ctx:  context.Background(),
			monitor: monitor.Monitor{
				Kind:      monitor.KindHTTP,
				TargetURL: "127.0.0.1:8080",
			},
		},
		{
			name: "wrong kind health",
			ctx:  context.Background(),
			monitor: monitor.Monitor{
				Kind:      monitor.KindHealth,
				TargetURL: "127.0.0.1:8080",
			},
		},
		{
			name: "empty target URL",
			ctx:  context.Background(),
			monitor: monitor.Monitor{
				Kind:      monitor.KindTCP,
				TargetURL: "",
			},
		},
		{
			name: "whitespace target URL",
			ctx:  context.Background(),
			monitor: monitor.Monitor{
				Kind:      monitor.KindTCP,
				TargetURL: "   ",
			},
		},
		{
			name: "scheme tcp:// not allowed",
			ctx:  context.Background(),
			monitor: monitor.Monitor{
				Kind:      monitor.KindTCP,
				TargetURL: "tcp://127.0.0.1:8080",
			},
		},
		{
			name: "scheme http:// not allowed",
			ctx:  context.Background(),
			monitor: monitor.Monitor{
				Kind:      monitor.KindTCP,
				TargetURL: "http://127.0.0.1:8080",
			},
		},
		{
			name: "missing port",
			ctx:  context.Background(),
			monitor: monitor.Monitor{
				Kind:      monitor.KindTCP,
				TargetURL: "127.0.0.1",
			},
		},
		{
			name: "empty host",
			ctx:  context.Background(),
			monitor: monitor.Monitor{
				Kind:      monitor.KindTCP,
				TargetURL: ":8080",
			},
		},
		{
			name: "non-numeric port",
			ctx:  context.Background(),
			monitor: monitor.Monitor{
				Kind:      monitor.KindTCP,
				TargetURL: "127.0.0.1:abc",
			},
		},
		{
			name: "port zero",
			ctx:  context.Background(),
			monitor: monitor.Monitor{
				Kind:      monitor.KindTCP,
				TargetURL: "127.0.0.1:0",
			},
		},
		{
			name: "negative port",
			ctx:  context.Background(),
			monitor: monitor.Monitor{
				Kind:      monitor.KindTCP,
				TargetURL: "127.0.0.1:-5",
			},
		},
		{
			name: "port exceeds 65535",
			ctx:  context.Background(),
			monitor: monitor.Monitor{
				Kind:      monitor.KindTCP,
				TargetURL: "127.0.0.1:65536",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := c.Check(tt.ctx, tt.monitor)
			if err == nil {
				t.Fatalf("expected error for test case %q, got nil", tt.name)
			}
		})
	}
}

func TestTCPChecker_ExactlyOneAttempt(t *testing.T) {
	t.Run("success attempt count", func(t *testing.T) {
		connTracker := &trackCloseConn{}
		mock := &mockDialer{
			dialFunc: func(ctx context.Context, network, address string) (net.Conn, error) {
				return connTracker, nil
			},
		}

		c := NewTCPChecker(mock)
		m := monitor.Monitor{
			ID:        "mon-tcp-count-ok",
			Kind:      monitor.KindTCP,
			TargetURL: "127.0.0.1:8080",
		}

		res, err := c.Check(context.Background(), m)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if mock.calls.Load() != 1 {
			t.Errorf("expected exactly 1 dial call, got %d", mock.calls.Load())
		}
		if res.AttemptCount != 1 {
			t.Errorf("expected AttemptCount 1, got %d", res.AttemptCount)
		}
	})

	t.Run("failure attempt count", func(t *testing.T) {
		mock := &mockDialer{
			dialFunc: func(ctx context.Context, network, address string) (net.Conn, error) {
				return nil, syscall.ECONNREFUSED
			},
		}

		c := NewTCPChecker(mock)
		m := monitor.Monitor{
			ID:        "mon-tcp-count-fail",
			Kind:      monitor.KindTCP,
			TargetURL: "127.0.0.1:8080",
		}

		res, err := c.Check(context.Background(), m)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if mock.calls.Load() != 1 {
			t.Errorf("expected exactly 1 dial call, got %d", mock.calls.Load())
		}
		if res.AttemptCount != 1 {
			t.Errorf("expected AttemptCount 1, got %d", res.AttemptCount)
		}
	})
}

func TestTCPChecker_NetworkErrorClassifications(t *testing.T) {
	tests := []struct {
		name          string
		dialErr       error
		expectedClass ErrorClass
	}{
		{
			name: "dns error",
			dialErr: &net.DNSError{
				Err:  "no such host",
				Name: "nonexistent.domain.local",
			},
			expectedClass: ErrorClassDNS,
		},
		{
			name:          "connection refused",
			dialErr:       syscall.ECONNREFUSED,
			expectedClass: ErrorClassConnRefused,
		},
		{
			name:          "unclassified error",
			dialErr:       errors.New("unrecognized network error"),
			expectedClass: ErrorClassNone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockDialer{
				dialFunc: func(ctx context.Context, network, address string) (net.Conn, error) {
					return nil, tt.dialErr
				},
			}

			c := NewTCPChecker(mock)
			m := monitor.Monitor{
				ID:        "mon-tcp-err-class",
				Kind:      monitor.KindTCP,
				TargetURL: "127.0.0.1:8080",
			}

			res, err := c.Check(context.Background(), m)
			if err != nil {
				t.Fatalf("expected nil Go error, got: %v", err)
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
