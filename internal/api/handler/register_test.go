package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRegister_HealthAndClustersWithoutStore(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, Deps{})

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/health status = %d, want 200", rec.Code)
	}
	var health map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&health); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	if health["status"] != "ok" {
		t.Errorf("health status = %q, want ok", health["status"])
	}

	req = httptest.NewRequest(http.MethodGet, "/api/clusters/", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/clusters/ status = %d, want 200 (ring fallback)", rec.Code)
	}
	var clusters []any
	if err := json.NewDecoder(rec.Body).Decode(&clusters); err != nil {
		t.Fatalf("decode clusters: %v", err)
	}
	if clusters == nil {
		t.Fatal("clusters JSON is null, want []")
	}
	if len(clusters) != 0 {
		t.Errorf("clusters len = %d, want 0", len(clusters))
	}

	req = httptest.NewRequest(http.MethodGet, "/api/not-a-route", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /api/not-a-route status = %d, want 404", rec.Code)
	}
}
