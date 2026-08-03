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

// Telegram sends alerts as MarkdownV2 messages to a bot chat.
//
// Set both env vars to enable:
//   - TELEGRAM_BOT_TOKEN — token from @BotFather
//   - TELEGRAM_CHAT_ID   — numeric chat ID (send `/getID` to @userinfobot, or
//     pull it from getUpdates after starting a chat with the bot)
//
// Either missing -> NewTelegram returns (nil, false) so callers can log a
// warning and continue without panicking.
const telegramAPI = "https://api.telegram.org/bot%s/sendMessage"

// Telegram delivers alerts through a Telegram Bot API chat.
type Telegram struct {
	botToken string
	chatID   string
	client   *http.Client
}

// NewTelegram constructs a Telegram notifier if both env vars are present.
// The returned bool reports whether the notifier is live (true) or should be
// silently skipped (false). Callers should fall back to NopNotifier when false.
func NewTelegram() (*Telegram, bool) {
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	chatID := os.Getenv("TELEGRAM_CHAT_ID")
	if token == "" || chatID == "" {
		return nil, false
	}
	return &Telegram{
		botToken: token,
		chatID:   chatID,
		client:   &http.Client{Timeout: 10 * time.Second},
	}, true
}

// Notify sends a MarkdownV2-formatted alert message. Telegram rejects unknown
// formatting entities -> the message is escaped conservatively.
func (t *Telegram) Notify(ctx context.Context, a AlertInfo) error {
	text := formatAlert(a)
	payload, err := json.Marshal(map[string]string{
		"chat_id":    t.chatID,
		"text":       text,
		"parse_mode": "MarkdownV2",
	})
	if err != nil {
		return fmt.Errorf("marshal telegram payload: %w", err)
	}

	url := fmt.Sprintf(telegramAPI, t.botToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build telegram request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		// sanitizeHTTPErr: the request URL embeds the bot token — keep it out of logs.
		return fmt.Errorf("telegram send: %w", sanitizeHTTPErr(err))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		// 429 (rate limit) and 5xx are retryable; the outbox will pick them up.
		return fmt.Errorf("telegram returned %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// formatAlert renders the alert as Telegram MarkdownV2. Characters reserved by
// MarkdownV2 are escaped per Telegram's rules.
func formatAlert(a AlertInfo) string {
	sev := markdownEscape(a.Severity)
	title := markdownEscape(a.Title)
	metric := markdownEscape(a.MetricID)
	alertID := markdownEscape(a.AlertID)

	return fmt.Sprintf(
		"*[%s]* %s\n"+
			"```\n"+
			"metric: %s\n"+
			"rule  : #%d\n"+
			"id    : %s\n"+
			"```",
		sev, title, metric, a.RuleID, alertID,
	)
}

// markdownEscape escapes Telegram's MarkdownV2 reserved characters.
// https://core.telegram.org/bots/api#markdownv2-style
func markdownEscape(s string) string {
	reserved := []rune{'_', '*', '[', ']', '(', ')', '~', '`', '>', '#', '+', '-', '=', '|', '{', '}', '.', '!'}
	var b bytes.Buffer
	for _, r := range s {
		escaped := false
		for _, rr := range reserved {
			if r == rr {
				escaped = true
				break
			}
		}
		if escaped {
			b.WriteRune('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Compile-time interface check.
var _ Notifier = (*Telegram)(nil)
