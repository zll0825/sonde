package provider

import (
	"context"
	"sync"
	"time"
)

// TokenBucket implements a simple token bucket rate limiter.
// It allows up to Burst requests instantly, then throttles to RPS tokens/sec.
type TokenBucket struct {
	rate       float64   // tokens per second
	burst      int       // max tokens
	tokens     float64   // current available tokens
	lastRefill time.Time // last time tokens were added
	mu         sync.Mutex
	closed     bool
}

// NewTokenBucket creates a token bucket with the given rate and burst capacity.
func NewTokenBucket(rps float64, burst int) *TokenBucket {
	if burst < 1 {
		burst = 1
	}
	if rps <= 0 {
		rps = 0.1 // safety floor
	}
	return &TokenBucket{
		rate:       rps,
		burst:      burst,
		tokens:     float64(burst),
		lastRefill: time.Now(),
	}
}

// Wait blocks until a token is available or the context is cancelled.
func (tb *TokenBucket) Wait(ctx context.Context) error {
	for {
		tb.mu.Lock()
		tb.refill()

		if tb.closed {
			tb.mu.Unlock()
			return context.Canceled
		}

		if tb.tokens >= 1 {
			tb.tokens--
			tb.mu.Unlock()
			return nil
		}

		// Calculate exact wait time needed for one full token
		tokensNeeded := 1 - tb.tokens
		waitTime := time.Duration(float64(time.Second) * tokensNeeded / tb.rate)

		tb.mu.Unlock()

		// Wait for either the time needed, or context cancellation
		// Cap wait at 1 second intervals for responsiveness, but refill will accumulate tokens correctly
		timer := time.NewTimer(minDuration(waitTime, time.Second))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
			// loop back, refill will add tokens based on actual elapsed time
		}
	}
}

// TryAcquire attempts to take a token without blocking. Returns true if acquired.
func (tb *TokenBucket) TryAcquire() bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	tb.refill()
	if tb.tokens < 1 {
		return false
	}
	tb.tokens--
	return true
}

// refill adds tokens based on elapsed time. Must be called with lock held.
func (tb *TokenBucket) refill() {
	now := time.Now()
	elapsed := now.Sub(tb.lastRefill).Seconds()
	tb.tokens += elapsed * tb.rate
	if tb.tokens > float64(tb.burst) {
		tb.tokens = float64(tb.burst)
	}
	tb.lastRefill = now
}

// Close signals the limiter to stop and wakes any waiters.
func (tb *TokenBucket) Close() {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	tb.closed = true
}

// Rate returns the current configured rate.
func (tb *TokenBucket) Rate() float64 {
	return tb.rate
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
