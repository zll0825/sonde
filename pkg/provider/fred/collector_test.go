package fred

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"capital_observatory/pkg/model"
	"capital_observatory/pkg/provider"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func testCollector(t *testing.T, bindings []Binding, rt http.RoundTripper) *Collector {
	t.Helper()
	client := &http.Client{Transport: rt}
	cfg := provider.Config{
		ProviderName: "fred-test",
		Timeout:      5 * time.Second,
		RPS:          100,
		Burst:        10,
		MaxRetries:   1,
		BaseDelay:    10 * time.Millisecond,
		MaxDelay:     50 * time.Millisecond,
	}
	c, err := NewCollector(Options{
		APIKey:   "test-secret",
		Client:   provider.NewSafeHTTPClientWithHTTPClient(cfg, client),
		Bindings: bindings,
	})
	if err != nil {
		t.Fatalf("NewCollector: %v", err)
	}
	return c
}

func TestParseValue(t *testing.T) {
	if _, ok := ParseValue(".", 1); ok {
		t.Fatal("missing '.' should be false")
	}
	if _, ok := ParseValue("", 1); ok {
		t.Fatal("empty should be false")
	}
	if _, ok := ParseValue("foo", 1); ok {
		t.Fatal("non-numeric should be false")
	}
	v, ok := ParseValue("4.21", 1)
	if !ok || v != 4.21 {
		t.Fatalf("identity scale = %v %v", v, ok)
	}
	v, ok = ParseValue("7200000", 1e6)
	if !ok || v != 7.2e12 {
		t.Fatalf("WALCL scale = %v %v", v, ok)
	}
}

func TestCollector_ClassifiesSnapshotsAsReal(t *testing.T) {
	bindings := []Binding{{MetricID: "us.mkt.ten_year_yield", SeriesID: "DGS10", UnitScale: 1, Frequency: "daily"}}
	c := testCollector(t, bindings, roundTripFunc(func(*http.Request) (*http.Response, error) {
		body := `{"observations":[{"date":"2026-08-01","value":"4.2"}]}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	}))
	snaps, err := c.GetSnapshots(context.Background())
	if err != nil {
		t.Fatalf("GetSnapshots: %v", err)
	}
	if len(snaps) != 1 {
		t.Fatalf("len=%d", len(snaps))
	}
	if snaps[0].SourceClass != model.SourceClassReal || snaps[0].Provider != ProviderName {
		t.Fatalf("snap=%+v", snaps[0])
	}
}

func TestCollector_PartialErrorOmitsSecret(t *testing.T) {
	bindings := []Binding{
		{MetricID: "fed.ins.balance_sheet", SeriesID: "WALCL", UnitScale: 1e6, Frequency: "weekly"},
		{MetricID: "us.mkt.ten_year_yield", SeriesID: "DGS10", UnitScale: 1, Frequency: "daily"},
	}
	c := testCollector(t, bindings, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Query().Get("series_id") == "DGS10" {
			return &http.Response{StatusCode: http.StatusBadGateway, Body: io.NopCloser(strings.NewReader("upstream unavailable")), Header: make(http.Header)}, nil
		}
		body := `{"observations":[{"date":"2026-08-01","value":"4.2"}]}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	}))
	snaps, err := c.GetSnapshots(context.Background())
	if err == nil || len(snaps) != 1 {
		t.Fatalf("partial = %d %v", len(snaps), err)
	}
	if !strings.Contains(err.Error(), "fred/us.mkt.ten_year_yield") || strings.Contains(err.Error(), "test-secret") {
		t.Fatalf("error = %q", err)
	}
}

func TestCollector_ExtraBindingNeedsNoGoTable(t *testing.T) {
	// Acceptance: a new FRED series is a binding, not a per-series Go table.
	bindings := []Binding{
		{MetricID: "us.mkt.ten_year_yield", SeriesID: "DGS10", UnitScale: 1, Frequency: "daily"},
		{MetricID: "us.mkt.extra_fixture", SeriesID: "TESTX", UnitScale: 1, Frequency: "daily"},
	}
	c := testCollector(t, bindings, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		id := req.URL.Query().Get("series_id")
		val := "4.2"
		if id == "TESTX" {
			val = "9.9"
		}
		body := `{"observations":[{"date":"2026-08-01","value":"` + val + `"}]}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	}))
	snaps, err := c.GetSnapshots(context.Background())
	if err != nil {
		t.Fatalf("GetSnapshots: %v", err)
	}
	found := false
	for _, s := range snaps {
		if s.MetricID == "us.mkt.extra_fixture" && s.Value == 9.9 {
			found = true
		}
	}
	if !found {
		t.Fatalf("extra binding missing from snapshots: %+v", snaps)
	}
}

func TestLoadBindings_DefaultScale(t *testing.T) {
	yaml := []byte(`
provider: fred
series:
  - metric: oil.energy.wti
    series: DCOILWTICO
    frequency: daily
`)
	got, err := LoadBindings(yaml)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].UnitScale != 1 || got[0].SeriesID != "DCOILWTICO" {
		t.Fatalf("%+v", got)
	}
}
