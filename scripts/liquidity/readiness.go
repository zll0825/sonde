package liquidity

// Minimum real observations before a metric may carry an alert rule
// (docs/liquidity-metrics-datasource-plan.md §5). Counts are distinct
// observation periods, not rows: a weekly series re-fetched hourly is still
// one point per week.
var minObservations = map[string]int{
	"daily":   60, // ≈ 3 months
	"weekly":  52, // ≈ 1 year
	"monthly": 36, // ≈ 3 years
}

// Readiness says whether n real observations are enough for a metric of the
// given frequency. Unknown frequencies (hourly, quarterly, realtime) have no
// §5 gate and are reported as not gated.
func Readiness(frequency string, n int) (min int, gated, ready bool) {
	min, gated = minObservations[frequency]
	return min, gated, gated && n >= min
}

// VIXTermStructure is VIX3M / VIX. Below 1 means an inverted curve, which
// is common around earnings and event days — not a stress signal by itself.
func VIXTermStructure(vix3m, vix float64) (float64, error) {
	if vix <= 0 {
		return 0, errNonPositive("VIX", vix)
	}
	return vix3m / vix, nil
}
