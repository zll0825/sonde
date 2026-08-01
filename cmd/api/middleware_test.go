package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// headerToken returns the bearer token to set on a test request.
// We read the token from the env at test execution time so that the
// literal string is never passed through fmt print/verb expansion.
func headerToken(token string) string {
	return "Bearer " + token
}

func TestAuthMiddleware_GET_IsPublic(t *testing.T) {
	called := false
	h := authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/alerts", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !called {
		t.Error("GET request should reach handler without auth")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestAuthMiddleware_POST_NoToken_Rejected(t *testing.T) {
	os.Unsetenv("API_TOKEN")
	h := authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called without token")
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/control/sync", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestAuthMiddleware_POST_WrongToken(t *testing.T) {
	os.Setenv("API_TOKEN", "secret123")
	defer os.Unsetenv("API_TOKEN")
	h := authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called with wrong token")
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/control/sync", nil)
	req.Header.Set("Authorization", headerToken("wrong-token"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestAuthMiddleware_POST_CorrectToken(t *testing.T) {
	os.Setenv("API_TOKEN", "secret123")
	defer os.Unsetenv("API_TOKEN")
	called := false
	h := authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/control/sync", nil)
	req.Header.Set("Authorization", headerToken("secret123"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !called {
		t.Error("handler should be called with correct bearer token")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestRateLimitMiddleware_AllowsBurst(t *testing.T) {
	h := rateLimitMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	// Initial bucket has 10 tokens — first 10 requests succeed.
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/alerts", nil)
		req.RemoteAddr = "10.0.0.1:12345"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("request %d: status = %d, want 200", i, rec.Code)
		}
	}
}

func TestRateLimitMiddleware_BlocksWhenExhausted(t *testing.T) {
	h := rateLimitMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	ip := "10.0.0.99:54321"
	var blocked bool
	for i := 0; i < 15; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/alerts", nil)
		req.RemoteAddr = ip
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			blocked = true
			break
		}
	}
	if !blocked {
		t.Error("expected a 429 after bucket exhaustion")
	}
}
