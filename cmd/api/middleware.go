package main

import (
	"net"
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
				"error": "missing or invalid bearer token",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Rate-limit tuning. Buckets refill at rateLimitPerSecond, burst up to
// rateLimitBurstCap. Idle buckets are swept so the per-IP map cannot grow
// without bound under address churn.
const (
	rateLimitPerSecond = 10
	rateLimitBurstCap  = 100
	bucketIdleTTL      = 10 * time.Minute
	bucketSweepEvery   = time.Minute
)

// rateLimitMiddleware is a fixed-window token bucket per client IP.
// Control endpoints are bursty (rare) so this is intentionally lenient;
// research / alerts can serve rateLimitPerSecond sustained.
func rateLimitMiddleware(next http.Handler) http.Handler {
	type bucket struct {
		tokens int
		last   time.Time
	}

	var (
		mu        sync.Mutex
		buckets   = make(map[string]*bucket)
		lastSweep = time.Now()
	)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := clientKey(r.RemoteAddr)
		now := time.Now()

		mu.Lock()
		// Opportunistic sweep: drop buckets idle past the TTL.
		if now.Sub(lastSweep) > bucketSweepEvery {
			for ip, b := range buckets {
				if now.Sub(b.last) > bucketIdleTTL {
					delete(buckets, ip)
				}
			}
			lastSweep = now
		}

		b, ok := buckets[key]
		if !ok {
			b = &bucket{tokens: rateLimitPerSecond, last: now}
			buckets[key] = b
		}
		// Refill.
		elapsed := now.Sub(b.last)
		refill := int(elapsed.Seconds() * rateLimitPerSecond)
		if refill > 0 {
			b.tokens = min(rateLimitBurstCap, b.tokens+refill)
			b.last = now
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

// clientKey extracts the client IP from RemoteAddr. net.SplitHostPort handles
// IPv6 literals ("[::1]:8080") correctly — a naive cut at the first colon
// would collapse every IPv6 client into one shared bucket.
func clientKey(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}
