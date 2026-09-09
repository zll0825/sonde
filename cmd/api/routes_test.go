package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRegisterStaticRoutes(t *testing.T) {
	mux := http.NewServeMux()
	registerStaticRoutes(mux, "../../web")

	// 1. Root / serves landing page (index.html)
	reqRoot := httptest.NewRequest(http.MethodGet, "/", nil)
	rrRoot := httptest.NewRecorder()
	mux.ServeHTTP(rrRoot, reqRoot)

	if rrRoot.Code != http.StatusOK {
		t.Fatalf("expected 200 for /, got %d", rrRoot.Code)
	}
	bodyRoot := rrRoot.Body.String()
	if !strings.Contains(bodyRoot, "Sonde") {
		t.Errorf("expected landing page title in body, got: %s", bodyRoot[:min(200, len(bodyRoot))])
	}
	if !strings.Contains(bodyRoot, "landing-container") {
		t.Errorf("expected landing-container class in body")
	}
	if !strings.Contains(bodyRoot, "hero.title") {
		t.Errorf("expected data-i18n hero.title in landing page")
	}

	// 2. /app serves workbench (app.html)
	reqApp := httptest.NewRequest(http.MethodGet, "/app", nil)
	rrApp := httptest.NewRecorder()
	mux.ServeHTTP(rrApp, reqApp)

	if rrApp.Code != http.StatusOK {
		t.Fatalf("expected 200 for /app, got %d", rrApp.Code)
	}
	bodyApp := rrApp.Body.String()
	if !strings.Contains(bodyApp, "data-tab=\"alerts\"") {
		t.Errorf("expected alerts tab in app.html, got: %s", bodyApp[:min(200, len(bodyApp))])
	}
	if !strings.Contains(bodyApp, "data-tab=\"plugins\"") {
		t.Errorf("expected plugins tab in app.html")
	}
	if !strings.Contains(bodyApp, "status-ticker") {
		t.Errorf("expected status-ticker in app.html header")
	}
	if !strings.Contains(bodyApp, "btn-nav-home") {
		t.Errorf("expected btn-nav-home in app.html header")
	}

	// 3. /app.html directly
	reqAppHTML := httptest.NewRequest(http.MethodGet, "/app.html", nil)
	rrAppHTML := httptest.NewRecorder()
	mux.ServeHTTP(rrAppHTML, reqAppHTML)
	if rrAppHTML.Code != http.StatusOK {
		t.Fatalf("expected 200 for /app.html, got %d", rrAppHTML.Code)
	}

	// 4. Static CSS and JS assets
	for _, asset := range []string{"/src/landing.css", "/src/landing.js", "/src/app.css", "/src/app.js"} {
		req := httptest.NewRequest(http.MethodGet, asset, nil)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("expected 200 for %s, got %d", asset, rr.Code)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
