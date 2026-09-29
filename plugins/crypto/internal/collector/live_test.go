package collector

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLiveDerivatives hits the real TFTC, OKX and Deribit endpoints. Skipped
// unless SONDE_LIVE=1:
//
//	SONDE_LIVE=1 go test ./internal/collector -run Live -v
func TestLiveDerivatives(t *testing.T) {
	if os.Getenv("SONDE_LIVE") != "1" {
		t.Skip("set SONDE_LIVE=1 to hit real endpoints")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	r := &RealCollector{etfFlows: NewTFTCCollector(), okx: NewOKXCollector(), deribit: NewDeribitCollector()}

	for _, s := range r.latestPositioning(ctx) {
		t.Logf("latest %-26s %20.8f  ts=%s provider=%s", s.MetricID, s.Value, s.Timestamp.Format(time.RFC3339), s.Provider)
	}
	end := time.Now().UTC()
	hist := r.historyPositioning(ctx, end.AddDate(0, 0, -30), end)
	counts := map[string]int{}
	first := map[string]time.Time{}
	for _, s := range hist {
		counts[s.MetricID]++
		if f, ok := first[s.MetricID]; !ok || s.Timestamp.Before(f) {
			first[s.MetricID] = s.Timestamp
		}
	}
	t.Logf("30-day window counts: %v", counts)
	long := r.historyPositioning(ctx, end.AddDate(-3, 0, 0), end)
	lc := map[string]int{}
	lf := map[string]time.Time{}
	for _, s := range long {
		lc[s.MetricID]++
		if f, ok := lf[s.MetricID]; !ok || s.Timestamp.Before(f) {
			lf[s.MetricID] = s.Timestamp
		}
	}
	for id, n := range lc {
		t.Logf("3-year window %-26s n=%d earliest=%s", id, n, lf[id].Format("2006-01-02"))
	}
}
