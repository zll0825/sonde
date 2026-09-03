package collector

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"capital_observatory/pkg/model"
	"capital_observatory/pkg/provider"
	fredprov "capital_observatory/pkg/provider/fred"
	commodities "capital_observatory/plugins/commodities"
)

const testFREDBindings = `
provider: fred
latestLookbackDays: 45
series:
  - metric: oil.energy.wti
    series: DCOILWTICO
    frequency: daily
`

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

func liveFREDBindings(t *testing.T) []fredprov.Binding {
	t.Helper()
	raw, err := commodities.CatalogFS.ReadFile("bindings.yaml")
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
		ProviderName: "fred-commodities-test",
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
		Bindings: liveFREDBindings(t),
	})
	if err != nil {
		t.Fatalf("NewCollector: %v", err)
	}
	return c
}

func TestNewFREDCollector_RequiresAPIKey(t *testing.T) {
	t.Setenv("FRED_API_KEY", "")
	raw, err := commodities.CatalogFS.ReadFile("bindings.yaml")
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewFREDCollector(raw)
	if err == nil || !strings.Contains(err.Error(), "FRED_API_KEY") {
		t.Fatalf("error = %v", err)
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
	if len(snaps) != 2 {
		t.Fatalf("snapshots=%d, want 2 FRED legs (gold stays on Alpha Vantage)", len(snaps))
	}
	for _, snap := range snaps {
		if snap.MetricID == goldMetricID {
			t.Fatal("gold must not come from the FRED provider")
		}
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
