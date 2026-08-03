package collector

import (
	"context"
	"testing"
	"time"
)

// Regression: RealCollector backfill must NOT fabricate price / hash-rate
// history from the Mock random walk — GetObservations has no provider filter,
// so fake baselines would drive the percentile/trend detectors into bogus
// alerts against real live values. Only exchange_balance (mock in the live
// path too) may come from Mock.
func TestRealCollectorWindow_OnlyExchangeBalance(t *testing.T) {
	// Arrange
	r := NewRealCollector()
	end := time.Now()
	start := end.AddDate(0, 0, -10)

	// Act
	snaps, err := r.GetSnapshotsForWindow(context.Background(), start, end)

	// Assert
	if err != nil {
		t.Fatalf("GetSnapshotsForWindow: %v", err)
	}
	if len(snaps) == 0 {
		t.Fatal("expected exchange_balance history, got none")
	}
	for _, s := range snaps {
		if s.MetricID != "btc.ass.exchange_balance" {
			t.Errorf("backfill fabricated history for %q — only exchange_balance is allowed", s.MetricID)
		}
	}
}
