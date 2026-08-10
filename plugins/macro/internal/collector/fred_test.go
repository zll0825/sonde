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

func TestWALCLUnitsAreUSD(t *testing.T) {
	// Sanity: WALCL scale must be 1e6 (millions -> USD).
	for _, entry := range fredSeriesList {
		if entry.MetricID == "fed.ins.balance_sheet" && entry.UnitScale != 1e6 {
			t.Errorf("WALCL UnitScale = %v, want 1e6 (FRED reports in MILLIONS of USD)", entry.UnitScale)
		}
	}
}
