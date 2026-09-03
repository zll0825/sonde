package handler

import "time"

// Freshness verdicts, computed server-side so the frontend only maps the
// string to a color (single source of truth, design D2).
const (
	freshnessGreen  = "green"
	freshnessYellow = "yellow"
	freshnessRed    = "red"
)

// freshnessThreshold is the staleness window (relative to the metric's
// declared collection frequency) that still counts as green / yellow.
// Anything past yellow is red.
type freshnessThreshold struct {
	green  time.Duration
	yellow time.Duration
}

// freshnessThresholds is the package-level threshold table from design D2.
// Soak-phase calibration lives here, never in the frontend:
//
//	| frequency | green ≤ | yellow ≤ | red > | rationale                       |
//	|-----------|---------|----------|-------|----------------------------------|
//	| realtime  | 30m     | 2h       | 2h    | push-on-collect                  |
//	| hourly    | 3h      | 12h      | 12h   | hourly poll + 2-cycle tolerance  |
//	| daily     | 4d      | 7d       | 7d    | FRED/Yahoo 1–3d lag + weekend gap|
//	| weekly    | 10d     | 14d      | 14d   | WALCL Thu release + holiday drift|
var freshnessThresholds = map[string]freshnessThreshold{
	"realtime": {30 * time.Minute, 2 * time.Hour},
	"hourly":   {3 * time.Hour, 12 * time.Hour},
	"daily":    {4 * 24 * time.Hour, 7 * 24 * time.Hour},
	"weekly":   {10 * 24 * time.Hour, 14 * 24 * time.Hour},
}

// freshness classifies how stale a metric's newest observation is relative to
// its declared collection frequency. Boundary staleness counts as the relaxed
// side: staleness == green threshold is green, == yellow threshold is yellow.
// Unknown frequencies are treated as daily.
func freshness(frequency string, staleness time.Duration) string {
	t, ok := freshnessThresholds[frequency]
	if !ok {
		t = freshnessThresholds["daily"]
	}
	switch {
	case staleness <= t.green:
		return freshnessGreen
	case staleness <= t.yellow:
		return freshnessYellow
	default:
		return freshnessRed
	}
}
