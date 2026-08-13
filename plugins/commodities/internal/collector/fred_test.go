package collector

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

func TestFREDCommoditiesCollector_ReturnsPartialSnapshotsAndError(t *testing.T) {
	testClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Query().Get("series_id") == "GOLDAMGBD228NLBM" {
			return &http.Response{StatusCode: http.StatusBadGateway, Body: io.NopCloser(strings.NewReader("upstream unavailable")), Header: make(http.Header)}, nil
		}
		body := `{"observations":[{"date":"2026-08-01","value":"75.5"}]}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	f := &FREDCollector{apiKey: "test-secret", client: provider.NewSafeHTTPClientWithHTTPClient(provider.Config{
		ProviderName: "fred-commodities-partial", Timeout: time.Second, RPS: 100, Burst: 10,
	}, testClient)}

	snaps, err := f.GetSnapshots(context.Background())
	if err == nil || len(snaps) != 2 {
		t.Fatalf("partial result = %d snapshots, error %v; want 2 and non-nil", len(snaps), err)
	}
	if !strings.Contains(err.Error(), "fred/metal.precious.gold") || strings.Contains(err.Error(), "test-secret") {
		t.Fatalf("partial error = %q", err)
	}
}

func TestFREDCommoditiesCollector_BackfillReturnsPartialSnapshotsAndError(t *testing.T) {
	testClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Query().Get("series_id") == "GOLDAMGBD228NLBM" {
			return &http.Response{StatusCode: http.StatusBadGateway, Body: io.NopCloser(strings.NewReader("upstream unavailable")), Header: make(http.Header)}, nil
		}
		body := `{"observations":[{"date":"2026-08-01","value":"75.5"}]}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	f := &FREDCollector{apiKey: "test-secret", client: provider.NewSafeHTTPClientWithHTTPClient(provider.Config{
		ProviderName: "fred-commodities-backfill-partial", Timeout: time.Second, RPS: 100, Burst: 10,
	}, testClient)}

	snaps, err := f.GetSnapshotsForWindow(context.Background(), time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC))
	if err == nil || len(snaps) != 2 {
		t.Fatalf("partial backfill = %d snapshots, error %v; want 2 and non-nil", len(snaps), err)
	}
	if !strings.Contains(err.Error(), "fred/metal.precious.gold") || strings.Contains(err.Error(), "test-secret") {
		t.Fatalf("partial backfill error = %q", err)
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
	// Oil and gold are daily; copper (PCOPPUSDM) is a monthly IMF series.
	want := map[string]string{
		"oil.energy.wti":          "daily",
		"metal.industrial.copper": "monthly",
		"metal.precious.gold":     "daily",
	}
	for _, entry := range fredSeriesList {
		expected, ok := want[entry.MetricID]
		if !ok {
			t.Errorf("unexpected metric %q in fredSeriesList", entry.MetricID)
			continue
		}
		if entry.Frequency != expected {
			t.Errorf("series %s frequency = %q, want %q", entry.MetricID, entry.Frequency, expected)
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

// TestCommoditiesFREDSeriesWindowPaging verifies that the commodities FRED collector
// paginates results and uses offset parameters to retrieve multi-page data.
func TestCommoditiesFREDSeriesWindowPaging(t *testing.T) {
	requestCount := 0
	var offsets []int

	testClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requestCount++
		q := req.URL.Query()
		offsetStr := q.Get("offset")
		off, _ := strconv.Atoi(offsetStr)
		offsets = append(offsets, off)

		var observations []map[string]string
		if off == 0 {
			// Simulate a full page for the first request
			observations = make([]map[string]string, maxFREDPerPage)
			for i := 0; i < maxFREDPerPage; i++ {
				observations[i] = map[string]string{
					"date":  fmt.Sprintf("2020-01-%02d", (i%28)+1),
					"value": "75.5",
				}
			}
		} else {
			observations = []map[string]string{}
		}

		type obs struct {
			Date  string `json:"date"`
			Value string `json:"value"`
		}
		obsList := make([]obs, 0, len(observations))
		for _, o := range observations {
			obsList = append(obsList, obs{Date: o["date"], Value: o["value"]})
		}
		bodyBytes, _ := json.Marshal(map[string]interface{}{"observations": obsList})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(bodyBytes)), Header: make(http.Header)}, nil
	})}

	testCfg := provider.Config{
		ProviderName: "fred-commodities-paging",
		Timeout:      5 * time.Second,
		RPS:          100,
		Burst:        10,
		MaxRetries:   0,
		BaseDelay:    time.Millisecond,
		MaxDelay:     time.Millisecond,
	}
	f := &FREDCollector{
		apiKey: "test",
		client: provider.NewSafeHTTPClientWithHTTPClient(testCfg, testClient),
	}

	end := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	start := end.AddDate(-1, 0, 0) // 1 year — exceeds old 90-day cap

	snaps, err := f.GetSnapshotsForWindow(context.Background(), start, end)
	if err != nil {
		t.Fatalf("GetSnapshotsForWindow: %v", err)
	}

	if requestCount < 2 {
		t.Fatalf("expected at least 2 HTTP requests (paged), got %d", requestCount)
	}

	hasOffset := false
	for _, off := range offsets {
		if off > 0 {
			hasOffset = true
			break
		}
	}
	if !hasOffset {
		t.Errorf("expected offset > 0 in paged requests, got offsets: %v", offsets)
	}

	if len(snaps) == 0 {
		t.Fatal("expected some snapshots from paged FRED commodity fetch")
	}
}

// TestMaxHistoricalWindow verifies the exported constant is 10 years.
func TestMaxHistoricalWindow(t *testing.T) {
	expected := 3650 * 24 * time.Hour
	if MaxHistoricalWindow != expected {
		t.Errorf("MaxHistoricalWindow = %v, want %v", MaxHistoricalWindow, expected)
	}
}
