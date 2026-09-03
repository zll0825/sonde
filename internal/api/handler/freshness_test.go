package handler

import (
	"testing"
	"time"
)

// TestFreshness covers the design D2 threshold table for every declared
// frequency. Boundary staleness counts as the relaxed side: staleness exactly
// at the green threshold is green, exactly at the yellow threshold is yellow.
// Unknown frequencies fall back to daily.
func TestFreshness(t *testing.T) {
	tests := []struct {
		name      string
		frequency string
		staleness time.Duration
		want      string
	}{
		// realtime: green ≤ 30m, yellow ≤ 2h, red > 2h
		{"realtime fresh", "realtime", 5 * time.Minute, freshnessGreen},
		{"realtime green boundary 30m", "realtime", 30 * time.Minute, freshnessGreen},
		{"realtime yellow just past green", "realtime", 31 * time.Minute, freshnessYellow},
		{"realtime yellow boundary 2h", "realtime", 2 * time.Hour, freshnessYellow},
		{"realtime red", "realtime", 2*time.Hour + time.Minute, freshnessRed},

		// hourly: green ≤ 3h, yellow ≤ 12h, red > 12h
		{"hourly fresh", "hourly", time.Hour, freshnessGreen},
		{"hourly green boundary 3h", "hourly", 3 * time.Hour, freshnessGreen},
		{"hourly yellow", "hourly", 6 * time.Hour, freshnessYellow},
		{"hourly yellow boundary 12h", "hourly", 12 * time.Hour, freshnessYellow},
		{"hourly red", "hourly", 13 * time.Hour, freshnessRed},

		// daily: green ≤ 4d, yellow ≤ 7d, red > 7d
		{"daily fresh", "daily", 2 * 24 * time.Hour, freshnessGreen},
		{"daily green boundary 4d", "daily", 4 * 24 * time.Hour, freshnessGreen},
		{"daily yellow", "daily", 5 * 24 * time.Hour, freshnessYellow},
		{"daily yellow boundary 7d", "daily", 7 * 24 * time.Hour, freshnessYellow},
		{"daily red", "daily", 8 * 24 * time.Hour, freshnessRed},

		// weekly: green ≤ 10d, yellow ≤ 14d, red > 14d
		{"weekly fresh", "weekly", 3 * 24 * time.Hour, freshnessGreen},
		{"weekly green boundary 10d", "weekly", 10 * 24 * time.Hour, freshnessGreen},
		{"weekly yellow", "weekly", 12 * 24 * time.Hour, freshnessYellow},
		{"weekly yellow boundary 14d", "weekly", 14 * 24 * time.Hour, freshnessYellow},
		{"weekly red", "weekly", 15 * 24 * time.Hour, freshnessRed},

		// unknown frequency falls back to daily
		{"unknown frequency green by daily", "monthly", 3 * 24 * time.Hour, freshnessGreen},
		{"unknown frequency red by daily", "", 8 * 24 * time.Hour, freshnessRed},

		// zero staleness is always fresh
		{"zero staleness green", "realtime", 0, freshnessGreen},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := freshness(tt.frequency, tt.staleness); got != tt.want {
				t.Errorf("freshness(%q, %v) = %q, want %q", tt.frequency, tt.staleness, got, tt.want)
			}
		})
	}
}
