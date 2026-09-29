package collector

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLive hits the real CFTC and FINRA endpoints. Skipped unless SONDE_LIVE=1:
//
//	SONDE_LIVE=1 go test ./internal/collector -run Live -v
func TestLive(t *testing.T) {
	if os.Getenv("SONDE_LIVE") != "1" {
		t.Skip("set SONDE_LIVE=1 to hit real endpoints")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	c := NewRealCollector()
	snaps, err := c.GetSnapshots(ctx)
	if err != nil {
		t.Errorf("latest: %v", err)
	}
	for _, s := range snaps {
		t.Logf("latest %-22s %18.0f  ts=%s (%s) provider=%s", s.MetricID, s.Value, s.Timestamp.Format("2006-01-02"), s.Timestamp.Weekday(), s.Provider)
	}

	end := time.Now().UTC()
	win, err := c.GetSnapshotsForWindow(ctx, end.AddDate(0, -3, 0), end)
	if err != nil {
		t.Errorf("window: %v", err)
	}
	counts := map[string]int{}
	for _, s := range win {
		counts[s.MetricID]++
	}
	t.Logf("3-month window counts: %v", counts)
}
