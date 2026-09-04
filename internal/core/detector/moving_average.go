package detector

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

	"sonde/pkg/model"
)

// MovingAverageDetector fires when the latest value crosses above or below
// an N-period simple moving average by a configured absolute margin.
//
// Config JSON schema:
//
//		{"window": 20, "margin": 0.0, "above": true}
//
//	  - `window` (optional, default 20): number of most recent observations that
//	    form the SMA. Capped at the number of observations actually received.
//	  - `margin` (optional, default 0.0): absolute gap the latest value must clear
//	    beyond the SMA before firing. Use 0 for pure crossover.
//	  - `above` (optional, default true): direction of the signal.
//	    true  → fire when latest ≥ SMA + margin (bullish/tension crossover).
//	    false → fire when latest ≤ SMA - margin (bearish/relaxation crossover).
//
// At least 1 observation is required; with fewer than `window` points the SMA
// uses whatever is available (a smaller effective window).
type MovingAverageDetector struct{}

func (MovingAverageDetector) Name() string { return "moving_average" }

func (MovingAverageDetector) Evaluate(ctx context.Context, rule model.Rule, obs []model.Observation) (*Trigger, error) {
	var cfg struct {
		Window int     `json:"window"`
		Margin float64 `json:"margin"`
		Above  bool    `json:"above"`
	}
	if len(rule.Config) > 0 {
		if err := json.Unmarshal(rule.Config, &cfg); err != nil {
			return nil, fmt.Errorf("parse moving_average config: %w", err)
		}
	}
	if cfg.Window <= 0 {
		cfg.Window = 20
	}

	n := int(math.Min(float64(cfg.Window), float64(len(obs))))
	if n < 1 {
		return nil, nil
	}

	var sum float64
	for i := len(obs) - n; i < len(obs); i++ {
		sum += obs[i].Value
	}
	ma := sum / float64(n)
	latest := obs[len(obs)-1].Value

	// Decide whether the cross condition is satisfied.
	fired := false
	if cfg.Above {
		fired = latest >= ma+cfg.Margin
	} else {
		fired = latest <= ma-cfg.Margin
	}

	if !fired {
		return nil, nil
	}

	newest := obs[len(obs)-1]
	return &Trigger{
		RuleID:        rule.ID,
		RuleName:      rule.Name,
		MetricID:      rule.MetricID,
		DetectorName:  "moving_average",
		Severity:      rule.Severity,
		WindowStart:   obs[len(obs)-n].Time,
		WindowEnd:     newest.Time,
		RuleVersion:   rule.Version,
		RuleEffective: rule.EffectiveFrom,
		Evidence: map[string]interface{}{
			"latest":     latest,
			"ma":         ma,
			"margin":     cfg.Margin,
			"window":     n,
			"above":      cfg.Above,
			"cross":      latest - ma, // positive = above MA, negative = below
			"metric_uid": newest.MetricUID,
		},
		DedupKey:       ComputeDedupKey(rule.MetricID, rule.ID),
		SourceProvider: newest.SourceProvider,
		SourceClass:    model.NormalizeSourceClass(newest.SourceClass),
	}, nil
}
