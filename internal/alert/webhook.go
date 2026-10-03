package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	// DefaultWebhookTimeout specifies the default bounded duration for webhook delivery.
	DefaultWebhookTimeout = 5 * time.Second

	// maxResponseBodyRead limits response body drainage to avoid memory exhaustion.
	maxResponseBodyRead = 4096
)

// WebhookConfig configures a webhook-based alert notifier.
type WebhookConfig struct {
	WebhookURL string
	Timeout    time.Duration
	Client     *http.Client
}

// WebhookNotifier delivers AlertEvents as JSON POST requests to a webhook URL.
type WebhookNotifier struct {
	webhookURL string
	timeout    time.Duration
	client     *http.Client
}

// NewWebhookNotifier constructs a Notifier from WebhookConfig.
// If WebhookURL is empty, a NoopNotifier is returned, disabling alerts.
func NewWebhookNotifier(cfg WebhookConfig) Notifier {
	if cfg.WebhookURL == "" {
		return NoopNotifier{}
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultWebhookTimeout
	}

	client := cfg.Client
	if client == nil {
		client = &http.Client{
			Timeout: timeout,
		}
	}

	return &WebhookNotifier{
		webhookURL: cfg.WebhookURL,
		timeout:    timeout,
		client:     client,
	}
}

// Notify serializes event to JSON and POSTs it to the configured webhook endpoint.
// It enforces a bounded timeout and response read limit without leaking sensitive URLs in errors.
func (w *WebhookNotifier) Notify(ctx context.Context, event AlertEvent) error {
	if w.webhookURL == "" {
		return nil
	}

	if ctx == nil {
		ctx = context.Background()
	}

	notifyCtx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()

	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal alert event: %w", err)
	}

	req, err := http.NewRequestWithContext(notifyCtx, http.MethodPost, w.webhookURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create webhook request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Deployment-Watchdog-Alert/1.0")

	resp, err := w.client.Do(req)
	if err != nil {
		if errors.Is(notifyCtx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("webhook delivery timed out after %v", w.timeout)
		}
		if errors.Is(notifyCtx.Err(), context.Canceled) {
			return fmt.Errorf("webhook delivery canceled")
		}
		return fmt.Errorf("webhook delivery failed: %w", err)
	}
	defer resp.Body.Close()

	// Drain bounded response body to allow TCP connection reuse
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBodyRead))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook endpoint responded with non-2xx status code: %d", resp.StatusCode)
	}

	return nil
}
