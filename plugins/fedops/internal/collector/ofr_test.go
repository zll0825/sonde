package collector

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"testing"
	"time"
)

func TestOFRParsesMillisecondPairsAndPicksLatest(t *testing.T) {
	t1 := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	client, srv := testClient(t, "ofr-test", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{
			"OFRFSI":{"data":[`+
			`[`+strconv.FormatInt(t1.UnixMilli(), 10)+`, -0.40],`+
			`[`+strconv.FormatInt(t2.UnixMilli(), 10)+`, -0.25]`+
			`]}
		}`)
	})
	c := NewOFRCollectorWithClient(client)
	c.url = srv.URL

	snaps, err := c.GetSnapshots(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 1 || snaps[0].Value != -0.25 {
		t.Fatalf("latest = %+v", snaps)
	}
	if !snaps[0].Timestamp.Equal(t2) {
		t.Fatalf("timestamp = %s, want %s", snaps[0].Timestamp, t2)
	}

	win, err := c.GetSnapshotsForWindow(context.Background(), t1, t1.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(win) != 1 || win[0].Value != -0.40 {
		t.Fatalf("window = %+v, want only 2026-09-01", win)
	}
}
