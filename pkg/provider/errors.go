package provider

import (
	"errors"
	"net/url"
)

// ErrCircuitOpen is returned when the circuit breaker is in open state,
// meaning requests to the provider are temporarily blocked due to
// consecutive failures.
var ErrCircuitOpen = errors.New("provider circuit breaker is open")

// ErrResponseTooLarge is returned when an upstream payload exceeds the
// caller-provided response limit.
var ErrResponseTooLarge = errors.New("provider response exceeds size limit")

// SanitizeTransportError removes the credential-bearing URL that net/http
// attaches to transport failures while preserving the underlying error chain.
func SanitizeTransportError(err error) error {
	if err == nil {
		return nil
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		return urlErr.Err
	}
	return err
}
