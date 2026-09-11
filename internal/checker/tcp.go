package checker

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/chavaliadi/watchdog/internal/monitor"
)

// TCPDialer represents the dialer contract for establishing TCP connections.
// *net.Dialer from the Go standard library satisfies this interface.
type TCPDialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

// TCPChecker performs a single TCP connection attempt against a target monitor.
type TCPChecker struct {
	dialer TCPDialer
}

// Ensure TCPChecker satisfies the Checker interface at compile time.
var _ Checker = (*TCPChecker)(nil)

// NewTCPChecker creates a new TCPChecker with the specified TCP dialer.
// If dialer is nil, a default &net.Dialer{} is used.
func NewTCPChecker(dialer TCPDialer) *TCPChecker {
	if dialer == nil {
		dialer = &net.Dialer{}
	}
	return &TCPChecker{dialer: dialer}
}

// Check performs exactly one TCP check attempt against the target monitor.
func (c *TCPChecker) Check(ctx context.Context, m monitor.Monitor) (CheckResult, error) {
	if ctx == nil {
		return CheckResult{}, errors.New("nil context provided to TCPChecker")
	}

	targetAddr, err := validateTCPMonitor(m)
	if err != nil {
		return CheckResult{}, err
	}

	start := time.Now()
	conn, dialErr := c.dialer.DialContext(ctx, "tcp", targetAddr)
	latency := time.Since(start)

	if dialErr != nil {
		return CheckResult{
			MonitorID:    m.ID,
			CheckedAt:    start.UTC(),
			OK:           false,
			Latency:      latency,
			ErrorClass:   classifyNetworkError(ctx, dialErr),
			ErrorDetail:  dialErr.Error(),
			AttemptCount: 1,
		}, nil
	}

	defer func() {
		_ = conn.Close() // Best effort close on check completion.
	}()

	return CheckResult{
		MonitorID:    m.ID,
		CheckedAt:    start.UTC(),
		OK:           true,
		Latency:      latency,
		AttemptCount: 1,
	}, nil
}

func validateTCPMonitor(m monitor.Monitor) (string, error) {
	if m.Kind != monitor.KindTCP {
		return "", fmt.Errorf("unsupported monitor kind %q: TCPChecker requires kind %q", m.Kind, monitor.KindTCP)
	}

	target := strings.TrimSpace(m.TargetURL)
	if target == "" {
		return "", errors.New("target URL must not be empty")
	}

	if strings.Contains(target, "://") {
		return "", fmt.Errorf("invalid TCP target %q: scheme not allowed, expected host:port", m.TargetURL)
	}

	host, port, err := net.SplitHostPort(target)
	if err != nil {
		return "", fmt.Errorf("invalid TCP target %q: %w", m.TargetURL, err)
	}

	if strings.TrimSpace(host) == "" {
		return "", fmt.Errorf("invalid TCP target %q: host must not be empty", m.TargetURL)
	}

	portNum, err := strconv.Atoi(port)
	if err != nil || portNum < 1 || portNum > 65535 {
		return "", fmt.Errorf("invalid TCP target %q: port %q must be between 1 and 65535", m.TargetURL, port)
	}

	return net.JoinHostPort(host, port), nil
}
