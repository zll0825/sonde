package detector

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

	"sonde/pkg/model"
)

// VolatilityDetector fires when the coefficient of variation (stddev/mean)
// exceeds a configured high-water mark. Volatile series indicate instability
// worth surfacing even if no individual observation crosses a threshold.
//
// Config JSON schema:
//
//		{"threshold": 0.2}
//
//	  - `threshold` (optional, default 0.2): minimum coefficient of variation
//	    (σ/|μ|) required to fire. Below this the series is considered stable.
//
// The detector requires ≥ 2 observations. A zero or near-zero mean disables
// the signal (division by a tiny mean inflates cv without bound); in that
// case it fires directly when the stddev itself is non-trivial.
type VolatilityDetector struct{}

func (VolatilityDetector) Name() string { return "volatility" }

func (VolatilityDetector) Evaluate(ctx context.Context, rule model.Rule, obs []model.Observation) (*Trigger, error) {
	var cfg struct {
		Threshold float64 `json:"threshold"`
	}
	if len(rule.Config) > 0 {
		if err := json.Unmarshal(rule.Config, &cfg); err != nil {
			return nil, fmt.Errorf("parse volatility config: %w", err)
		}
	}
	if cfg.Threshold <= 0 {
		cfg.Threshold = 0.2
	}

	n := len(obs)
	if n < 2 {
		return nil, nil
	}

	var sum, sumSq float64
	for _, o := range obs {
		sum += o.Value
		sumSq += o.Value * o.Value
	}
	mean := sum / float64(n)
	variance := sumSq/float64(n) - mean*mean
	if variance < 0 {
		variance = 0
	}
	stddev := math.Sqrt(variance)

	var cv float64
	if mean != 0 {
		cv = stddev / math.Abs(mean)
	} else {
		// Zero-mean series: σ itself is the signal. Mirror the threshold as an
		// absolute stddev so the config stays in familiar "fractional" units
		// for non-zero-mean cases. 10% threshold → fire when σ ≥ 0.10.
		cv = stddev
	}

	if cv >= cfg.Threshold {
		return &Trigger{
			RuleID:        rule.ID,
			RuleName:      rule.Name,
			MetricID:      rule.MetricID,
			DetectorName:  "volatility",
			Severity:      rule.Severity,
			WindowStart:   obs[0].Time,
			WindowEnd:     obs[n-1].Time,
			RuleVersion:   rule.Version,
			RuleEffective: rule.EffectiveFrom,
			Evidence: map[string]interface{}{
				"cv":         cv,
				"threshold":  cfg.Threshold,
				"mean":       mean,
				"stddev":     stddev,
				"samples":    n,
				"metric_uid": obs[n-1].MetricUID,
			},
			DedupKey:       ComputeDedupKey(rule.MetricID, rule.ID),
			SourceProvider: obs[n-1].SourceProvider,
			SourceClass:    model.NormalizeSourceClass(obs[n-1].SourceClass),
		}, nil
	}
	return nil, nil
}
