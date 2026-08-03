package notifier

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Regression: the alert engine marshals triggered_at as time.Time (RFC3339
// string). AlertInfo previously declared it int64, so EVERY payload unmarshal
// errored and alerts were dispatched with a spurious warning. The payload
// shape below mirrors internal/core/alert/engine.go.
func TestAlertInfo_UnmarshalsEnginePayload(t *testing.T) {
	// Arrange — same field set and types as the engine's outbox payload.
	payload, err := json.Marshal(map[string]interface{}{
		"alert_id":     "alert-042",
		"title":        "DXY above 105",
		"severity":     "warning",
		"metric_id":    "us.mkt.dollar_index",
		"rule_id":      7,
		"triggered_at": time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}

	// Act
	var parsed AlertInfo
	err = json.Unmarshal(payload, &parsed)

	// Assert
	if err != nil {
		t.Fatalf("AlertInfo must unmarshal the engine payload cleanly, got: %v", err)
	}
	if parsed.AlertID != "alert-042" || parsed.RuleID != 7 {
		t.Errorf("fields not populated: %+v", parsed)
	}
	if parsed.TriggeredAt.IsZero() {
		t.Error("TriggeredAt should be populated from RFC3339 string")
	}
}

// Regression: url.Error embeds the full request URL (bot token / api key) in
// its message; sanitizeHTTPErr must strip it before the error reaches logs.
func TestSanitizeHTTPErr_StripsURL(t *testing.T) {
	inner := errors.New("connection refused")
	uerr := &url.Error{
		Op:  "Post",
		URL: "https://api.telegram.org/botSECRET-TOKEN/sendMessage",
		Err: inner,
	}

	got := sanitizeHTTPErr(fmt.Errorf("wrapped: %w", uerr))

	if strings.Contains(got.Error(), "SECRET-TOKEN") {
		t.Errorf("sanitized error still leaks the URL: %v", got)
	}
	if !strings.Contains(got.Error(), "connection refused") {
		t.Errorf("sanitized error lost the underlying cause: %v", got)
	}
}

// Non-url.Error values pass through unchanged.
func TestSanitizeHTTPErr_PassThrough(t *testing.T) {
	plain := errors.New("some other failure")
	if got := sanitizeHTTPErr(plain); !errors.Is(got, plain) {
		t.Errorf("sanitizeHTTPErr should pass through non-url errors, got %v", got)
	}
}
