package provider

import "time"

// Gap represents a detected gap in a time series.
type Gap struct {
	Start    time.Time
	End      time.Time
	Duration time.Duration
}

// BackfillCoverage records the actual coverage returned by a FRED backfill call,
// including detected gaps so operators can query data quality without parsing logs.
type BackfillCoverage struct {
	MetricID    string    `json:"metric_id"`
	ActualStart time.Time `json:"actual_start"`
	ActualEnd   time.Time `json:"actual_end"`
	SampleCount int       `json:"sample_count"`
	HasGaps     bool      `json:"has_gaps"`
	GapCount    int       `json:"gap_count"`
}

// DetectGaps walks sorted timestamps and flags gaps larger than 2x the expected period.
// Frequency vocabulary and periods come from FrequencyPeriod; unknown frequencies
// keep the historical 30-day fallback.
func DetectGaps(times []time.Time, freq string) []Gap {
	var gaps []Gap
	if len(times) < 2 {
		return nil
	}
	period, ok := FrequencyPeriod(freq)
	if !ok {
		period = 30 * 24 * time.Hour
	}
	for i := 1; i < len(times); i++ {
		gap := times[i].Sub(times[i-1])
		if gap > period*2 {
			gaps = append(gaps, Gap{Start: times[i-1], End: times[i], Duration: gap})
		}
	}
	return gaps
}
