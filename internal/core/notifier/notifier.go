// Package notifier delivers alert notifications to external channels
// (Telegram, generic webhook). It is consumed by the alert.outboxWorker
// handler so that every alert.triggered event can fan out to a real destination.
//
// Design notes
//
//   - The handler in cmd/core/main.go owns the noise-budget bookkeeping; the
//     notifier only sends. If sending fails the handler returns the error so the
//     outbox retry machinery (already implemented) owns redelivery -- do not
//     implement your own retry loop here.
//   - Credentials are read from env at Use- time (NewTelegram, NewWebhook), not
//     from flags; this keeps the package agnostic to deployment style.
package notifier

import (
	"context"
	"errors"
	"net/url"
	"time"
)

// AlertInfo is the payload the notifier needs to render + send an alert.
// It mirrors the JSON shape the alert engine writes into event_outbox.payload
// (see internal/core/alert/engine.go — triggered_at is a time.Time / RFC3339
// string there, so it must be time.Time here or every unmarshal errors).
type AlertInfo struct {
	AlertID     string    `json:"alert_id"`
	Title       string    `json:"title"`
	Severity    string    `json:"severity"`
	MetricID    string    `json:"metric_id"`
	RuleID      int       `json:"rule_id"`
	TriggeredAt time.Time `json:"triggered_at"`
}

// sanitizeHTTPErr strips the request URL from HTTP-transport errors. url.Error
// embeds the full URL in its message, which for Telegram contains the bot token
// (path) and for token-bearing webhooks the secret — those must not reach logs.
func sanitizeHTTPErr(err error) error {
	var uerr *url.Error
	if errors.As(err, &uerr) {
		return uerr.Err
	}
	return err
}

// Notifier delivers one alert to an external destination.
type Notifier interface {
	// Notify sends the alert. Returns an error if the destination is unreachable
	// or rejects the payload -- the outbox worker treats non-nil as retryable.
	Notify(ctx context.Context, a AlertInfo) error
}

// NopNotifier drops alerts silently; it is the default when no channel is
// configured, so that unit tests and local dev do not crash on missing creds.
type NopNotifier struct{}

// Notify implements Notifier by doing nothing.
func (NopNotifier) Notify(context.Context, AlertInfo) error { return nil }
