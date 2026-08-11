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

func TestFREDCollector_ClassifiesUpstreamSnapshotsAsReal(t *testing.T) {
	testClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		body := `{"observations":[{"date":"2026-08-01","value":"4.2"}]}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	// Use test-friendly config with high RPS to avoid rate limit delays in tests
	testCfg := provider.Config{
		ProviderName: "fred-test",
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

func TestParseFREDValue_ScalingIdentity(t *testing.T) {
	// DGS10 yield — identity scale (percent).
	v, ok := parseFREDValue("4.21", 1)
	if !ok {
		t.Fatal("parseFREDValue(\"4.21\", 1) should return ok=true")
	}
	if v != 4.21 {
		t.Errorf("parseFREDValue(\"4.21\", 1) = %v, want 4.21", v)
	}
}

func TestParseFREDValue_WALCLScaling(t *testing.T) {
	// WALCL is reported in millions; scale by 1e6 to get USD.
	v, ok := parseFREDValue("7200000", 1e6)
	if !ok {
		t.Fatal("parseFREDValue(\"7200000\", 1e6) should return ok=true")
	}
	const want = 7.2e12 // 7.2 trillion USD (7200000 millions)
	if v != want {
		t.Errorf("parseFREDValue(\"7200000\", 1e6) = %v, want %v (7.2T USD)", v, want)
	}
}

func TestParseFREDValue_LargeInteger(t *testing.T) {
	// Percentile with a zero-trail.
	v, ok := parseFREDValue("103.45", 1)
	if !ok {
		t.Fatal("parseFREDValue(\"103.45\", 1) should return ok=true")
	}
	if v != 103.45 {
		t.Errorf("parseFREDValue(\"103.45\", 1) = %v, want 103.45", v)
	}
}

func TestFREDSeriesListOrder(t *testing.T) {
	// The series list should match the order the registration declares metrics in.
	// Includes liquidity, rates, dollar/FX, and inflation dimensions.
	want := []string{
		"fed.ins.balance_sheet",
		"us.mkt.ten_year_yield",
		"us.mkt.dollar_index",
		"us.mkt.usd_cny",
		"us.mkt.cpi",
		"us.mkt.inflation_yoy",
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

func TestMacroCollectorFetchURL_UnitsCPI_PCH(t *testing.T) {
	// Verify that CPIAUCSL_PCH requests include &units=pc1.
	testClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		q := req.URL.Query()
		if q.Get("series_id") == "CPIAUCSL_PCH" {
			if q.Get("units") != "pc1" {
				t.Errorf("CPIAUCSL_PCH units param = %q, want %q", q.Get("units"), "pc1")
			}
		}
		body := `{"observations":[{"date":"2026-08-01","value":"3.2"}]}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	cfg := provider.Config{
		ProviderName: "fred-test-units",
		Timeout:      5 * time.Second,
		RPS:          100,
		Burst:        10,
		MaxRetries:   1,
		BaseDelay:    10 * time.Millisecond,
		MaxDelay:     50 * time.Millisecond,
	}
	f := &FREDCollector{
		apiKey: "test",
		client: provider.NewSafeHTTPClientWithHTTPClient(cfg, testClient),
	}
	_, err := f.GetSnapshots(context.Background())
	if err != nil {
		t.Fatalf("GetSnapshots: %v", err)
	}
}

func TestWALCLUnitsAreUSD(t *testing.T) {
	// Sanity: WALCL scale must be 1e6 (millions -> USD).
	for _, entry := range fredSeriesList {
		if entry.MetricID == "fed.ins.balance_sheet" && entry.UnitScale != 1e6 {
			t.Errorf("WALCL UnitScale = %v, want 1e6 (FRED reports in MILLIONS of USD)", entry.UnitScale)
		}
	}
}

// TestFetchSeriesWindowPaging verifies that GetSnapshotsForWindow requests
// paginated observations when the series data spans more than one FRED page.
// It checks that offset parameters appear in subsequent requests.
func TestFetchSeriesWindowPaging(t *testing.T) {
	requestCount := 0
	var offsets []int

	testClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requestCount++
		q := req.URL.Query()
		offsetStr := q.Get("offset")
		off, _ := strconv.Atoi(offsetStr)
		offsets = append(offsets, off)

		// First page returns maxFREDPerPage observations; second page returns 0 (end)
		var observations []map[string]string
		if off == 0 {
			// Simulate a full page
			observations = make([]map[string]string, maxFREDPerPage)
			for i := 0; i < maxFREDPerPage; i++ {
				observations[i] = map[string]string{
					"date":  fmt.Sprintf("2020-01-%02d", (i%28)+1),
					"value": "4.2",
				}
			}
		} else {
			// Empty second page signals end
			observations = []map[string]string{}
		}

		// Marshal observations inline
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

	cfg := provider.Config{
		ProviderName: "fred-test-paging",
		Timeout:      5 * time.Second,
		RPS:          100,
		Burst:        10,
		MaxRetries:   0,
		BaseDelay:    time.Millisecond,
		MaxDelay:     time.Millisecond,
	}
	f := &FREDCollector{
		apiKey: "test",
		client: provider.NewSafeHTTPClientWithHTTPClient(cfg, testClient),
	}

	// Request a window > 90 days to trigger the paged path (but < MaxHistoricalWindow)
	end := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	start := end.AddDate(-1, 0, 0) // 1 year

	snaps, err := f.GetSnapshotsForWindow(context.Background(), start, end)
	if err != nil {
		t.Fatalf("GetSnapshotsForWindow: %v", err)
	}

	// Multiple series are queried; we should see at least 2 requests for DGS10 (daily series)
	// since it returns maxFREDPerPage + empty page.
	if requestCount < 2 {
		t.Fatalf("expected at least 2 HTTP requests (paged), got %d", requestCount)
	}

	// Verify offset parameter was used
	hasOffset := false
	for _, off := range offsets {
		if off > 0 {
			hasOffset = true
			break
		}
	}
	if !hasOffset {
		t.Errorf("expected some requests with offset > 0 for paged results, got offsets: %v", offsets)
	}

	if len(snaps) == 0 {
		t.Fatal("expected at least some snapshots from paged fetch")
	}
}

// TestMaxHistoricalWindow verifies the exported constant is 10 years.
func TestMaxHistoricalWindow(t *testing.T) {
	expected := 3650 * 24 * time.Hour
	if MaxHistoricalWindow != expected {
		t.Errorf("MaxHistoricalWindow = %v, want %v", MaxHistoricalWindow, expected)
	}
}
