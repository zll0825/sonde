package collector

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"sonde/pkg/model"
	"sonde/pkg/provider"
)

// Fixtures in testdata/ are real responses captured on 2026-09-29.

func testSafeClient(name string, rt roundTripFunc) *provider.SafeHTTPClient {
	return provider.NewSafeHTTPClientWithHTTPClient(testProviderConfig(name), &http.Client{Transport: rt})
}

func readFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// routeRT serves fixtures by URL path and records every request URL.
func routeRT(routes map[string]string, seen *[]string) roundTripFunc {
	return func(req *http.Request) (*http.Response, error) {
		if seen != nil {
			*seen = append(*seen, req.URL.String())
		}
		body, ok := routes[req.URL.Path]
		if !ok {
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("nf")), Header: make(http.Header)}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	}
}

func ms(v int64) time.Time { return time.UnixMilli(v).UTC() }

func TestTFTCParsesWholeUSDAndSkipsHolidayPlaceholders(t *testing.T) {
	c := &TFTCCollector{client: testSafeClient("tftc-test", fixtureRT(readFixture(t, "tftc_flows.json"))), url: "https://tftc.test/data.json"}

	times, values, err := c.GetFlowHistory(context.Background(), time.Time{}, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	// 2024-01-15 (MLK day) is netFlowUsd 0 + perEtfUsd null → skipped.
	wantDates := []string{"2024-01-12", "2024-01-16", "2026-09-24", "2026-09-25"}
	if len(times) != len(wantDates) {
		t.Fatalf("times = %v", times)
	}
	for i, d := range wantDates {
		if times[i].Format("2006-01-02") != d {
			t.Errorf("times[%d] = %s, want %s", i, times[i], d)
		}
	}
	if values[1] != -52_700_000 {
		t.Errorf("2024-01-16 = %v, want -52.7M USD (units are whole USD)", values[1])
	}

	v, ts, err := c.GetLatestFlow(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v != 134465169.155 || ts.Format("2006-01-02") != "2026-09-25" {
		t.Fatalf("latest = %v @ %s", v, ts)
	}
}

func TestTFTCRejectsUnexpectedUnits(t *testing.T) {
	_, _, err := parseTFTCFlows([]byte(`{"units":"USD millions","days":[{"date":"2026-09-25","netFlowUsd":134.4}]}`), time.Time{}, time.Now())
	if err == nil {
		t.Fatal("units other than USD must fail rather than silently mis-scale")
	}
}

func TestOKXLatestUsesOICcyAndSettledFunding(t *testing.T) {
	var seen []string
	c := &OKXCollector{
		client: testSafeClient("okx-test", routeRT(map[string]string{
			"/api/v5/public/open-interest": readFixture(t, "okx_open_interest.json"),
			"/api/v5/public/funding-rate":  readFixture(t, "okx_funding_rate.json"),
		}, &seen)),
		base: "https://okx.test",
		now:  time.Now,
	}
	oi, oiTS, err := c.GetLatestOpenInterest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if oi != 28009.1882000000916 || !oiTS.Equal(ms(1790669395968)) {
		t.Fatalf("OI = %v @ %s, want oiCcy (BTC)", oi, oiTS)
	}
	if !strings.Contains(seen[0], "instId=BTC-USDT-SWAP") || !strings.Contains(seen[0], "instType=SWAP") {
		t.Fatalf("OI request = %s", seen[0])
	}

	fr, frTS, err := c.GetLatestFundingRate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// settFundingRate 0.0000254900915325 settled at prevFundingTime, as percent.
	if diff := fr - 0.00254900915325; diff > 1e-12 || diff < -1e-12 {
		t.Fatalf("funding = %v %%, want settled 0.00254900915325 %%", fr)
	}
	if !frTS.Equal(ms(1790668800000)) {
		t.Fatalf("funding ts = %s, want prevFundingTime", frTS)
	}
}

func TestOKXLatestFundingFallsBackToHistoryWhileSettling(t *testing.T) {
	settling := strings.Replace(readFixture(t, "okx_funding_rate.json"), `"settState":"settled"`, `"settState":"processing"`, 1)
	c := &OKXCollector{
		client: testSafeClient("okx-test", routeRT(map[string]string{
			"/api/v5/public/funding-rate":         settling,
			"/api/v5/public/funding-rate-history": readFixture(t, "okx_funding_history.json"),
		}, nil)),
		base: "https://okx.test",
		now:  time.Now,
	}
	fr, ts, err := c.GetLatestFundingRate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !ts.Equal(ms(1790668800000)) || fr < 0.0025 || fr > 0.0026 {
		t.Fatalf("fallback funding = %v @ %s", fr, ts)
	}
}

func TestOKXHistoryAscendingClosedBarsAndFundingWindow(t *testing.T) {
	c := &OKXCollector{
		client: testSafeClient("okx-test", routeRT(map[string]string{
			"/api/v5/rubik/stat/contracts/open-interest-history": readFixture(t, "okx_oi_history.json"),
			"/api/v5/public/funding-rate-history":                readFixture(t, "okx_funding_history.json"),
		}, nil)),
		base: "https://okx.test",
		// Fixture captured 2026-09-29 08:10 UTC: the bar opened 09-28 16:00 UTC is still open.
		now: func() time.Time { return ms(1790669395968) },
	}
	start, end := ms(1790380800000), ms(1790669395968)
	times, values, err := c.GetOpenInterestHistory(context.Background(), start, end)
	if err != nil {
		t.Fatal(err)
	}
	if len(times) != 2 {
		t.Fatalf("OI history = %v %v, want 2 closed bars", times, values)
	}
	if !times[0].Equal(ms(1790438400000).Add(24*time.Hour)) || values[0] != 27962.3102000000594 {
		t.Fatalf("first = %s %v, want stamped at bar close with oiCcy", times[0], values[0])
	}
	if !times[1].After(times[0]) {
		t.Fatal("history must be ascending")
	}

	ft, fv, err := c.GetFundingRateHistory(context.Background(), ms(1790611200000), ms(1790668800000))
	if err != nil {
		t.Fatal(err)
	}
	if len(ft) != 3 || !ft[0].Equal(ms(1790611200000)) || !ft[2].Equal(ms(1790668800000)) {
		t.Fatalf("funding history = %v", ft)
	}
	if fv[0] < 0.0070 || fv[0] > 0.0071 {
		t.Fatalf("funding[0] = %v %%, want 0.00709…%%", fv[0])
	}
}

func TestOKXErrorCodeFails(t *testing.T) {
	c := &OKXCollector{client: testSafeClient("okx-test", fixtureRT(`{"code":"51001","data":[],"msg":"Instrument ID does not exist"}`)), base: "https://okx.test", now: time.Now}
	if _, _, err := c.GetLatestOpenInterest(context.Background()); err == nil || !strings.Contains(err.Error(), "51001") {
		t.Fatalf("err = %v", err)
	}
}

func TestDeribitDVOLDropsFormingBarAndFollowsContinuation(t *testing.T) {
	now := ms(1790669398937) // 2026-09-29 08:09 UTC
	c := &DeribitCollector{
		client: testSafeClient("deribit-test", fixtureRT(readFixture(t, "deribit_dvol.json"))),
		base:   "https://deribit.test",
		now:    func() time.Time { return now },
	}
	v, ts, err := c.GetLatestDVOL(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// The 2026-09-29 bar is still forming; latest completed is 2026-09-28 close.
	if v != 35.93 || ts.Format("2006-01-02") != "2026-09-28" {
		t.Fatalf("latest DVOL = %v @ %s", v, ts)
	}

	var calls []string
	pages := []string{
		`{"jsonrpc":"2.0","result":{"data":[[1790467200000,1,1,1,35.07],[1790553600000,1,1,1,35.93]],"continuation":1790380800000}}`,
		`{"jsonrpc":"2.0","result":{"data":[[1790294400000,1,1,1,34.62],[1790380800000,1,1,1,34.92]],"continuation":null}}`,
	}
	c.client = testSafeClient("deribit-test", func(req *http.Request) (*http.Response, error) {
		calls = append(calls, req.URL.Query().Get("end_timestamp"))
		body := pages[len(calls)-1]
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	times, values, err := c.GetDVOLHistory(context.Background(), ms(1790294400000), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[1] != "1790380800000" {
		t.Fatalf("calls = %v, want continuation as next end_timestamp", calls)
	}
	if len(times) != 4 || values[0] != 34.62 || values[3] != 35.93 {
		t.Fatalf("history = %v %v", times, values)
	}
}

func TestDeribitErrorPayloadFails(t *testing.T) {
	c := &DeribitCollector{client: testSafeClient("deribit-test", fixtureRT(`{"jsonrpc":"2.0","error":{"code":10043,"message":"bad"}}`)), base: "https://deribit.test", now: time.Now}
	if _, _, err := c.GetLatestDVOL(context.Background()); err == nil {
		t.Fatal("error payload must fail")
	}
}

func TestRealCollectorEmitsNewMetricsAsReal(t *testing.T) {
	r := newTestRealCollector(notFoundRT, notFoundRT, notFoundRT)
	r.etfFlows.client = testSafeClient("tftc-test", fixtureRT(readFixture(t, "tftc_flows.json")))
	r.okx.client = testSafeClient("okx-test", routeRT(map[string]string{
		"/api/v5/public/open-interest": readFixture(t, "okx_open_interest.json"),
		"/api/v5/public/funding-rate":  readFixture(t, "okx_funding_rate.json"),
	}, nil))
	// Deribit left failing: its metric must be dropped without losing the others.

	snaps, err := r.GetSnapshots(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, s := range snaps {
		got[s.MetricID] = true
		if s.SourceClass != model.SourceClassReal {
			t.Errorf("%s class = %s", s.MetricID, s.SourceClass)
		}
	}
	for _, id := range []string{MetricETFNetFlow, MetricOKXOpenInterest, MetricOKXFundingRate} {
		if !got[id] {
			t.Errorf("missing %s", id)
		}
	}
	if got[MetricDVOL] {
		t.Error("failed Deribit source must not emit dvol")
	}
}
