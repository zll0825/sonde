package provider

import (
	"sync"
	"time"
)

// CircuitState is the state of the circuit breaker.
type CircuitState int

const (
	// StateClosed allows requests. Normal operating state.
	StateClosed CircuitState = iota
	// StateOpen blocks requests due to consecutive failures.
	StateOpen
	// StateHalfOpen allows a limited probe request to test recovery.
	StateHalfOpen
)

func (s CircuitState) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// CircuitBreaker implements the circuit breaker pattern for outbound requests.
// After FailureThreshold consecutive failures, the circuit opens and blocks
// requests for OpenDuration. Then it enters half-open, allowing a limited
// number of probe requests. On success, it closes again.
type CircuitBreaker struct {
	config         CircuitBreakerConfig
	state          CircuitState
	failures       int
	lastFailure    time.Time
	halfOpenCount  int
	mu             sync.Mutex
}

// NewCircuitBreaker creates a circuit breaker with the given configuration.
func NewCircuitBreaker(cfg CircuitBreakerConfig) *CircuitBreaker {
	if cfg.FailureThreshold < 1 {
		cfg.FailureThreshold = 3
	}
	if cfg.OpenDuration == 0 {
		cfg.OpenDuration = 60 * time.Second
	}
	if cfg.HalfOpenMaxRequests < 1 {
		cfg.HalfOpenMaxRequests = 1
	}
	return &CircuitBreaker{
		config: cfg,
		state:  StateClosed,
	}
}

// Allow checks if a request is allowed to proceed.
// Returns true if the request should be sent, false if it should be blocked.
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case StateClosed:
		return true

	case StateOpen:
		// Check if the open duration has elapsed; try half-open
		if time.Since(cb.lastFailure) >= cb.config.OpenDuration {
			cb.state = StateHalfOpen
			cb.halfOpenCount = 0
			return true
		}
		return false

	case StateHalfOpen:
		// Allow limited probe requests
		if cb.halfOpenCount < cb.config.HalfOpenMaxRequests {
			cb.halfOpenCount++
			return true
		}
		return false

	default:
		return false
	}
}

// RecordSuccess signals that a request succeeded.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case StateHalfOpen:
		// Success in half-open: close the circuit
		cb.state = StateClosed
		cb.failures = 0
		cb.halfOpenCount = 0

	case StateClosed:
		// Reset failure count on success
		cb.failures = 0
	}
}

// RecordFailure signals that a request failed.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.lastFailure = time.Now()

	switch cb.state {
	case StateHalfOpen:
		// Failure in half-open: reopen immediately
		cb.state = StateOpen
		cb.halfOpenCount = 0

	case StateClosed:
		cb.failures++
		if cb.failures >= cb.config.FailureThreshold {
			cb.state = StateOpen
		}
	}
}

// State returns the current state as a string for health reporting.
func (cb *CircuitBreaker) State() string {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state.String()
}

// FailureCount returns the current consecutive failure count.
func (cb *CircuitBreaker) FailureCount() int {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.failures
}
