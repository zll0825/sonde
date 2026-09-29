package collector

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

const (
	testAFRETablePath   = "/diaochatongjisi/attachDir/2026/09/2026091417323857622.htm"
	testAFRESectionPath = "/diaochatongjisi/116219/116319/2026ntjsj/shrzgm/index.html"
	testAFREYearPath    = "/diaochatongjisi/116219/116319/2026ntjsj/index.html"
)

func afreServer(t *testing.T, hits map[string]int, now time.Time) *AFRECollector {
	t.Helper()
	client, srv := testClient(t, "pbc-afre-test", func(w http.ResponseWriter, r *http.Request) {
		hits[r.URL.Path]++
		switch r.URL.Path {
		case afreRootPath:
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write(fixture(t, "pbc_stats_root.html"))
		case testAFREYearPath:
			_, _ = w.Write(fixture(t, "pbc_year_2026.html"))
		case testAFRESectionPath:
			_, _ = w.Write(fixture(t, "pbc_afre_section_2026.html"))
		case testAFRETablePath:
			// Real server sends bare "text/html"; charset only in <meta>.
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write(fixture(t, "pbc_afre_table_2026.gbk.htm"))
		default:
			http.NotFound(w, r)
		}
	})
	c := NewAFRECollector(client)
	c.baseURL = srv.URL
	c.now = func() time.Time { return now }
	return c
}

func TestFindAFRETableMatchesExactTitle(t *testing.T) {
	href, ok := findAFRETable(string(fixture(t, "pbc_afre_section_2026.html")))
	if !ok || href != testAFRETablePath {
		t.Fatalf("href = %q ok=%v", href, ok)
	}
	// 存量 or 地区 rows must never be picked even if listed first.
	decoy := `<div class="titp20">社会融资规模存量统计表<br></div><a href="/stock.htm">htm</a>` +
		`<div class="titp20">地区社会融资规模增量统计表<br></div><a href="/region.htm">htm</a>`
	if href, ok := findAFRETable(decoy); ok {
		t.Fatalf("decoy matched %q", href)
	}
}

func TestFindAFRESectionSkipsRegional(t *testing.T) {
	href, ok := findAFRESection(string(fixture(t, "pbc_year_2026.html")))
	if !ok || href != testAFRESectionPath {
		t.Fatalf("section = %q ok=%v", href, ok)
	}
}

func TestParseAFRETableGBKFirstOccurrenceAndBlanks(t *testing.T) {
	page, err := decodeHTML(fetched{body: fixture(t, "pbc_afre_table_2026.gbk.htm"), contentType: "text/html"})
	if err != nil {
		t.Fatal(err)
	}
	vals := parseAFRETable(page, 2026)
	got := map[time.Month]float64{}
	for _, v := range vals {
		got[v.month.Month()] = v.value
	}
	want := map[time.Month]float64{time.January: 72185, time.July: 14068, time.August: 16577}
	if len(got) != len(want) {
		t.Fatalf("months = %v, want %v (Sept/Dec blank rows skipped)", got, want)
	}
	for m, v := range want {
		if got[m] != v {
			t.Errorf("%v = %v, want %v (share-section 100.0 must not override)", m, got[m], v)
		}
	}
	if other := parseAFRETable(page, 2025); len(other) != 0 {
		t.Fatalf("rows of another year must be ignored: %+v", other)
	}
}

func TestAFRELatestDiscoversTableAndScales(t *testing.T) {
	hits := map[string]int{}
	c := afreServer(t, hits, time.Date(2026, 9, 29, 8, 0, 0, 0, time.UTC))
	snaps, err := c.GetSnapshots(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 1 {
		t.Fatalf("snaps = %+v", snaps)
	}
	s := snaps[0]
	if s.MetricID != metricAFRE || s.Value != 16577e8 || !s.Timestamp.Equal(dateUTC(2026, 8, 1)) || s.Provider != providerPBC {
		t.Fatalf("snap = %+v", s)
	}
	for _, p := range []string{afreRootPath, testAFREYearPath, testAFRESectionPath, testAFRETablePath} {
		if hits[p] != 1 {
			t.Errorf("path %s hit %d times, want 1", p, hits[p])
		}
	}
}

func TestAFREWindowSkipsMissingYearButKeepsOthers(t *testing.T) {
	hits := map[string]int{}
	c := afreServer(t, hits, time.Date(2026, 9, 29, 8, 0, 0, 0, time.UTC))
	// 2025's year page is linked from the fixture root but not served → 404.
	snaps, err := c.GetSnapshotsForWindow(context.Background(), dateUTC(2025, 6, 1), dateUTC(2026, 7, 31))
	if err == nil || !strings.Contains(err.Error(), "2025") {
		t.Fatalf("want partial error naming 2025, got %v", err)
	}
	got := map[time.Time]float64{}
	for _, s := range snaps {
		got[s.Timestamp] = s.Value
	}
	if len(got) != 2 || got[dateUTC(2026, 1, 1)] != 72185e8 || got[dateUTC(2026, 7, 1)] != 14068e8 {
		t.Fatalf("window snaps = %v, want Jan & Jul 2026 (Aug is after end)", got)
	}
}

func TestPBCCookieJarSurvivesChallengeRedirect(t *testing.T) {
	client, srv := testClient(t, "pbc-cookie-test", func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie("wzws_cid"); err != nil {
			http.SetCookie(w, &http.Cookie{Name: "wzws_cid", Value: "x", Path: "/"})
			http.Redirect(w, r, r.URL.Path, http.StatusFound)
			return
		}
		_, _ = w.Write([]byte(`<html><meta charset="utf-8">ok</html>`))
	})
	page, err := pbcGetHTML(context.Background(), client, srv.URL+"/x/index.html")
	if err != nil || !strings.Contains(page, "ok") {
		t.Fatalf("page=%q err=%v", page, err)
	}
}
