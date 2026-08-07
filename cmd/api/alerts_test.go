package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAlertsHandlerReturnsFrozenProvenanceAndDedupAudit(t *testing.T) {
	triggeredAt := time.Date(2026, 8, 7, 2, 3, 4, 0, time.UTC)
	db := (&fakeDB{}).stub(newFakeRows([]any{
		"alt_1", "title", "summary", "warning", "metric.id", triggeredAt, "active",
		"yahoo_finance", "real", 2, nil,
	}))
	defer db.exhausted(t)

	req := httptest.NewRequest(http.MethodGet, "/api/alerts", nil).WithContext(context.Background())
	rec := httptest.NewRecorder()
	alertsHandler(db).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got []map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("alerts len = %d, want 1", len(got))
	}
	if got[0]["source_provider"] != "yahoo_finance" || got[0]["source_class"] != "real" {
		t.Errorf("provenance = %v/%v, want yahoo_finance/real", got[0]["source_provider"], got[0]["source_class"])
	}
	if got[0]["dedup_count"] != float64(2) {
		t.Errorf("dedup_count = %v, want 2", got[0]["dedup_count"])
	}
	if _, ok := got[0]["last_deduplicated_at"]; !ok {
		t.Error("last_deduplicated_at field is missing")
	}
}
