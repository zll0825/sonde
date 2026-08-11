package provider

import "sync"

var (
	fredShared     *SafeHTTPClient
	fredSharedOnce sync.Once
)

// SharedFREDClient returns a singleton SafeHTTPClient keyed to FRED.
// Both macro and commodities plugins share the same token bucket + circuit
// breaker so their combined request rate stays under the FRED free-tier limit.
func SharedFREDClient() *SafeHTTPClient {
	fredSharedOnce.Do(func() {
		fredShared = NewSafeHTTPClient(FREDConfig())
	})
	return fredShared
}
