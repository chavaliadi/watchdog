package checker

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/chavaliadi/watchdog/internal/monitor"
)

// HTTPClient represents the HTTP execution contract, satisfied by *http.Client.
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// HTTPChecker performs a single HTTP health check against a target monitor.
type HTTPChecker struct {
	client HTTPClient
}

// Ensure HTTPChecker satisfies the Checker interface at compile time.
var _ Checker = (*HTTPChecker)(nil)

// NewHTTPChecker creates a new HTTPChecker with the specified HTTP client.
// If client is nil, http.DefaultClient is used.
func NewHTTPChecker(client HTTPClient) *HTTPChecker {
	if client == nil {
		client = http.DefaultClient
	}
	return &HTTPChecker{client: client}
}

// Check performs exactly one HTTP check attempt against the target monitor.
func (c *HTTPChecker) Check(ctx context.Context, m monitor.Monitor) (CheckResult, error) {
	if ctx == nil {
		return CheckResult{}, errors.New("nil context provided to HTTPChecker")
	}

	if err := validateMonitor(m); err != nil {
		return CheckResult{}, err
	}

	req, err := http.NewRequestWithContext(ctx, strings.ToUpper(strings.TrimSpace(m.Method)), m.TargetURL, nil)
	if err != nil {
		return CheckResult{}, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	start := time.Now()
	resp, doErr := c.client.Do(req)
	latency := time.Since(start)

	if doErr != nil {
		return CheckResult{
			MonitorID:    m.ID,
			CheckedAt:    start.UTC(),
			OK:           false,
			Latency:      latency,
			ErrorClass:   classifyNetworkError(ctx, doErr),
			ErrorDetail:  doErr.Error(),
			AttemptCount: 1,
		}, nil
	}

	if resp.Body != nil {
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024*1024))
	}

	inRange, err := isInStatusRange(m.ExpectedStatusRange, resp.StatusCode)
	if err != nil {
		return CheckResult{}, fmt.Errorf("evaluating expected status range: %w", err)
	}

	res := CheckResult{
		MonitorID:    m.ID,
		CheckedAt:    start.UTC(),
		OK:           inRange,
		StatusCode:   resp.StatusCode,
		Latency:      latency,
		AttemptCount: 1,
	}

	if !inRange {
		res.ErrorClass = ErrorClassStatus
		res.ErrorDetail = fmt.Sprintf("HTTP status %d outside expected range %s", resp.StatusCode, m.ExpectedStatusRange)
	}

	return res, nil
}

func validateMonitor(m monitor.Monitor) error {
	if m.Kind != monitor.KindHTTP {
		return fmt.Errorf("unsupported monitor kind %q: HTTPChecker requires kind %q", m.Kind, monitor.KindHTTP)
	}

	if strings.TrimSpace(m.TargetURL) == "" {
		return errors.New("target URL must not be empty")
	}

	parsedURL, err := url.ParseRequestURI(m.TargetURL)
	if err != nil || parsedURL.Scheme == "" || parsedURL.Host == "" {
		return fmt.Errorf("invalid target URL %q", m.TargetURL)
	}

	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return fmt.Errorf("unsupported URL scheme %q: only http and https are supported", parsedURL.Scheme)
	}

	method := strings.ToUpper(strings.TrimSpace(m.Method))
	if method == "" {
		return errors.New("HTTP method must not be empty")
	}

	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodOptions, http.MethodTrace:
	default:
		return fmt.Errorf("unsupported HTTP method %q", m.Method)
	}

	if strings.TrimSpace(m.ExpectedStatusRange) == "" {
		return errors.New("expected status range must not be empty")
	}

	if _, err := isInStatusRange(m.ExpectedStatusRange, 0); err != nil {
		return fmt.Errorf("invalid expected status range %q: %w", m.ExpectedStatusRange, err)
	}

	return nil
}

func isInStatusRange(expectedRange string, statusCode int) (bool, error) {
	trimmed := strings.TrimSpace(expectedRange)
	if trimmed == "" {
		return false, errors.New("status range cannot be empty")
	}

	parts := strings.Split(trimmed, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		if strings.Contains(part, "-") {
			rangeParts := strings.Split(part, "-")
			if len(rangeParts) != 2 {
				return false, fmt.Errorf("invalid status range format: %q", part)
			}
			minCode, err := strconv.Atoi(strings.TrimSpace(rangeParts[0]))
			if err != nil {
				return false, fmt.Errorf("invalid status code: %w", err)
			}
			maxCode, err := strconv.Atoi(strings.TrimSpace(rangeParts[1]))
			if err != nil {
				return false, fmt.Errorf("invalid status code: %w", err)
			}
			if minCode > maxCode {
				return false, fmt.Errorf("invalid range: min %d exceeds max %d", minCode, maxCode)
			}
			if statusCode >= minCode && statusCode <= maxCode {
				return true, nil
			}
		} else {
			code, err := strconv.Atoi(part)
			if err != nil {
				return false, fmt.Errorf("invalid status code: %w", err)
			}
			if statusCode == code {
				return true, nil
			}
		}
	}

	return false, nil
}

func classifyNetworkError(ctx context.Context, err error) ErrorClass {
	if err == nil {
		return ErrorClassNone
	}

	if errors.Is(err, context.DeadlineExceeded) || (ctx != nil && errors.Is(ctx.Err(), context.DeadlineExceeded)) {
		return ErrorClassTimeout
	}
	if errors.Is(err, context.Canceled) || (ctx != nil && errors.Is(ctx.Err(), context.Canceled)) {
		return ErrorClassTimeout
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return ErrorClassTimeout
	}

	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return ErrorClassDNS
	}

	if errors.Is(err, syscall.ECONNREFUSED) {
		return ErrorClassConnRefused
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) && errors.Is(opErr.Err, syscall.ECONNREFUSED) {
		return ErrorClassConnRefused
	}

	var certInvalidErr *x509.CertificateInvalidError
	var unknownAuthErr x509.UnknownAuthorityError
	var hostnameErr x509.HostnameError
	var recordHeaderErr tls.RecordHeaderError
	if errors.As(err, &certInvalidErr) ||
		errors.As(err, &unknownAuthErr) ||
		errors.As(err, &hostnameErr) ||
		errors.As(err, &recordHeaderErr) {
		return ErrorClassTLS
	}

	errStr := strings.ToLower(err.Error())
	if strings.Contains(errStr, "tls:") || strings.Contains(errStr, "certificate") || strings.Contains(errStr, "handshake failure") {
		return ErrorClassTLS
	}

	return ErrorClassNone
}
