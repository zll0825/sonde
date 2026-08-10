package provider

import "errors"

// ErrCircuitOpen is returned when the circuit breaker is in open state,
// meaning requests to the provider are temporarily blocked due to
// consecutive failures.
var ErrCircuitOpen = errors.New("provider circuit breaker is open")
