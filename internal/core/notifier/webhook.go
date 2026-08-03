package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// Webhook POSTs a JSON AlertInfo payload to a generic HTTP endpoint. Useful for
// Slack/Discord/Prometheus-Alertmanager bridges: set WEBHOOK_URL to the
// ingress endpoint.
//
// Env:
//   - WEBHOOK_URL — destination URL. Missing -> returns (nil, false).
//   - WEBHOOK_TOKEN — optional bearer-token set as Authorization: Bearer <t>.
type Webhook struct {
	url    string
	token  string
	client *http.Client
}

// NewWebhook constructs a Webhook notifier if WEBHOOK_URL is present.
// Returns false when unset so callers can degrade to NopNotifier.
func NewWebhook() (*Webhook, bool) {
	url := os.Getenv("WEBHOOK_URL")
	if url == "" {
		return nil, false
	}
	return &Webhook{
		url:    url,
		token:  os.Getenv("WEBHOOK_TOKEN"),
		client: &http.Client{Timeout: 10 * time.Second},
	}, true
}

// Notify POSTs the alert as JSON. Any non-2xx status is retryable.
func (w *Webhook) Notify(ctx context.Context, a AlertInfo) error {
	payload, err := json.Marshal(a)
	if err != nil {
		return fmt.Errorf("marshal webhook payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if w.token != "" {
		req.Header.Set("Authorization", "Bearer "+w.token)
	}
	resp, err := w.client.Do(req)
	if err != nil {
		// sanitizeHTTPErr: webhook URLs may embed secrets — keep them out of logs.
		return fmt.Errorf("webhook send: %w", sanitizeHTTPErr(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		// 429 / 5xx are retryable; the outbox worker handles retry.
		return fmt.Errorf("webhook returned %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// Compile-time interface check.
var _ Notifier = (*Webhook)(nil)
