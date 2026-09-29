package collector

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"sonde/pkg/model"
)

// testdata/cftc_cot.json is a real response (2026-09-29) for the four tracked
// codes, report dates 2026-09-15 and 2026-09-22.
func cftcFixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/cftc_cot.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func newTestCFTC(t *testing.T, now time.Time, h http.HandlerFunc) *CFTCCollector {
	client, srv := testClient(t, "cftc-test", h)
	c := NewCFTCCollectorWithClient(client)
	c.url = srv.URL
	c.now = func() time.Time { return now }
	return c
}

func TestCFTCLatestIsTuesdayNetNoncommAndReal(t *testing.T) {
	body := cftcFixture(t)
	var gotQuery string
	var gotToken string
	c := newTestCFTC(t, time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC), func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("$where")
		gotToken = r.Header.Get("X-App-Token")
		_, _ = w.Write(body)
	})
	snaps, err := c.GetSnapshots(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if gotToken != "" {
		t.Errorf("anonymous collector sent X-App-Token %q", gotToken)
	}
	for _, code := range []string{"13874A", "043602", "088691", "133741"} {
		if !strings.Contains(gotQuery, "'"+code+"'") {
			t.Errorf("$where %q missing code %s", gotQuery, code)
		}
	}
	want := map[string]float64{
		"cot.es.noncomm_net":  218043 - 351271,
		"cot.zn.noncomm_net":  601677 - 1413429,
		"cot.gc.noncomm_net":  253982 - 28129,
		"cot.btc.noncomm_net": 17658 - 14902,
	}
	tuesday := dateUTC(2026, 9, 22)
	if len(snaps) != len(want) {
		t.Fatalf("snaps=%d, want %d: %+v", len(snaps), len(want), snaps)
	}
	for _, s := range snaps {
		if s.Value != want[s.MetricID] {
			t.Errorf("%s = %v, want %v", s.MetricID, s.Value, want[s.MetricID])
		}
		if !s.Timestamp.Equal(tuesday) || s.Timestamp.Weekday() != time.Tuesday {
			t.Errorf("%s timestamp = %s, want Tuesday report date %s", s.MetricID, s.Timestamp, tuesday)
		}
		if s.SourceClass != model.SourceClassReal || s.Provider != providerCFTC {
			t.Errorf("%s class/provider = %s/%s", s.MetricID, s.SourceClass, s.Provider)
		}
	}
}

// 周二持仓、周五 15:30 ET 发布：发布前（例如周五 15:00 ET）不得产出该周观测。
func TestCFTCDropsReportsBeforeFridayRelease(t *testing.T) {
	body := cftcFixture(t)
	beforeRelease := time.Date(2026, 9, 25, 19, 0, 0, 0, time.UTC) // Fri 15:00 EDT
	c := newTestCFTC(t, beforeRelease, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) })
	snaps, err := c.GetSnapshots(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range snaps {
		if !s.Timestamp.Equal(dateUTC(2026, 9, 15)) {
			t.Errorf("%s used unreleased report %s", s.MetricID, s.Timestamp)
		}
	}

	if got := cotReleaseTime(dateUTC(2026, 9, 22)); !got.Equal(time.Date(2026, 9, 25, 20, 30, 0, 0, time.UTC)) {
		t.Errorf("release time = %s", got)
	}
}

func TestCFTCSendsOptionalAppToken(t *testing.T) {
	t.Setenv(envCFTCAppToken, "tok-123")
	c := NewCFTCCollector()
	if c.token != "tok-123" {
		t.Fatalf("token = %q", c.token)
	}
	t.Setenv(envCFTCAppToken, "")
	if NewCFTCCollector().token != "" {
		t.Fatal("empty env must mean anonymous")
	}

	body := cftcFixture(t)
	var got string
	tc := newTestCFTC(t, time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC), func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-App-Token")
		_, _ = w.Write(body)
	})
	tc.token = "tok-123"
	if _, err := tc.GetSnapshots(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got != "tok-123" {
		t.Fatalf("X-App-Token = %q", got)
	}
}

func TestCFTCWindowUsesSoQLRange(t *testing.T) {
	body := cftcFixture(t)
	var wheres, offsets []string
	c := newTestCFTC(t, time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC), func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		wheres = append(wheres, q.Get("$where"))
		offsets = append(offsets, q.Get("$offset"))
		if q.Get("$order") == "" || q.Get("$limit") == "" {
			t.Errorf("missing $order/$limit: %v", q)
		}
		_, _ = w.Write(body)
	})
	snaps, err := c.GetSnapshotsForWindow(context.Background(), dateUTC(2026, 9, 1), dateUTC(2026, 9, 28))
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 8 {
		t.Fatalf("window snaps = %d, want 8 (4 contracts × 2 weeks)", len(snaps))
	}
	if len(wheres) != 1 || !strings.Contains(wheres[0], "between '2026-09-01T00:00:00' and '2026-09-28T23:59:59'") {
		t.Fatalf("$where = %v", wheres)
	}
	if offsets[0] != "0" {
		t.Fatalf("offset = %v", offsets)
	}
	for i := 1; i < len(snaps); i++ {
		if snaps[i].Timestamp.Before(snaps[i-1].Timestamp) {
			t.Fatal("window snapshots must be ascending")
		}
	}
}

func TestCFTCWindowFollowsOffsetWhenPageIsFull(t *testing.T) {
	rows := []string{
		`{"report_date_as_yyyy_mm_dd":"2026-09-15T00:00:00.000","cftc_contract_market_code":"13874A","noncomm_positions_long_all":"10","noncomm_positions_short_all":"4"}`,
		`{"report_date_as_yyyy_mm_dd":"2026-09-22T00:00:00.000","cftc_contract_market_code":"13874A","noncomm_positions_long_all":"20","noncomm_positions_short_all":"5"}`,
		`{"report_date_as_yyyy_mm_dd":"2026-09-22T00:00:00.000","cftc_contract_market_code":"999999","noncomm_positions_long_all":"1","noncomm_positions_short_all":"1"}`,
	}
	var calls int
	c := newTestCFTC(t, time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC), func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch r.URL.Query().Get("$offset") {
		case "0":
			_, _ = w.Write([]byte("[" + rows[0] + "," + rows[1] + "]"))
		case "2":
			_, _ = w.Write([]byte("[" + rows[2] + "]"))
		default:
			t.Errorf("unexpected offset %q", r.URL.Query().Get("$offset"))
		}
	})
	c.pageSize = 2
	snaps, err := c.GetSnapshotsForWindow(context.Background(), dateUTC(2026, 9, 1), dateUTC(2026, 9, 28))
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(snaps) != 2 || snaps[0].Value != 6 || snaps[1].Value != 15 {
		t.Fatalf("calls=%d snaps=%+v", calls, snaps)
	}
}

func TestCFTCUpstreamErrorFails(t *testing.T) {
	c := newTestCFTC(t, time.Now(), func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "throttled", http.StatusForbidden)
	})
	if _, err := c.GetSnapshots(context.Background()); err == nil {
		t.Fatal("403 must surface as error")
	}
}
