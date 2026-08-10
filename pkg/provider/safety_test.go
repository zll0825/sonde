package provider

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

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
