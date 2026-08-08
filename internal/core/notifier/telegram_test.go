package notifier

import (
	"context"
	"strings"
	"testing"
)

func TestMarkdownEscape_Reserved(t *testing.T) {
	// MarkdownV2 reserved chars get backslash-escaped.
	in := "hello_world *bold* [link] (url) ~strike"
	got := markdownEscape(in)
	for _, ch := range []string{"_", "*", "[", "]", "(", ")", "~"} {
		if !strings.Contains(got, "\\"+ch) {
			t.Errorf("markdownEscape(%q): missing escape for %q -> %s", in, ch, got)
		}
	}
	// Unreserved chars must NOT be escaped.
	if strings.Contains(got, "\\b") || strings.Contains(got, "\\o") || strings.Contains(got, "\\l") {
		t.Errorf("markdownEscape incorrectly escaped unreserved chars: %s", got)
	}
}

func TestMarkdownEscape_DotsAndExclamation(t *testing.T) {
	got := markdownEscape("rule fired! see details.")
	for _, ch := range []string{"\\.", "\\!"} {
		if !strings.Contains(got, ch) {
			t.Errorf("markdownEscape: %q should contain %q", got, ch)
		}
	}
}

func TestMarkdownEscape_PassDigits(t *testing.T) {
	// Digits should pass through unchanged.
	got := "btc_ass_price"
	out := markdownEscape(got)
	expected := "btc\\_ass\\_price"
	if out != expected {
		t.Errorf("markdownEscape(%q) = %q, want %q", got, out, expected)
	}
}

func TestNopNotify_NoError(t *testing.T) {
	n := NopNotifier{}
	if err := n.Notify(context.Background(), AlertInfo{}); err != nil {
		t.Errorf("NopNotifier.Notify should always return nil, got %v", err)
	}
}

func TestFormatAlert_Structure(t *testing.T) {
	out := formatAlert(AlertInfo{
		AlertID:  "alert-001",
		Title:    "BTC price spike",
		Severity: "critical",
		MetricID: "btc.ass.price",
		RuleID:   3,
	})
	// Line 1 contains severity, line 2 contains title, code fence follows.
	if !strings.Contains(out, "critical") {
		t.Errorf("formatAlert missing severity: %s", out)
	}
	if !strings.Contains(out, "BTC price spike") {
		t.Errorf("formatAlert missing title: %s", out)
	}
	if !strings.Contains(out, "```") {
		t.Errorf("formatAlert missing code fence: %s", out)
	}
	// rule_id is rendered
	if !strings.Contains(out, "#3") {
		t.Errorf("formatAlert missing rule_id: %s", out)
	}
}

func TestFormatAlert_EscapesReservedInTitle(t *testing.T) {
	out := formatAlert(AlertInfo{
		Title: "yield_spike_percentile detected!",
	})
	// The '_' in the rule title should be escaped.
	if !strings.Contains(out, "yield\\_spike\\_percentile") {
		t.Errorf("formatAlert did not escape underscores in title: %s", out)
	}
	// The '!' should also be escaped.
	if !strings.Contains(out, "detected\\!") {
		t.Errorf("formatAlert did not escape '!' in title: %s", out)
	}
}

func TestResolve_FallsBackToNop(t *testing.T) {
	clearNotificationEnv(t)
	n := Resolve()
	if _, ok := n.(NopNotifier); !ok {
		t.Errorf("expected NopNotifier without env creds, got %T", n)
	}
}

func TestResolve_PrefersTelegramWhenConfigured(t *testing.T) {
	clearNotificationEnv(t)
	t.Setenv("TELEGRAM_BOT_TOKEN", "test-token")
	t.Setenv("TELEGRAM_CHAT_ID", "test-chat")

	n := Resolve()
	if _, ok := n.(*Telegram); !ok {
		t.Fatalf("expected Telegram with complete credentials, got %T", n)
	}
}

func TestResolve_FallsBackToWebhookWhenTelegramMissing(t *testing.T) {
	clearNotificationEnv(t)
	t.Setenv("WEBHOOK_URL", "http://127.0.0.1/test")

	n := Resolve()
	if _, ok := n.(*Webhook); !ok {
		t.Fatalf("expected Webhook when Telegram is unavailable, got %T", n)
	}
}

func clearNotificationEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"TELEGRAM_BOT_TOKEN", "TELEGRAM_CHAT_ID", "WEBHOOK_URL", "WEBHOOK_TOKEN"} {
		t.Setenv(key, "")
	}
}
