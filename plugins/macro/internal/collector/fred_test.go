package collector

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"sonde/pkg/model"
	"sonde/pkg/provider"
	fredprov "sonde/pkg/provider/fred"
	macro "sonde/plugins/macro"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type stubQuota struct {
	err      error
	reserves int
}

func (s *stubQuota) Reserve(context.Context, string) error {
	s.reserves++
	return s.err
}

func (s *stubQuota) RecordFailure(context.Context, string) error { return nil }

func liveBindings(t *testing.T) []fredprov.Binding {
	t.Helper()
	raw, err := macro.CatalogFS.ReadFile("bindings.yaml")
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := fredprov.LoadBindings(raw)
	if err != nil {
		t.Fatal(err)
	}
	return bindings
}

func sharedCollector(t *testing.T, rt http.RoundTripper, quota provider.ProviderLimiter) *fredprov.Collector {
	t.Helper()
	cfg := provider.Config{
		ProviderName: "fred-macro-test",
		Timeout:      5 * time.Second,
		RPS:          100,
		Burst:        10,
		MaxRetries:   0,
		BaseDelay:    time.Millisecond,
		MaxDelay:     time.Millisecond,
	}
	safe := provider.NewSafeHTTPClientWithHTTPClient(cfg, &http.Client{Transport: rt})
	if quota != nil {
		safe.SetProviderLimiter(quota)
	}
	c, err := fredprov.NewCollector(fredprov.Options{
		APIKey:   "test-secret",
		Client:   safe,
		Bindings: liveBindings(t),
	})
	if err != nil {
		t.Fatalf("NewCollector: %v", err)
	}
	return c
}

func TestNewFREDCollector_RequiresAPIKey(t *testing.T) {
	t.Setenv("FRED_API_KEY", "")
	raw, err := macro.CatalogFS.ReadFile("bindings.yaml")
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewFREDCollector(raw)
	if err == nil || !strings.Contains(err.Error(), "FRED_API_KEY") {
		t.Fatalf("error = %v", err)
	}
}

func TestNewFREDCollector_RequiresBindings(t *testing.T) {
	t.Setenv("FRED_API_KEY", "k")
	_, err := NewFREDCollector([]byte(`provider: fred
series: []
`))
	if err == nil {
		t.Fatal("empty series should fail")
	}
}

func TestSharedProvider_LiveYAMLSnapshotsAreReal(t *testing.T) {
	c := sharedCollector(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		body := `{"observations":[{"date":"2026-08-01","value":"4.2"}]}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	}), nil)
	snaps, err := c.GetSnapshots(context.Background())
	if err != nil {
		t.Fatalf("GetSnapshots: %v", err)
	}
	if len(snaps) != len(liveBindings(t)) {
		t.Fatalf("snapshots=%d bindings=%d", len(snaps), len(liveBindings(t)))
	}
	for _, snap := range snaps {
		if snap.SourceClass != model.SourceClassReal || snap.Provider != fredprov.ProviderName {
			t.Fatalf("snap=%+v", snap)
		}
	}
}

func TestSharedProvider_QuotaFailClosed(t *testing.T) {
	wireCalls := 0
	quota := &stubQuota{err: provider.ErrProviderQuotaExceeded}
	c := sharedCollector(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		wireCalls++
		return nil, fmt.Errorf("must not be called")
	}), quota)
	snaps, err := c.GetSnapshots(context.Background())
	if err == nil || !strings.Contains(err.Error(), provider.ErrProviderQuotaExceeded.Error()) {
		t.Fatalf("GetSnapshots = %v %v", snaps, err)
	}
	if wireCalls != 0 || quota.reserves == 0 {
		t.Fatalf("wire=%d reserves=%d", wireCalls, quota.reserves)
	}
}
