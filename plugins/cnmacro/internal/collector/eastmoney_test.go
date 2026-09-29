package collector

import (
	"context"
	"net/http"
	"testing"
	"time"

	"sonde/pkg/model"
)

func eastMoneyServer(t *testing.T, requests *[]string) *EastMoneyCollector {
	t.Helper()
	client, srv := testClient(t, "eastmoney-test", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/data/v1/get" {
			http.NotFound(w, r)
			return
		}
		if ua := r.Header.Get("User-Agent"); ua != browserUserAgent {
			t.Errorf("User-Agent = %q, want browser-like", ua)
		}
		*requests = append(*requests, r.URL.RawQuery)
		switch r.URL.Query().Get("reportName") {
		case reportMoney:
			_, _ = w.Write(fixture(t, "eastmoney_money_supply.json"))
		case reportLPR:
			_, _ = w.Write(fixture(t, "eastmoney_lpr.json"))
		default:
			http.Error(w, "unknown report", http.StatusBadRequest)
		}
	})
	c := NewEastMoneyCollectorWithClient(client)
	c.baseURL = srv.URL
	return c
}

func TestEastMoneyLatestMapsM1M2AndGap(t *testing.T) {
	var reqs []string
	c := eastMoneyServer(t, &reqs)
	snaps, err := c.GetSnapshots(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := byMetric(snaps)
	aug := dateUTC(2026, 8, 1)
	check := func(id string, want float64, ts time.Time) {
		t.Helper()
		if len(got[id]) != 1 {
			t.Fatalf("%s: %d snapshots, want 1 (latest only)", id, len(got[id]))
		}
		s := got[id][0]
		if s.Value != want || !s.Timestamp.Equal(ts) {
			t.Errorf("%s = %v @ %v, want %v @ %v", id, s.Value, s.Timestamp, want, ts)
		}
		if s.SourceClass != model.SourceClassReal || s.Provider != providerEastMoney {
			t.Errorf("%s class=%v provider=%s", id, s.SourceClass, s.Provider)
		}
	}
	// BASIC_CURRENCY_SAME is M2 YoY; CURRENCY_SAME is M1 YoY (verified
	// against the PBoC 2026 money-supply table: BASIC_CURRENCY = M2 level).
	check(metricM2Yoy, 7.5, aug)
	check(metricM1Yoy, 4.1, aug)
	check(metricM1M2, -3.4, aug)
	check(metricLPR1Y, 3, dateUTC(2026, 9, 20))
	check(metricLPR5Y, 3.5, dateUTC(2026, 9, 20))
	if len(reqs) != 2 {
		t.Fatalf("requests = %d, want 2", len(reqs))
	}
}

func TestEastMoneyWindowDropsPreReformLPRAndFiltersMonths(t *testing.T) {
	var reqs []string
	c := eastMoneyServer(t, &reqs)
	snaps, err := c.GetSnapshotsForWindow(context.Background(), dateUTC(2019, 8, 1), dateUTC(2026, 7, 31))
	if err != nil {
		t.Fatal(err)
	}
	got := byMetric(snaps)
	// Money rows: 2026-06, 2026-07 in window; 2026-08 is after end.
	if n := len(got[metricM2Yoy]); n != 2 {
		t.Fatalf("m2 window rows = %d, want 2", n)
	}
	// LPR: only 2019-08-20 in window; 2019-08-16 is pre-reform.
	if n := len(got[metricLPR1Y]); n != 1 || !got[metricLPR1Y][0].Timestamp.Equal(dateUTC(2019, 8, 20)) {
		t.Fatalf("lpr_1y window = %+v", got[metricLPR1Y])
	}
	if n := len(got[metricLPR5Y]); n != 1 || got[metricLPR5Y][0].Value != 4.85 {
		t.Fatalf("lpr_5y window = %+v", got[metricLPR5Y])
	}
}

func TestLPRNullIsAbsentNotZero(t *testing.T) {
	rows := []lprRow{{TradeDate: "2019-08-20 00:00:00", LPR1Y: optFloat{4.25, true}}}
	snaps := lprSnapshots(rows, time.Now())
	if len(snaps) != 1 || snaps[0].MetricID != metricLPR1Y {
		t.Fatalf("null LPR5Y must not emit a 0 snapshot: %+v", snaps)
	}
}

func TestEastMoneyFailureIsReported(t *testing.T) {
	client, srv := testClient(t, "eastmoney-fail", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"result":null,"success":false,"message":"返回数据为空","code":9201}`))
	})
	c := NewEastMoneyCollectorWithClient(client)
	c.baseURL = srv.URL
	if _, err := c.GetSnapshots(context.Background()); err == nil {
		t.Fatal("want error when both reports fail")
	}
}
