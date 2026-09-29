package collector

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

func cfetsServer(t *testing.T, md, csv []byte, now time.Time) *CFETSCollector {
	t.Helper()
	client, srv := testClient(t, "cfets-test", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case cfetsLatestPath:
			if md == nil {
				http.Error(w, "down", http.StatusNotFound)
				return
			}
			_, _ = w.Write(md)
		case cfetsHistoryPath:
			if csv == nil {
				http.Error(w, "down", http.StatusNotFound)
				return
			}
			_, _ = w.Write(csv)
		default:
			http.NotFound(w, r)
		}
	})
	c := NewCFETSCollectorWithClient(client)
	c.baseURL = srv.URL
	c.now = func() time.Time { return now }
	return c
}

// prr-chrt.csv has no header. Column 8 (0-based 7) is DR007 per CFETS' own
// chart script prr-h-chart.html (legend DR001/DR007/DR014 ← vArr[6..8]).
func TestParsePRRHistoryUsesEighthColumnAsDR007(t *testing.T) {
	rows, err := parsePRRHistory(fixture(t, "cfets_prr_chrt.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3 (leading CRLF skipped)", len(rows))
	}
	if !rows[0].date.Equal(dateUTC(2026, 9, 28)) || rows[0].rate != 1.3911 {
		t.Fatalf("first row = %+v, want 2026-09-28 1.3911", rows[0])
	}
}

func TestParsePRRLatestTakesDR007WeightedRate(t *testing.T) {
	got, err := parsePRRLatest(fixture(t, "cfets_prr_md.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !got.date.Equal(dateUTC(2026, 9, 29)) || got.rate != 1.3863 {
		t.Fatalf("latest = %+v, want 2026-09-29 weightedRate 1.3863", got)
	}
}

func TestCFETSLatestTodayIsPreliminaryAndCSVIsFinal(t *testing.T) {
	now := time.Date(2026, 9, 29, 7, 45, 0, 0, time.UTC) // 15:45 CST
	c := cfetsServer(t, fixture(t, "cfets_prr_md.json"), fixture(t, "cfets_prr_chrt.csv"), now)
	snaps, err := c.GetSnapshots(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 2 {
		t.Fatalf("snaps = %+v, want CSV newest + prr-md", snaps)
	}
	grades := map[time.Time]string{}
	for _, s := range snaps {
		grades[s.Timestamp] = s.Grade
		if s.MetricID != metricDR007 || s.Provider != providerCFETS {
			t.Errorf("unexpected snapshot %+v", s)
		}
	}
	if grades[dateUTC(2026, 9, 28)] != "delayed" || grades[dateUTC(2026, 9, 29)] != "preliminary" {
		t.Fatalf("grades = %v", grades)
	}
}

func TestCFETSPriorDayPRRMDIsFinal(t *testing.T) {
	now := time.Date(2026, 9, 30, 1, 0, 0, 0, time.UTC) // 09:00 CST next day
	c := cfetsServer(t, fixture(t, "cfets_prr_md.json"), fixture(t, "cfets_prr_chrt.csv"), now)
	snaps, err := c.GetSnapshots(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range snaps {
		if s.Grade != "delayed" {
			t.Fatalf("%v grade = %s, want delayed", s.Timestamp, s.Grade)
		}
	}
}

func TestCFETSSameDateEmitsOnceFromCSV(t *testing.T) {
	csv := []byte("2026-09-29,,,,,,1.3378,1.3863,1.3806\r\n2026-09-28,,,,,,1.3569,1.3911,1.399\r\n")
	now := time.Date(2026, 9, 30, 1, 0, 0, 0, time.UTC)
	c := cfetsServer(t, fixture(t, "cfets_prr_md.json"), csv, now)
	snaps, err := c.GetSnapshotsForWindow(context.Background(), dateUTC(2026, 9, 1), dateUTC(2026, 9, 30))
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 2 {
		t.Fatalf("snaps = %d, want 2 (no duplicate 09-29)", len(snaps))
	}
}

func TestCrossCheckDR007(t *testing.T) {
	latest := datedRate{date: dateUTC(2026, 9, 29), rate: 1.3863}
	if checked, msg := crossCheckDR007(latest, []datedRate{{dateUTC(2026, 9, 29), 1.3863}}); !checked || msg != "" {
		t.Fatalf("matching values: checked=%v msg=%q", checked, msg)
	}
	if checked, msg := crossCheckDR007(latest, []datedRate{{dateUTC(2026, 9, 29), 1.3816}}); !checked || !strings.Contains(msg, "!=") {
		t.Fatalf("mismatch must warn: checked=%v msg=%q", checked, msg)
	}
	if checked, _ := crossCheckDR007(latest, []datedRate{{dateUTC(2026, 9, 28), 1.3911}}); checked {
		t.Fatal("different dates cannot be cross-checked")
	}
}

func TestCFETSHistoryFailureKeepsLatest(t *testing.T) {
	now := time.Date(2026, 9, 29, 7, 45, 0, 0, time.UTC)
	c := cfetsServer(t, fixture(t, "cfets_prr_md.json"), nil, now)
	snaps, err := c.GetSnapshots(context.Background())
	if err == nil {
		t.Fatal("want partial error")
	}
	if len(snaps) != 1 || snaps[0].Value != 1.3863 {
		t.Fatalf("partial snaps = %+v", snaps)
	}
}
