// Package notifier 将告警推送到外部通道（Telegram、通用 webhook），由 alert
// outbox 处理器调用，使每条 alert.triggered 事件触达真实目的地。
//
// 设计约定
//
//   - 噪音预算记账归 cmd/core/main.go 的处理器管，本包只负责发送；发送失败
//     返回 error，由 outbox 重试机制接管重投——不要在包内自建重试循环。
//   - 凭据一律在构造时从环境变量读取（NewTelegram / NewWebhook），与部署
//     方式解耦。
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
