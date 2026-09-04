package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAlertsHandlerReturnsFrozenProvenanceAndDedupAudit(t *testing.T) {
	triggeredAt := time.Date(2026, 8, 7, 2, 3, 4, 0, time.UTC)
	db := (&fakeDB{}).stub(newFakeRows([]any{
		"alt_1", "title", "summary", "warning", "metric.id", triggeredAt, "active",
		"yahoo_finance", "real", 2, nil, nil,
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
	if _, ok := got[0]["resolved_at"]; !ok {
		t.Error("resolved_at field is missing")
	}
}

func TestAlertsHandlerFiltersByStatus(t *testing.T) {
	triggeredAt := time.Date(2026, 8, 7, 2, 3, 4, 0, time.UTC)
	resolvedAt := time.Date(2026, 8, 7, 3, 0, 0, 0, time.UTC)
	req := httptest.NewRequest(http.MethodGet, "/api/alerts?status=resolved", nil).WithContext(context.Background())
	rec := httptest.NewRecorder()
	db := (&fakeDB{}).stub(newFakeRows([]any{
		"alt_res", "resolved title", "resolved summary", "info", "metric.id", triggeredAt, "resolved",
		"fred", "real", 1, nil, &resolvedAt,
	}))
	defer db.exhausted(t)
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
	if got[0]["status"] != "resolved" {
		t.Errorf("status = %v, want resolved", got[0]["status"])
	}
}

func TestPatchAlertHandler_Success(t *testing.T) {
	updatedAt := time.Date(2026, 8, 7, 3, 30, 0, 0, time.UTC)
	db := (&fakeDB{}).stub(newFakeRows([]any{
		"alt_1", "acknowledged", updatedAt, nil,
	}))
	defer db.exhausted(t)

	req := httptest.NewRequest(http.MethodPatch, "/api/alerts/alt_1", strings.NewReader(`{"status":"acknowledged"}`))
	req.SetPathValue("id", "alt_1")
	rec := httptest.NewRecorder()
	patchAlertHandler(db).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["id"] != "alt_1" || resp["status"] != "acknowledged" {
		t.Errorf("got %v, want id=alt_1, status=acknowledged", resp)
	}
}

func TestPatchAlertHandler_ValidationAndNotFound(t *testing.T) {
	db := &fakeDB{}
	defer db.exhausted(t)

	// Invalid status
	req := httptest.NewRequest(http.MethodPatch, "/api/alerts/alt_1", strings.NewReader(`{"status":"invalid_status"}`))
	req.SetPathValue("id", "alt_1")
	rec := httptest.NewRecorder()
	patchAlertHandler(db).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for invalid status", rec.Code)
	}

	// Missing ID
	req = httptest.NewRequest(http.MethodPatch, "/api/alerts/", strings.NewReader(`{"status":"resolved"}`))
	rec = httptest.NewRecorder()
	patchAlertHandler(db).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for missing ID", rec.Code)
	}

	// Not found in DB
	db.stub(newFakeRows()) // empty rows causes ErrNoRows on Scan
	req = httptest.NewRequest(http.MethodPatch, "/api/alerts/alt_missing", strings.NewReader(`{"status":"resolved"}`))
	req.SetPathValue("id", "alt_missing")
	rec = httptest.NewRecorder()
	patchAlertHandler(db).ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for missing alert", rec.Code)
	}
}
