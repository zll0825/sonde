// Package provider provides shared HTTP client safety primitives for plugin
// collectors: rate limiting, circuit breaking, retry with backoff, and
// 429/Retry-After handling. All collectors should use SafeHTTPClient to
// ensure consistent provider quota protection.
package provider

import (
	"context"
	"io"
	"math"
	"math/rand"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

// SafeHTTPClient wraps an http.Client with rate limiting, circuit breaking,
// and retry-with-backoff for outbound provider requests.
type SafeHTTPClient struct {
	client      *http.Client
	limiter     *TokenBucket
	breaker     *CircuitBreaker
	maxRetries  int
	baseDelay   time.Duration
	maxDelay    time.Duration
	jitterRatio float64       // 0.0-1.0, fraction of delay to randomize
	semaphore   chan struct{} // concurrency limiter; nil when MaxConcurrent<=0

	mu       sync.Mutex
	provider string // for logging context
}

// Config configures a SafeHTTPClient.
type Config struct {
	// ProviderName is used for logging and health metrics.
	ProviderName string
	// Timeout is the per-request HTTP timeout.
	Timeout time.Duration
	// RPS is the maximum requests per second to this provider.
	// Use 0 to disable rate limiting (not recommended).
	RPS float64
	// Burst is the bucket capacity for burst tolerance.
	Burst int
	// MaxRetries is the maximum number of retry attempts on transient failures.
	MaxRetries int
	// BaseDelay is the initial backoff delay between retries.
	BaseDelay time.Duration
	// MaxDelay caps the backoff delay.
	MaxDelay time.Duration
	// JitterRatio adds randomization to backoff to avoid thundering herd.
	// Defaults to 0.25 (25% jitter). Max 0.5.
	JitterRatio float64
	// MaxConcurrent bounds the number of in-flight requests. Default 1.
	// Use 0 to disable (not recommended in production).
	MaxConcurrent int
	// CircuitBreaker configures the failure circuit.
	Circuit CircuitBreakerConfig
}

// CircuitBreakerConfig configures when the circuit opens and how it recovers.
type CircuitBreakerConfig struct {
	// FailureThreshold is the number of consecutive failures before the circuit opens.
	FailureThreshold int
	// OpenDuration is how long the circuit stays open before entering half-open.
	OpenDuration time.Duration
	// HalfOpenMaxRequests limits concurrent test requests in half-open state.
	HalfOpenMaxRequests int
}

// DefaultConfig returns sensible free-tier defaults for public API access.
func DefaultConfig(provider string) Config {
	return Config{
		ProviderName: provider,
		Timeout:      15 * time.Second,
		RPS:          0.5, // 1 request per 2 seconds default
		Burst:        2,   // allow small bursts
		MaxRetries:   3,
		BaseDelay:    1 * time.Second,
		MaxDelay:     30 * time.Second,
		JitterRatio:  0.25,
		Circuit: CircuitBreakerConfig{
			FailureThreshold:    5,
			OpenDuration:        60 * time.Second,
			HalfOpenMaxRequests: 1,
		},
	}
}

// NewSafeHTTPClientWithHTTPClient creates a SafeHTTPClient using the provided
// underlying http.Client. Useful for testing with custom RoundTripper.
func NewSafeHTTPClientWithHTTPClient(cfg Config, httpClient *http.Client) *SafeHTTPClient {
	s := NewSafeHTTPClient(cfg)
	if httpClient == nil {
		return s
	}
	if httpClient.Timeout == 0 {
		httpClient.Timeout = s.client.Timeout
	}
	s.client = httpClient
	return s
}

// NewSafeHTTPClient creates a SafeHTTPClient with the given configuration.
func NewSafeHTTPClient(cfg Config) *SafeHTTPClient {
	if cfg.Timeout == 0 {
		cfg.Timeout = 15 * time.Second
	}
	if cfg.JitterRatio == 0 {
		cfg.JitterRatio = 0.25
	}
	if cfg.JitterRatio > 0.5 {
		cfg.JitterRatio = 0.5
	}
	if cfg.MaxConcurrent == 0 {
		cfg.MaxConcurrent = 1
	}

	s := &SafeHTTPClient{
		client:      &http.Client{Timeout: cfg.Timeout},
		maxRetries:  cfg.MaxRetries,
		baseDelay:   cfg.BaseDelay,
		maxDelay:    cfg.MaxDelay,
		jitterRatio: cfg.JitterRatio,
		provider:    cfg.ProviderName,
		semaphore:   make(chan struct{}, cfg.MaxConcurrent),
	}
	if cfg.RPS > 0 {
		s.limiter = NewTokenBucket(cfg.RPS, cfg.Burst)
	}
	s.breaker = NewCircuitBreaker(cfg.Circuit)
	return s
}

// Do executes an HTTP request with all safety mechanisms applied:
// 1. Rate limiting (wait for token)
// 2. Circuit breaker check (fail fast if open)
// 3. Retry with exponential backoff and jitter on transient failures
// 4. 429/Retry-After handling
func (s *SafeHTTPClient) Do(req *http.Request) (*http.Response, error) {
	// Step 0: concurrency limit
	select {
	case s.semaphore <- struct{}{}:
		defer func() { <-s.semaphore }()
	case <-req.Context().Done():
		return nil, req.Context().Err()
	}

	var lastErr error

	for attempt := 0; attempt <= s.maxRetries; attempt++ {
		// Step 1: Rate limit
		if s.limiter != nil {
			if err := s.limiter.Wait(req.Context()); err != nil {
				return nil, err
			}
		}

		// Step 2: Circuit breaker check
		if !s.breaker.Allow() {
			lastErr = ErrCircuitOpen
			log.Warn().
				Str("provider", s.provider).
				Msg("circuit breaker open; refusing request")
			return nil, lastErr
		}

		// Step 3: Execute request
		resp, err := s.client.Do(req)

		// Step 4: Handle response
		if err != nil {
			lastErr = err
			s.breaker.RecordFailure()
			if attempt < s.maxRetries {
				delay := s.backoffDelay(attempt)
				log.Warn().
					Str("provider", s.provider).
					Int("attempt", attempt+1).
					Dur("retry_in", delay).
					Err(err).
					Msg("request failed; retrying")
				if !s.sleep(req.Context(), delay) {
					return nil, req.Context().Err()
				}
			}
			continue
		}

		// Handle 429 with Retry-After
		if resp.StatusCode == http.StatusTooManyRequests {
			s.breaker.RecordFailure()
			retryAfter := parseRetryAfter(resp)
			if retryAfter > 0 && attempt < s.maxRetries {
				delay := min(retryAfter, s.maxDelay)
				log.Warn().
					Str("provider", s.provider).
					Int("attempt", attempt+1).
					Dur("retry_after", delay).
					Msg("rate limited (429); waiting Retry-After")
				_ = resp.Body.Close()
				if !s.sleep(req.Context(), delay) {
					return nil, req.Context().Err()
				}
				continue
			}
			// No retries left; return the 429 response to caller
			return resp, nil
		}

		// Handle 5xx transient server errors with retry
		if resp.StatusCode >= 500 && resp.StatusCode < 600 {
			s.breaker.RecordFailure()
			if attempt < s.maxRetries {
				_ = resp.Body.Close()
				delay := s.backoffDelay(attempt)
				log.Warn().
					Str("provider", s.provider).
					Int("attempt", attempt+1).
					Int("status", resp.StatusCode).
					Dur("retry_in", delay).
					Msg("server error (5xx); retrying")
				if !s.sleep(req.Context(), delay) {
					return nil, req.Context().Err()
				}
				continue
			}
		}

		// Success or non-retryable status
		if resp.StatusCode < 400 {
			s.breaker.RecordSuccess()
		} else {
			// 4xx (except 429) are client errors, not provider failures
			s.breaker.RecordSuccess()
		}
		return resp, nil
	}

	return nil, lastErr
}

// backoffDelay computes exponential backoff with jitter.
func (s *SafeHTTPClient) backoffDelay(attempt int) time.Duration {
	delay := s.baseDelay * time.Duration(math.Pow(2, float64(attempt)))
	if delay > s.maxDelay {
		delay = s.maxDelay
	}
	if s.jitterRatio > 0 {
		jitter := time.Duration(rand.Float64() * s.jitterRatio * float64(delay))
		delay = delay/2 + jitter
	}
	return delay
}

// sleep waits for the duration but returns false if context is cancelled.
func (s *SafeHTTPClient) sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// parseRetryAfter extracts the Retry-After header value in seconds.
func parseRetryAfter(resp *http.Response) time.Duration {
	h := resp.Header.Get("Retry-After")
	if h == "" {
		return 0
	}
	// Try as seconds first
	if secs, err := strconv.Atoi(h); err == nil {
		return time.Duration(secs) * time.Second
	}
	// Try as HTTP-date
	if t, err := http.ParseTime(h); err == nil {
		return time.Until(t)
	}
	return 0
}

// CircuitState returns the current circuit breaker state for health reporting.
func (s *SafeHTTPClient) CircuitState() string {
	return s.breaker.State()
}

// FailureCount returns the current consecutive failure count from the circuit breaker.
func (s *SafeHTTPClient) FailureCount() int {
	return s.breaker.FailureCount()
}

// min returns the smaller duration.
func min(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

// ReadAll reads the response body with a size limit, for use after Do.
func ReadAll(resp *http.Response, limit int64) ([]byte, error) {
	if limit <= 0 {
		limit = 1 << 20 // 1MB default
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	_ = resp.Body.Close()
	return body, err
}
