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
	verdict := "worth_researching"
	rationale := "clear anomaly"
	db := (&fakeDB{}).stub(newFakeRows([]any{
		"alt_1", "title", "summary", "warning", "metric.id", triggeredAt, "active",
		"yahoo_finance", "real", 2, nil, nil,
		&verdict, &rationale, &triggeredAt,
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
	if got[0]["latest_verdict"] != "worth_researching" {
		t.Errorf("latest_verdict = %v, want worth_researching", got[0]["latest_verdict"])
	}
	if !strings.Contains(db.lastSQL, "a.mode = $") {
		t.Errorf("default alerts query missing mode filter: %s", db.lastSQL)
	}
	if len(db.lastArgs) < 2 || db.lastArgs[0] != "active" || db.lastArgs[1] != "live" {
		t.Errorf("default alerts args = %#v, want status=active mode=live", db.lastArgs)
	}
}

func TestAlertsHandlerFiltersByMode(t *testing.T) {
	triggeredAt := time.Date(2026, 8, 7, 2, 3, 4, 0, time.UTC)
	db := (&fakeDB{}).stub(newFakeRows([]any{
		"alt_obs", "observe title", "summary", "info", "metric.id", triggeredAt, "active",
		"fred", "real", 1, nil, nil,
		nil, nil, nil,
	}))
	defer db.exhausted(t)

	req := httptest.NewRequest(http.MethodGet, "/api/alerts?mode=observe", nil).WithContext(context.Background())
	rec := httptest.NewRecorder()
	alertsHandler(db).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(db.lastSQL, "a.mode = $") {
		t.Errorf("observe query missing mode filter: %s", db.lastSQL)
	}
	if len(db.lastArgs) < 2 || db.lastArgs[1] != "observe" {
		t.Errorf("observe args = %#v, want mode=observe", db.lastArgs)
	}
}

func TestAlertsHandlerRejectsIllegalMode(t *testing.T) {
	db := &fakeDB{}
	defer db.exhausted(t)
	req := httptest.NewRequest(http.MethodGet, "/api/alerts?mode=all", nil)
	rec := httptest.NewRecorder()
	alertsHandler(db).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for illegal mode", rec.Code)
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
		nil, nil, nil,
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

func TestAlertsHandlerPagination(t *testing.T) {
	triggeredAt := time.Date(2026, 8, 7, 2, 3, 4, 0, time.UTC)
	resolvedAt := time.Date(2026, 8, 7, 3, 0, 0, 0, time.UTC)

	// Test envelope=true
	db := (&fakeDB{}).
		stub(newFakeRows([]any{12})). // COUNT(*)
		stub(newFakeRows([]any{
			"alt_res1", "title 1", "sum 1", "warning", "metric.id", triggeredAt, "resolved",
			"fred", "real", 1, nil, &resolvedAt,
			nil, nil, nil,
		}, []any{
			"alt_res2", "title 2", "sum 2", "info", "metric.id", triggeredAt, "resolved",
			"fred", "real", 1, nil, &resolvedAt,
			nil, nil, nil,
		}))
	defer db.exhausted(t)

	req := httptest.NewRequest(http.MethodGet, "/api/alerts?status=resolved&page=2&limit=2&envelope=true", nil).WithContext(context.Background())
	rec := httptest.NewRecorder()
	alertsHandler(db).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	if rec.Header().Get("X-Total-Count") != "12" {
		t.Errorf("X-Total-Count = %s, want 12", rec.Header().Get("X-Total-Count"))
	}
	if rec.Header().Get("X-Page") != "2" {
		t.Errorf("X-Page = %s, want 2", rec.Header().Get("X-Page"))
	}
	if rec.Header().Get("X-Page-Size") != "2" {
		t.Errorf("X-Page-Size = %s, want 2", rec.Header().Get("X-Page-Size"))
	}
	if rec.Header().Get("X-Total-Pages") != "6" {
		t.Errorf("X-Total-Pages = %s, want 6", rec.Header().Get("X-Total-Pages"))
	}

	var envResp struct {
		Items      []map[string]any `json:"items"`
		Total      int              `json:"total"`
		Page       int              `json:"page"`
		PageSize   int              `json:"page_size"`
		TotalPages int              `json:"total_pages"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&envResp); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if envResp.Total != 12 || envResp.Page != 2 || envResp.PageSize != 2 || envResp.TotalPages != 6 {
		t.Errorf("envelope metadata mismatch: %+v", envResp)
	}
	if len(envResp.Items) != 2 {
		t.Errorf("envelope items len = %d, want 2", len(envResp.Items))
	}

	// Test envelope=false (headers set, returns raw array)
	dbArray := (&fakeDB{}).
		stub(newFakeRows([]any{5})). // COUNT(*)
		stub(newFakeRows([]any{
			"alt_all1", "title 1", "sum 1", "warning", "metric.id", triggeredAt, "active",
			"fred", "real", 1, nil, nil,
			nil, nil, nil,
		}))
	defer dbArray.exhausted(t)

	reqArr := httptest.NewRequest(http.MethodGet, "/api/alerts?status=all&page=1&limit=10", nil).WithContext(context.Background())
	recArr := httptest.NewRecorder()
	alertsHandler(dbArray).ServeHTTP(recArr, reqArr)

	if recArr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recArr.Code)
	}
	if recArr.Header().Get("X-Total-Count") != "5" {
		t.Errorf("X-Total-Count = %s, want 5", recArr.Header().Get("X-Total-Count"))
	}
	var arrResp []map[string]any
	if err := json.NewDecoder(recArr.Body).Decode(&arrResp); err != nil {
		t.Fatalf("decode array response: %v", err)
	}
	if len(arrResp) != 1 {
		t.Errorf("arr items len = %d, want 1", len(arrResp))
	}
}
