package collector

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

func TestFREDCommoditiesCollector_ClassifiesUpstreamSnapshotsAsReal(t *testing.T) {
	testClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		body := `{"observations":[{"date":"2026-08-01","value":"75.5"}]}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	testCfg := provider.Config{
		ProviderName: "fred-commodities-test",
		Timeout:      5 * time.Second,
		RPS:          100, // high RPS for testing
		Burst:        10,
		MaxRetries:   1,
		BaseDelay:    10 * time.Millisecond,
		MaxDelay:     50 * time.Millisecond,
	}
	f := &FREDCollector{
		apiKey: "test",
		client: provider.NewSafeHTTPClientWithHTTPClient(testCfg, testClient),
	}
	snaps, err := f.GetSnapshots(context.Background())
	if err != nil {
		t.Fatalf("GetSnapshots: %v", err)
	}
	for _, snap := range snaps {
		if snap.SourceClass != model.SourceClassReal {
			t.Errorf("snapshot %q class = %q, want real", snap.MetricID, snap.SourceClass)
		}
	}
}

func TestParseFREDValue_Missing(t *testing.T) {
	// FRED marks missing observations with ".".
	if v, ok := parseFREDValue(".", 1); ok {
		t.Errorf("parseFREDValue(\".\") should return ok=false, got (%v, %v)", v, ok)
	}
	if v, ok := parseFREDValue("", 1); ok {
		t.Errorf("parseFREDValue(\"\") should return ok=false, got (%v, %v)", v, ok)
	}
}

func TestParseFREDValue_NonNumeric(t *testing.T) {
	if v, ok := parseFREDValue("foo", 1); ok {
		t.Errorf("parseFREDValue(\"foo\") should return ok=false, got (%v, %v)", v, ok)
	}
}

func TestParseFREDValue_WTI(t *testing.T) {
	// WTI oil price — identity scale (USD/bbl).
	v, ok := parseFREDValue("75.50", 1)
	if !ok {
		t.Fatal("parseFREDValue(\"75.50\", 1) should return ok=true")
	}
	if v != 75.50 {
		t.Errorf("parseFREDValue(\"75.50\", 1) = %v, want 75.50", v)
	}
}

func TestParseFREDValue_CopperCents(t *testing.T) {
	// Copper in cents/lb — identity scale.
	v, ok := parseFREDValue("420.5", 1)
	if !ok {
		t.Fatal("parseFREDValue(\"420.5\", 1) should return ok=true")
	}
	if v != 420.5 {
		t.Errorf("parseFREDValue(\"420.5\", 1) = %v, want 420.5", v)
	}
}

func TestParseFREDValue_GoldDollars(t *testing.T) {
	// Gold in USD/oz — identity scale.
	v, ok := parseFREDValue("2350.10", 1)
	if !ok {
		t.Fatal("parseFREDValue(\"2350.10\", 1) should return ok=true")
	}
	if v != 2350.10 {
		t.Errorf("parseFREDValue(\"2350.10\", 1) = %v, want 2350.10", v)
	}
}

func TestCommoditiesSeriesListOrder(t *testing.T) {
	// The series list should cover all three commodity dimensions: oil, copper, gold.
	want := []string{
		"oil.energy.wti",
		"metal.industrial.copper",
		"metal.precious.gold",
	}
	if len(fredSeriesList) != len(want) {
		t.Fatalf("fredSeriesList has %d entries, want %d", len(fredSeriesList), len(want))
	}
	for i, entry := range fredSeriesList {
		if entry.MetricID != want[i] {
			t.Errorf("fredSeriesList[%d].MetricID = %q, want %q", i, entry.MetricID, want[i])
		}
	}
}

func TestCommoditiesSeriesFrequencyConsistency(t *testing.T) {
	// All three commodity series should be daily frequency.
	for _, entry := range fredSeriesList {
		if entry.Frequency != "daily" {
			t.Errorf("series %s frequency = %q, want daily", entry.MetricID, entry.Frequency)
		}
	}
}

func TestNewFREDCollector_RequiresAPIKey(t *testing.T) {
	t.Setenv("FRED_API_KEY", "")
	_, err := NewFREDCollector()
	if err == nil {
		t.Fatal("NewFREDCollector should fail without FRED_API_KEY")
	}
}

func TestMock_GetSnapshots_CoversAllDimensions(t *testing.T) {
	snaps, err := Mock{}.GetSnapshots(context.Background())
	if err != nil {
		t.Fatalf("Mock GetSnapshots: %v", err)
	}
	got := make(map[string]bool, len(snaps))
	for _, snap := range snaps {
		got[snap.MetricID] = true
	}
	want := []string{"oil.energy.wti", "metal.industrial.copper", "metal.precious.gold"}
	for _, metric := range want {
		if !got[metric] {
			t.Errorf("mock missing metric %q", metric)
		}
	}
}

func TestMock_GetSnapshotsForWindow_SeriesShape(t *testing.T) {
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC)
	snaps, err := Mock{}.GetSnapshotsForWindow(context.Background(), start, end)
	if err != nil {
		t.Fatalf("GetSnapshotsForWindow: %v", err)
	}
	// Expect ~31 days × 3 metrics = ~93 snapshots
	if len(snaps) < 60 || len(snaps) > 100 {
		t.Errorf("GetSnapshotsForWindow got %d snapshots, want ~93 (31 days × 3 metrics)", len(snaps))
	}
}
