package collector

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"sonde/pkg/provider"
)

func testHTTPConfig(name string) provider.Config {
	return provider.Config{
		ProviderName: name,
		Timeout:      2 * time.Second,
		RPS:          100,
		Burst:        100,
		MaxRetries:   0,
		BaseDelay:    time.Millisecond,
		MaxDelay:     time.Millisecond,
	}
}

func testClient(t *testing.T, name string, h http.HandlerFunc) (*provider.SafeHTTPClient, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	safe := provider.NewSafeHTTPClientWithHTTPClient(testHTTPConfig(name), srv.Client())
	return safe, srv
}
