package detector

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"capital_observatory/pkg/model"
)

// PercentileDetector fires when a metric's latest value exceeds a historical
// percentile computed from the provided observation window. This lets rules
// express "ETF资金流达到历史99%分位" (PRD §十一).
//
// Config JSON schema:
//
//		{"percentile": 95, "consecutive": 1, "min_observations": 10}
//
//	  - `percentile` (required, 0–100): the threshold percentile. 95 means "fire
//	    when the newest value sits at or above the 95th percentile of the sample."
//	  - `consecutive` (optional, default 1): how many of the most recent samples
//	    must breach the percentile before firing. Survives noisy outliers.
//	  - `min_observations` (optional, default 5): refuse to fire with fewer
//	    samples — percentiles over tiny samples are misleading.
//
// The caller is responsible for pushing enough historical observations into the
// engine; with only a few points the detector is effectively inert (limited by
// `min_observations`). This matches PRD §十一's suggestion that detectors
// receive context beyond the latest sample.
type PercentileDetector struct{}

func (PercentileDetector) Name() string { return "percentile" }

func (PercentileDetector) Evaluate(ctx context.Context, rule model.Rule, observations []model.Observation) (*Trigger, error) {
	var cfg percentileConfig
	if err := json.Unmarshal(rule.Config, &cfg); err != nil {
		return nil, fmt.Errorf("parse percentile config: %w", err)
	}
	if cfg.Percentile <= 0 || cfg.Percentile > 100 {
		cfg.Percentile = 95
	}
	if cfg.Consecutive <= 0 {
		cfg.Consecutive = 1
	}
	if cfg.MinObservations <= 0 {
		cfg.MinObservations = 5
	}

	if len(observations) < cfg.MinObservations {
		return nil, nil
	}

	// Sort ascending in time; percentile is computed over the whole sample.
	sorted := make([]model.Observation, len(observations))
	copy(sorted, observations)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Time.Before(sorted[j].Time) })

	threshold := computePercentile(sorted, cfg.Percentile)

	// Walk backwards from the newest; a value at or below the percentile
	// threshold ends the streak. Strictly "above" P<n> is the anomaly signal —
	// a uniform sample at exactly P99 is normal (test case: uniform 100s).
	breaches := 0
	var breachStart time.Time
	for i := len(sorted) - 1; i >= 0; i-- {
		if sorted[i].Value <= threshold {
			break
		}
		breaches++
		breachStart = sorted[i].Time
		if breaches >= cfg.Consecutive {
			return buildPercentileTrigger(rule, sorted, breachStart, cfg, threshold), nil
		}
	}

	return nil, nil
}

type percentileConfig struct {
	Percentile      float64 `json:"percentile"`
	Consecutive     int     `json:"consecutive"`
	MinObservations int     `json:"min_observations"`
}

// computePercentile returns the value at the given percentile (0–100) using
// linear interpolation between the nearest ranks. `sorted` must be time-sorted;
// the function copies values out before ranking so the caller's order is kept.
func computePercentile(sorted []model.Observation, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	values := make([]float64, len(sorted))
	for i, o := range sorted {
		values[i] = o.Value
	}
	sort.Float64s(values)

	if p >= 100 {
		return values[len(values)-1]
	}
	// Rank r = (p/100)*(n-1); interpolate between floor(r) and ceil(r).
	rank := (p / 100) * float64(len(values)-1)
	lower := int(rank)
	upper := lower + 1
	if upper >= len(values) {
		return values[lower]
	}
	frac := rank - float64(lower)
	return values[lower] + frac*(values[upper]-values[lower])
}

func buildPercentileTrigger(rule model.Rule, sorted []model.Observation, windowStart time.Time, cfg percentileConfig, threshold float64) *Trigger {
	newest := sorted[len(sorted)-1]
	return &Trigger{
		RuleID:        rule.ID,
		RuleName:      rule.Name,
		MetricID:      rule.MetricID,
		DetectorName:  "percentile",
		Severity:      rule.Severity,
		WindowStart:   windowStart,
		WindowEnd:     newest.Time,
		RuleVersion:   rule.Version,
		RuleEffective: rule.EffectiveFrom,
		Evidence: map[string]interface{}{
			"current_value":        newest.Value,
			"percentile":           cfg.Percentile,
			"percentile_threshold": threshold,
			"sample_size":          len(sorted),
			"metric_uid":           newest.MetricUID,
		},
		DedupKey:       ComputeDedupKey(rule.MetricID, rule.ID),
		SourceProvider: newest.SourceProvider,
		SourceClass:    model.NormalizeSourceClass(newest.SourceClass),
	}
}
