package collector

import (
	"context"
	"os"
	"sort"
	"testing"
	"time"
)

// TestLiveSmoke hits the real endpoints. Opt-in only (CI never sets it):
//
//	CNMACRO_LIVE=1 go test -run TestLiveSmoke -v ./internal/collector
func TestLiveSmoke(t *testing.T) {
	if os.Getenv("CNMACRO_LIVE") != "1" {
		t.Skip("set CNMACRO_LIVE=1 to hit real endpoints")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	r := NewRealCollector()

	snaps, err := r.GetSnapshots(ctx)
	t.Logf("GetSnapshots: %d snapshots, err=%v", len(snaps), err)
	sort.Slice(snaps, func(i, j int) bool {
		if snaps[i].MetricID != snaps[j].MetricID {
			return snaps[i].MetricID < snaps[j].MetricID
		}
		return snaps[i].Timestamp.Before(snaps[j].Timestamp)
	})
	for _, s := range snaps {
		t.Logf("  %-26s %s  %.6g  grade=%s provider=%s", s.MetricID, s.Timestamp.Format("2006-01-02"), s.Value, s.Grade, s.Provider)
	}

	if os.Getenv("CNMACRO_LIVE_WINDOW") == "1" {
		end := time.Now().UTC()
		start := end.AddDate(0, 0, -45)
		w, err := r.GetSnapshotsForWindow(ctx, start, end)
		t.Logf("GetSnapshotsForWindow(%s..%s): %d snapshots, err=%v", start.Format("2006-01-02"), end.Format("2006-01-02"), len(w), err)
		counts := map[string]int{}
		for _, s := range w {
			counts[s.MetricID]++
		}
		t.Logf("  per metric: %v", counts)
	}

	if from := os.Getenv("CNMACRO_LIVE_AFRE_FROM"); from != "" {
		start, err := time.Parse("2006-01-02", from)
		if err != nil {
			t.Fatal(err)
		}
		a, err := r.afre.GetSnapshotsForWindow(ctx, start, time.Now().UTC())
		t.Logf("AFRE window from %s: %d snapshots, err=%v", from, len(a), err)
		sort.Slice(a, func(i, j int) bool { return a[i].Timestamp.Before(a[j].Timestamp) })
		for _, s := range a {
			t.Logf("  %s  %.0f 亿元", s.Timestamp.Format("2006-01"), s.Value/afreUnitCNY)
		}
	}
}
