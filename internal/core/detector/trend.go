package detector

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"

	"capital_observatory/pkg/model"
)

// TrendDetector fires when the most recent observations form a sustained
// directional run — the metric has been climbing (or falling) for `consecutive`
// steps without reversal. This models PRD §十一's "黄金ETF连续流入15天"
// use case.
//
// Config JSON schema:
//
//		{"direction": "up"|"down"|"any", "consecutive": 5, "tolerance": 0.0}
//
//	  - `direction` (optional, default "any"): which monotonic direction to watch.
//	    "up" = each step strictly greater than its predecessor; "down" = strictly
//	    less; "any" = either, determined from the newest step and held constant.
//	  - `consecutive` (optional, default 3): number of consecutive same-direction
//	    steps required before firing. A single flat point (equal neighbor) resets.
//	  - `tolerance` (optional, default 0.0): fractional slack so near-flat noise
//	    does not break a streak. 0.001 = treat a <0.1% move as flat.
type TrendDetector struct{}

func (TrendDetector) Name() string { return "trend" }

func (TrendDetector) Evaluate(ctx context.Context, rule model.Rule, observations []model.Observation) (*Trigger, error) {
	var cfg trendConfig
	if err := json.Unmarshal(rule.Config, &cfg); err != nil {
		return nil, fmt.Errorf("parse trend config: %w", err)
	}
	if cfg.Consecutive <= 0 {
		cfg.Consecutive = 3
	}
	if cfg.Direction != "up" && cfg.Direction != "down" {
		cfg.Direction = "any"
	}

	if len(observations) < cfg.Consecutive+1 {
		return nil, nil // not enough points to confirm a streak
	}

	// Sort ascending in time.
	sorted := make([]model.Observation, len(observations))
	copy(sorted, observations)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Time.Before(sorted[j].Time) })

	// Walk backwards from the newest, counting same-direction steps.
	expected := cfg.Direction // "up", "down", or "any"
	streak := 0
	var streakStart time.Time
	for i := len(sorted) - 1; i > 0; i-- {
		prev := sorted[i-1].Value
		curr := sorted[i].Value
		stepDir := directionOf(prev, curr, cfg.Tolerance)

		if stepDir == "flat" {
			break
		}
		if expected == "any" {
			expected = stepDir // first step commits the streak direction
		}
		if stepDir != expected {
			break
		}
		streak++
		streakStart = sorted[i-1].Time
		if streak >= cfg.Consecutive {
			// `expected` is the resolved streak direction: with cfg "any" it
			// was committed by the first step, so evidence always carries the
			// actual direction, never the literal "any".
			return buildTrendTrigger(rule, sorted, streakStart, cfg, expected), nil
		}
	}

	return nil, nil
}

type trendConfig struct {
	Direction   string  `json:"direction"`
	Consecutive int     `json:"consecutive"`
	Tolerance   float64 `json:"tolerance"`
}

// directionOf classifies the step from prev → curr as "up", "down", or "flat"
// (iff the relative move is below the tolerance band).
//
// The denominator is |prev|: dividing by a signed prev flips the direction for
// negative series, and flow metrics are routinely negative (ETF outflows).
// prev=-10 → curr=-5 is a rise; (curr-prev)/prev would call it "down".
func directionOf(prev, curr, tolerance float64) string {
	if prev == 0 {
		if curr == 0 {
			return "flat"
		}
		if curr > 0 {
			return "up"
		}
		return "down"
	}
	rel := (curr - prev) / math.Abs(prev)
	if rel > tolerance {
		return "up"
	}
	if rel < -tolerance {
		return "down"
	}
	return "flat"
}

func buildTrendTrigger(rule model.Rule, sorted []model.Observation, streakStart time.Time, cfg trendConfig, direction string) *Trigger {
	newest := sorted[len(sorted)-1]
	return &Trigger{
		RuleID:        rule.ID,
		RuleName:      rule.Name,
		MetricID:      rule.MetricID,
		DetectorName:  "trend",
		Severity:      rule.Severity,
		WindowStart:   streakStart,
		WindowEnd:     newest.Time,
		RuleVersion:   rule.Version,
		RuleEffective: rule.EffectiveFrom,
		Evidence: map[string]interface{}{
			"direction":          direction,
			"consecutive":        cfg.Consecutive,
			"current_value":      newest.Value,
			"window_start_value": valueAtOrBefore(sorted, streakStart),
			"total_steps":        len(sorted),
			"metric_uid":         newest.MetricUID,
		},
		DedupKey: ComputeDedupKey(rule.MetricID, rule.ID),
	}
}

// valueAtOrBefore returns the value at the observation closest to but not after t.
func valueAtOrBefore(sorted []model.Observation, t time.Time) float64 {
	val := sorted[0].Value
	for _, o := range sorted {
		if o.Time.After(t) {
			break
		}
		val = o.Value
	}
	return val
}
