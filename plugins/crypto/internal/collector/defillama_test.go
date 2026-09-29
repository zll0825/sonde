package collector

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"sonde/pkg/model"
	"sonde/pkg/provider"
)

// Shape copied from https://stablecoins.llama.fi/stablecoincharts/all:
// date is a unix-seconds string, totals are keyed by peg type.
const stablecoinChartsFixture = `[
{"date":"1758931200","totalCirculating":{"peggedUSD":304000000000,"peggedEUR":500000000},"totalCirculatingUSD":{"peggedUSD":305000000000,"peggedEUR":580000000}},
{"date":"1759017600","totalCirculating":{"peggedUSD":305100000000},"totalCirculatingUSD":{"peggedUSD":306305000000}},
{"date":"bad","totalCirculatingUSD":{"peggedUSD":1}},
{"date":"1759104000","totalCirculatingUSD":{"peggedEUR":600000000}}
]`

var notFoundRT = roundTripFunc(func(req *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("not found")), Header: make(http.Header)}, nil
})

func fixtureRT(body string) roundTripFunc {
	return func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	}
}

func newTestStablecoinCollector(rt roundTripFunc) *StablecoinCollector {
	return &StablecoinCollector{client: provider.NewSafeHTTPClientWithHTTPClient(testProviderConfig("defillama-test"), &http.Client{Transport: rt})}
}

func TestParseStablecoinCharts_UsesUSDPegAndSkipsBadRows(t *testing.T) {
	times, values, err := parseStablecoinCharts([]byte(stablecoinChartsFixture), time.Time{}, time.Unix(1759200000, 0))
	if err != nil {
		t.Fatal(err)
	}
	// Row 3 has an unparseable date; row 4 has no USD peg. Only rows 1-2 survive,
	// and only peggedUSD counts (EUR stablecoins are a different dollar-liquidity story).
	if len(values) != 2 || values[0] != 305_000_000_000 || values[1] != 306_305_000_000 {
		t.Fatalf("values=%v", values)
	}
	if !times[1].Equal(time.Unix(1759017600, 0).UTC()) {
		t.Fatalf("times[1]=%v", times[1])
	}
}

func TestParseStablecoinCharts_FiltersWindow(t *testing.T) {
	_, values, err := parseStablecoinCharts([]byte(stablecoinChartsFixture), time.Unix(1759000000, 0), time.Unix(1759200000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || values[0] != 306_305_000_000 {
		t.Fatalf("values=%v, want only the in-window row", values)
	}
}

func TestStablecoinCollector_LatestIsLastRow(t *testing.T) {
	c := newTestStablecoinCollector(fixtureRT(stablecoinChartsFixture))
	v, ts, err := c.GetLatestSupply(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v != 306_305_000_000 || ts.Unix() != 1759017600 {
		t.Fatalf("latest=%v@%v", v, ts)
	}
}

func TestStablecoinCollector_RejectsGarbage(t *testing.T) {
	c := newTestStablecoinCollector(fixtureRT(`{"error":"rate limited"}`))
	if _, _, err := c.GetLatestSupply(context.Background()); err == nil {
		t.Fatal("want decode error for non-array body")
	}
}

func TestRealCollector_IncludesStablecoinSupply(t *testing.T) {
	r := newTestRealCollector(notFoundRT, notFoundRT, notFoundRT)
	r.stablecoins = newTestStablecoinCollector(fixtureRT(stablecoinChartsFixture))
	snaps, err := r.GetSnapshots(context.Background())
	if err != nil {
		t.Fatalf("GetSnapshots: %v", err)
	}
	if len(snaps) != 1 || snaps[0].MetricID != MetricStablecoinSupply || snaps[0].SourceClass != model.SourceClassReal {
		t.Fatalf("snaps=%+v, want one real stablecoin snapshot", snaps)
	}
}
