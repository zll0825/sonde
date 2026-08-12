package provider

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type fakeProviderLimiter struct {
	reserveErr error
	reserves   int
	failures   int
}

func (f *fakeProviderLimiter) Reserve(context.Context, string) error {
	f.reserves++
	return f.reserveErr
}

func (f *fakeProviderLimiter) RecordFailure(context.Context, string) error {
	f.failures++
	return nil
}

func TestSafeHTTPClient_QuotaRejectsBeforeWireRequest(t *testing.T) {
	cfg := DefaultConfig("test")
	cfg.RPS = 0
	cfg.MaxRetries = 0
	safe := NewSafeHTTPClient(cfg)
	limiter := &fakeProviderLimiter{reserveErr: ErrProviderQuotaExceeded}
	safe.SetProviderLimiter(limiter)
	wireCalls := 0
	safe.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		wireCalls++
		return nil, errors.New("must not be called")
	})

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://test.example/data", nil)
	_, err := safe.Do(req)
	if !errors.Is(err, ErrProviderQuotaExceeded) {
		t.Fatalf("Do error = %v, want ErrProviderQuotaExceeded", err)
	}
	if limiter.reserves != 1 || wireCalls != 0 {
		t.Fatalf("reserves=%d wireCalls=%d, want 1 and 0", limiter.reserves, wireCalls)
	}
}

func TestSafeHTTPClient_QuotaReservesEveryRetry(t *testing.T) {
	cfg := DefaultConfig("test")
	cfg.RPS = 0
	cfg.MaxRetries = 1
	cfg.BaseDelay = time.Nanosecond
	cfg.MaxDelay = time.Nanosecond
	safe := NewSafeHTTPClient(cfg)
	limiter := &fakeProviderLimiter{}
	safe.SetProviderLimiter(limiter)
	safe.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Header:     make(http.Header),
		}, nil
	})

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://test.example/data", nil)
	resp, err := safe.Do(req)
	if err != nil {
		t.Fatalf("Do returned error: %v", err)
	}
	_ = resp.Body.Close()
	if limiter.reserves != 2 || limiter.failures != 2 {
		t.Fatalf("reserves=%d failures=%d, want 2 and 2", limiter.reserves, limiter.failures)
	}
}

// TestSafeHTTPClient_Success verifies that a successful request passes through.
func TestSafeHTTPClient_Success(t *testing.T) {
	safe := NewSafeHTTPClient(DefaultConfig("test"))
	safe.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Header:     make(http.Header),
		}, nil
	})

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://test.example/data", nil)
	resp, err := safe.Do(req)
	if err != nil {
		t.Fatalf("Do returned error: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestNewSafeHTTPClientWithHTTPClient_PreservesConfiguredTimeout(t *testing.T) {
	cfg := DefaultConfig("test")
	cfg.Timeout = 20 * time.Millisecond
	cfg.MaxRetries = 0

	safe := NewSafeHTTPClientWithHTTPClient(cfg, &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			<-req.Context().Done()
			return nil, req.Context().Err()
		}),
	})
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://test.example/data", nil)

	started := time.Now()
	if _, err := safe.Do(req); err == nil {
		t.Fatal("Do returned nil error for a request that exceeded the configured timeout")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("configured timeout was not applied; request took %s", elapsed)
	}
}

func TestCircuitBreaker_HalfOpenRequestLimitIncludesFirstProbe(t *testing.T) {
	breaker := NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold:    1,
		OpenDuration:        time.Nanosecond,
		HalfOpenMaxRequests: 1,
	})
	breaker.RecordFailure()

	if !breaker.Allow() {
		t.Fatal("first half-open probe was rejected")
	}
	if breaker.Allow() {
		t.Fatal("second half-open probe exceeded HalfOpenMaxRequests")
	}
}

// TestSafeHTTPClient_429Retry verifies Retry-After handling on 429 responses.
func TestSafeHTTPClient_429Retry(t *testing.T) {
	safe := NewSafeHTTPClient(DefaultConfig("test"))
	safe.baseDelay = 50 * time.Millisecond
	safe.maxDelay = 200 * time.Millisecond

	attempts := 0
	safe.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Body:       io.NopCloser(strings.NewReader(`{}`)),
				Header:     http.Header{"Retry-After": []string{"1"}},
				Request:    req,
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Header:     make(http.Header),
		}, nil
	})

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://test.example/data", nil)
	resp, err := safe.Do(req)
	if err != nil {
		t.Fatalf("Do returned error: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}

// TestSafeHTTPClient_5xxRetry verifies retry on 5xx server errors.
func TestSafeHTTPClient_5xxRetry(t *testing.T) {
	safe := NewSafeHTTPClient(DefaultConfig("test"))
	safe.baseDelay = 10 * time.Millisecond
	safe.maxDelay = 50 * time.Millisecond

	attempts := 0
	safe.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		attempts++
		if attempts <= 2 {
			return &http.Response{
				StatusCode: http.StatusServiceUnavailable,
				Body:       io.NopCloser(strings.NewReader(`{}`)),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Header:     make(http.Header),
		}, nil
	})

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://test.example/data", nil)
	resp, err := safe.Do(req)
	if err != nil {
		t.Fatalf("Do returned error: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
}

// TestSafeHTTPClient_CircuitOpensAfterFailures verifies circuit breaker opens.
func TestSafeHTTPClient_CircuitOpensAfterFailures(t *testing.T) {
	safe := NewSafeHTTPClient(DefaultConfig("test"))
	safe.baseDelay = 1 * time.Millisecond
	safe.maxDelay = 5 * time.Millisecond
	// Use a long OpenDuration so it stays open throughout the test
	safe.breaker = NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold:    3,
		OpenDuration:        60 * time.Second,
		HalfOpenMaxRequests: 1,
	})

	safe.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})

	// Exhaust retries to accumulate failures
	for i := 0; i < 4; i++ {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://test.example/data", nil)
		_, _ = safe.Do(req)
	}

	// Circuit should now be open
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://test.example/data", nil)
	_, err := safe.Do(req)
	if err != ErrCircuitOpen {
		t.Fatalf("circuit state = %s; expected ErrCircuitOpen but got err=%v", safe.CircuitState(), err)
	}
	if safe.CircuitState() != "open" {
		t.Fatalf("circuit state = %s, want open", safe.CircuitState())
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
