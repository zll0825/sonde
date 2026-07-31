package detector

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"capital_observatory/pkg/model"
)

// ThresholdDetector fires when observations cross a configured boundary.
// Config JSON schema: {"operator": "gt"|"lt"|"gte"|"lte", "value": float, "consecutive": int}
type ThresholdDetector struct{}

func (ThresholdDetector) Name() string { return "threshold" }

func (ThresholdDetector) Evaluate(ctx context.Context, rule model.Rule, observations []model.Observation) (*Trigger, error) {
	var cfg thresholdConfig
	if err := json.Unmarshal(rule.Config, &cfg); err != nil {
		return nil, fmt.Errorf("parse threshold config: %w", err)
	}

	if cfg.Consecutive == 0 {
		cfg.Consecutive = 1
	}
	if cfg.Operator == "" {
		cfg.Operator = "gt"
	}

	// Sort observations by time ascending (assumes caller may pass unsorted).
	sorted := make([]model.Observation, len(observations))
	copy(sorted, observations)
	// Simple bubble for small N; in production use sort.Slice.
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].Time.Before(sorted[i].Time) {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	// Walk from newest backward looking for N consecutive breaches.
	consecutive := 0
	var breachStart time.Time
	for i := len(sorted) - 1; i >= 0; i-- {
		if compare(sorted[i].Value, cfg.Value, cfg.Operator) {
			consecutive++
			breachStart = sorted[i].Time
			if consecutive >= cfg.Consecutive {
				return buildTrigger(rule, sorted, breachStart, cfg), nil
			}
		} else {
			consecutive = 0
		}
	}

	return nil, nil
}

type thresholdConfig struct {
	Operator    string  `json:"operator"`
	Value       float64 `json:"value"`
	Consecutive int     `json:"consecutive"`
}

func compare(value, threshold float64, op string) bool {
	switch op {
	case "gt":
		return value > threshold
	case "gte":
		return value >= threshold
	case "lt":
		return value < threshold
	case "lte":
		return value <= threshold
	default:
		return value > threshold
	}
}

func buildTrigger(rule model.Rule, sorted []model.Observation, windowStart time.Time, cfg thresholdConfig) *Trigger {
	windowEnd := sorted[len(sorted)-1].Time
	return &Trigger{
		RuleID:        rule.ID,
		RuleName:      rule.Name,
		MetricID:      rule.MetricID,
		DetectorName:  "threshold",
		Severity:      rule.Severity,
		WindowStart:   windowStart,
		WindowEnd:     windowEnd,
		RuleVersion:   rule.Version,
		RuleEffective: rule.EffectiveFrom,
		Evidence: map[string]interface{}{
			"operator":    cfg.Operator,
			"threshold":   cfg.Value,
			"consecutive": cfg.Consecutive,
			"breaches":    cfg.Consecutive,
		},
		DedupKey: ComputeDedupKey(rule.MetricID, rule.ID, windowEnd),
	}
}

// _ keep time used
var _ = time.Now
