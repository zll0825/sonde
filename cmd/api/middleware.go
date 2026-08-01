package main

import (
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

// authMiddleware enforces a static bearer-token check on mutating endpoints.
//
// M5 ships with a simple static-token model so the control plane is not
// exposed without credentials. Production deployments should swap this for
// mTLS / OAuth2 in Phase 2.
//
// Token is read from $API_TOKEN. When unset, GET reads are still allowed;
// POST/PUT/DELETE/PATCH return 401.
func authMiddleware(next http.Handler) http.Handler {
	expected := os.Getenv("API_TOKEN")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			// Health + alert list remain public for dashboards.
			next.ServeHTTP(w, r)
			return
		}

		if expected == "" {
			log.Warn().Str("path", r.URL.Path).Str("method", r.Method).
				Msg("control endpoint hit without API_TOKEN configured")
			writeJSON(w, http.StatusUnauthorized, map[string]string{
				"error": "control endpoints require API_TOKEN to be set",
			})
			return
		}

		auth := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(auth, prefix) || strings.TrimPrefix(auth, prefix) != expected {
			writeJSON(w, http.StatusForbidden, map[string]string{
				"error": "missing or invalid bearer <_REDACTED>",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// rateLimitMiddleware is a fixed-window token bucket per client IP.
// Capacity 10 per second, refilled at 10/sec. Control endpoints are bursty
// (rare) so this is intentionally lenient; research / alerts can serve 10/s.
func rateLimitMiddleware(next http.Handler) http.Handler {
	type bucket struct {
		tokens int
		last   time.Time
	}

	var (
		mu      sync.Mutex
		buckets = make(map[string]*bucket)
	)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, _, _ := strings.Cut(r.RemoteAddr, ":")
		mu.Lock()
		b, ok := buckets[ip]
		if !ok {
			b = &bucket{tokens: 10}
			buckets[ip] = b
		}
		// Refill.
		elapsed := time.Since(b.last)
		refill := int(elapsed.Seconds() * 10)
		if refill > 0 {
			b.tokens = min(100, b.tokens+refill) // cap at 100
			b.last = time.Now()
		}
		if b.tokens <= 0 {
			mu.Unlock()
			writeJSON(w, http.StatusTooManyRequests, map[string]string{
				"error": "rate limit exceeded; retry later",
			})
			return
		}
		b.tokens--
		mu.Unlock()

		next.ServeHTTP(w, r)
	})
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
