package fred

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"sonde/pkg/model"
	"sonde/pkg/provider"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type stubQuota struct {
	err      error
	reserves int
}

func (s *stubQuota) Reserve(context.Context, string) error {
	s.reserves++
	return s.err
}

func (s *stubQuota) RecordFailure(context.Context, string) error { return nil }

func testCollector(t *testing.T, bindings []Binding, rt http.RoundTripper) *Collector {
	t.Helper()
	return testCollectorWithQuota(t, bindings, rt, nil)
}

func testCollectorWithQuota(t *testing.T, bindings []Binding, rt http.RoundTripper, quota provider.ProviderLimiter) *Collector {
	t.Helper()
	client := &http.Client{Transport: rt}
	cfg := provider.Config{
		ProviderName: "fred-test",
		Timeout:      5 * time.Second,
		RPS:          100,
		Burst:        10,
		MaxRetries:   0,
		BaseDelay:    10 * time.Millisecond,
		MaxDelay:     50 * time.Millisecond,
	}
	safe := provider.NewSafeHTTPClientWithHTTPClient(cfg, client)
	if quota != nil {
		safe.SetProviderLimiter(quota)
	}
	c, err := NewCollector(Options{
		APIKey:   "test-secret",
		Client:   safe,
		Bindings: bindings,
	})
	if err != nil {
		t.Fatalf("NewCollector: %v", err)
	}
	return c
}

func okFREDBody(value string) *http.Response {
	body := `{"observations":[{"date":"2026-08-01","value":"` + value + `"}]}`
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
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
		return okFREDBody("4.2"), nil
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
		return okFREDBody("4.2"), nil
	}))
	snaps, err := c.GetSnapshots(context.Background())
	if err == nil || len(snaps) != 1 {
		t.Fatalf("partial = %d %v", len(snaps), err)
	}
	if !strings.Contains(err.Error(), "fred/us.mkt.ten_year_yield") || strings.Contains(err.Error(), "test-secret") {
		t.Fatalf("error = %q", err)
	}
}

func TestCollector_BackfillPartialErrorOmitsSecret(t *testing.T) {
	bindings := []Binding{
		{MetricID: "fed.ins.balance_sheet", SeriesID: "WALCL", UnitScale: 1e6, Frequency: "weekly"},
		{MetricID: "us.mkt.ten_year_yield", SeriesID: "DGS10", UnitScale: 1, Frequency: "daily"},
	}
	c := testCollector(t, bindings, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Query().Get("series_id") == "DGS10" {
			return &http.Response{StatusCode: http.StatusBadGateway, Body: io.NopCloser(strings.NewReader("upstream unavailable")), Header: make(http.Header)}, nil
		}
		return okFREDBody("4.2"), nil
	}))
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	snaps, err := c.GetSnapshotsForWindow(context.Background(), start, start.Add(24*time.Hour))
	if err == nil || len(snaps) != 1 {
		t.Fatalf("partial backfill = %d %v", len(snaps), err)
	}
	if !strings.Contains(err.Error(), "fred/us.mkt.ten_year_yield") || strings.Contains(err.Error(), "test-secret") {
		t.Fatalf("error = %q", err)
	}
}

func TestCollector_QuotaFailClosed(t *testing.T) {
	wireCalls := 0
	quota := &stubQuota{err: provider.ErrProviderQuotaExceeded}
	c := testCollectorWithQuota(t, []Binding{
		{MetricID: "us.mkt.ten_year_yield", SeriesID: "DGS10", UnitScale: 1, Frequency: "daily"},
	}, roundTripFunc(func(*http.Request) (*http.Response, error) {
		wireCalls++
		return nil, fmt.Errorf("must not be called")
	}), quota)

	snaps, err := c.GetSnapshots(context.Background())
	if err == nil || !strings.Contains(err.Error(), provider.ErrProviderQuotaExceeded.Error()) {
		t.Fatalf("GetSnapshots = %v %v, want quota exceeded", snaps, err)
	}
	if len(snaps) != 0 || wireCalls != 0 || quota.reserves == 0 {
		t.Fatalf("snaps=%d wire=%d reserves=%d", len(snaps), wireCalls, quota.reserves)
	}
}

func TestCollector_QuotaUnavailableFailClosed(t *testing.T) {
	wireCalls := 0
	quota := &stubQuota{err: provider.ErrProviderQuotaUnavailable}
	c := testCollectorWithQuota(t, []Binding{
		{MetricID: "oil.energy.wti", SeriesID: "DCOILWTICO", UnitScale: 1, Frequency: "daily"},
	}, roundTripFunc(func(*http.Request) (*http.Response, error) {
		wireCalls++
		return nil, fmt.Errorf("must not be called")
	}), quota)

	snaps, err := c.GetSnapshotsForWindow(context.Background(), time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC))
	if err == nil || !strings.Contains(err.Error(), provider.ErrProviderQuotaUnavailable.Error()) {
		t.Fatalf("GetSnapshotsForWindow = %v %v, want quota unavailable", snaps, err)
	}
	if wireCalls != 0 {
		t.Fatalf("wireCalls=%d, want 0", wireCalls)
	}
}

func TestCollector_ExtraBindingNeedsNoGoTable(t *testing.T) {
	// Acceptance: a new FRED series is a binding, not a per-series Go table.
	bindings := []Binding{
		{MetricID: "us.mkt.ten_year_yield", SeriesID: "DGS10", UnitScale: 1, Frequency: "daily"},
		{MetricID: "us.mkt.extra_fixture", SeriesID: "TESTX", UnitScale: 1, Frequency: "daily"},
	}
	c := testCollector(t, bindings, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		val := "4.2"
		if req.URL.Query().Get("series_id") == "TESTX" {
			val = "9.9"
		}
		return okFREDBody(val), nil
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

func TestCollector_InflationUnitsPC1(t *testing.T) {
	gotUnits := map[string]bool{}
	c := testCollector(t, []Binding{
		{MetricID: "us.mkt.cpi", SeriesID: "CPIAUCSL", UnitScale: 1, Frequency: "monthly"},
		{MetricID: "us.mkt.inflation_yoy", SeriesID: "CPIAUCSL", UnitScale: 1, Frequency: "monthly", Units: "pc1"},
	}, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Query().Get("series_id") == "CPIAUCSL" {
			gotUnits[req.URL.Query().Get("units")] = true
		}
		return okFREDBody("3.2"), nil
	}))
	if _, err := c.GetSnapshots(context.Background()); err != nil {
		t.Fatalf("GetSnapshots: %v", err)
	}
	if !gotUnits["pc1"] {
		t.Fatal("inflation_yoy must request units=pc1")
	}
	if !gotUnits[""] {
		t.Fatal("CPI level must request native units")
	}
}

func TestCollector_WindowPaging(t *testing.T) {
	requestCount := 0
	var offsets []int
	c := testCollector(t, []Binding{
		{MetricID: "us.mkt.ten_year_yield", SeriesID: "DGS10", UnitScale: 1, Frequency: "daily"},
	}, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requestCount++
		off, _ := strconv.Atoi(req.URL.Query().Get("offset"))
		offsets = append(offsets, off)
		type obs struct {
			Date  string `json:"date"`
			Value string `json:"value"`
		}
		obsList := make([]obs, 0)
		if off == 0 {
			obsList = make([]obs, maxFREDPerPage)
			for i := range obsList {
				obsList[i] = obs{Date: fmt.Sprintf("2020-01-%02d", (i%28)+1), Value: "4.2"}
			}
		}
		bodyBytes, _ := json.Marshal(map[string]any{"observations": obsList})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(bodyBytes)), Header: make(http.Header)}, nil
	}))

	end := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	snaps, err := c.GetSnapshotsForWindow(context.Background(), end.AddDate(-1, 0, 0), end)
	if err != nil {
		t.Fatalf("GetSnapshotsForWindow: %v", err)
	}
	if requestCount < 2 {
		t.Fatalf("expected paged requests, got %d", requestCount)
	}
	hasOffset := false
	for _, off := range offsets {
		if off > 0 {
			hasOffset = true
			break
		}
	}
	if !hasOffset {
		t.Errorf("expected offset > 0, got %v", offsets)
	}
	if len(snaps) == 0 {
		t.Fatal("expected snapshots from paged fetch")
	}
}

func TestCollector_ClampsHistoricalWindow(t *testing.T) {
	var startParam string
	c := testCollector(t, []Binding{
		{MetricID: "us.mkt.ten_year_yield", SeriesID: "DGS10", UnitScale: 1, Frequency: "daily"},
	}, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		startParam = req.URL.Query().Get("observation_start")
		return okFREDBody("4.2"), nil
	}))
	end := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := c.GetSnapshotsForWindow(context.Background(), end.Add(-MaxHistoricalWindow-365*24*time.Hour), end); err != nil {
		t.Fatalf("GetSnapshotsForWindow: %v", err)
	}
	want := end.Add(-MaxHistoricalWindow).Format("2006-01-02")
	if startParam != want {
		t.Fatalf("observation_start=%q, want clamped %q", startParam, want)
	}
}

func TestMaxHistoricalWindow(t *testing.T) {
	if MaxHistoricalWindow != 3650*24*time.Hour {
		t.Fatalf("MaxHistoricalWindow = %v", MaxHistoricalWindow)
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
