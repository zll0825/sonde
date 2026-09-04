package detector

import (
	"context"
	"testing"
	"time"

	"sonde/pkg/model"
)

func trendRule(config string) model.Rule {
	return model.Rule{
		ID:            88,
		Name:          "trend-rule",
		MetricID:      "m1",
		DetectorName:  "trend",
		Severity:      model.SeverityWarning,
		Config:        []byte(config),
		Enabled:       true,
		Version:       1,
		EffectiveFrom: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

// trendVals builds time-ordered observations from values.
func trendVals(vals []float64) []model.Observation {
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	out := make([]model.Observation, 0, len(vals))
	for i, v := range vals {
		out = append(out, obs("mtr_trend", base.Add(time.Duration(i)*time.Hour), v))
	}
	return out
}

func TestTrendDetector_UpStreak(t *testing.T) {
	rule := trendRule(`{"direction":"up","consecutive":4}`)
	// 5 hours of +10 → 4 up-steps.
	trigger, err := TrendDetector{}.Evaluate(context.Background(), rule, trendVals([]float64{100, 110, 120, 130, 140}))
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if trigger == nil {
		t.Fatal("expected trigger for a 4-step uptrend, got nil")
	}
	if trigger.Evidence["direction"] != "up" {
		t.Errorf("evidence direction = %v, want up", trigger.Evidence["direction"])
	}
}

func TestTrendDetector_DownStreak(t *testing.T) {
	rule := trendRule(`{"direction":"down","consecutive":3}`)
	trigger, err := TrendDetector{}.Evaluate(context.Background(), rule, trendVals([]float64{200, 180, 160, 140}))
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if trigger == nil {
		t.Fatal("expected trigger for 3-step downtrend")
	}
}

func TestTrendDetector_AnyDirection(t *testing.T) {
	rule := trendRule(`{"direction":"any","consecutive":3}`)
	// Revert to up — "any" should commit to the first step's direction.
	trigger, err := TrendDetector{}.Evaluate(context.Background(), rule, trendVals([]float64{50, 60, 70, 80}))
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if trigger == nil {
		t.Fatal("expected 'any' mode to fire on a clean uptrend")
	}
	// Evidence must carry the RESOLVED direction, never the literal "any" —
	// an operator reading the alert needs to know which way the metric moved.
	if trigger.Evidence["direction"] != "up" {
		t.Errorf("any-mode uptrend evidence direction = %v, want up", trigger.Evidence["direction"])
	}
	// A downtrend also fires 'any'.
	trigger, err = TrendDetector{}.Evaluate(context.Background(), trendRule(`{"direction":"any","consecutive":3}`), trendVals([]float64{80, 70, 60, 50}))
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if trigger == nil {
		t.Fatal("expected 'any' mode to fire on a clean downtrend")
	}
	if trigger.Evidence["direction"] != "down" {
		t.Errorf("any-mode downtrend evidence direction = %v, want down", trigger.Evidence["direction"])
	}
}

func TestTrendDetector_ReversalResets(t *testing.T) {
	rule := trendRule(`{"direction":"up","consecutive":3}`)
	// U, U, D → streak of 2 < 3.
	trigger, err := TrendDetector{}.Evaluate(context.Background(), rule, trendVals([]float64{10, 20, 30, 25}))
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if trigger != nil {
		t.Error("a reversal should reset the streak; expected no trigger")
	}
}

func TestTrendDetector_FlatStepResets(t *testing.T) {
	rule := trendRule(`{"direction":"up","consecutive":3}`)
	// U, U, flat (equal neighbor), U → still streak of 2 at newest.
	trigger, err := TrendDetector{}.Evaluate(context.Background(), rule, trendVals([]float64{10, 20, 30, 30, 35}))
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if trigger != nil {
		t.Error("a flat step should break the up-streak")
	}
}

func TestTrendDetector_ToleranceIgnoresNoise(t *testing.T) {
	rule := trendRule(`{"direction":"up","consecutive":3,"tolerance":0.01}`)
	// Step of 0.5% < tolerance (1%) → flat → breaks streak.
	// Without tolerance, 10→10.5 is a genuine up-step.
	vals := []float64{100, 100.5, 101, 101.5} // each ~0.5% up
	trigger, err := TrendDetector{}.Evaluate(context.Background(), rule, trendVals(vals))
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	// With tolerance=0.01, all steps < 1% → all flat → no up direction committed.
	if trigger != nil {
		t.Error("all sub-tolerance steps should be flat, not a trend")
	}

	// Same data, tight tolerance → real trend fires.
	tight := trendRule(`{"direction":"up","consecutive":3,"tolerance":0.001}`)
	trigger, err = TrendDetector{}.Evaluate(context.Background(), tight, trendVals(vals))
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if trigger == nil {
		t.Error("with tight tolerance, 0.5% steps should count as up")
	}
}

func TestTrendDetector_NotEnoughData(t *testing.T) {
	rule := trendRule(`{"direction":"up","consecutive":5}`)
	// Only 3 points → at most 2 steps < 5.
	trigger, err := TrendDetector{}.Evaluate(context.Background(), rule, trendVals([]float64{1, 2, 3}))
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if trigger != nil {
		t.Error("fewer observations than consecutive+1 must not fire")
	}
}

func TestTrendDetector_Defaults(t *testing.T) {
	rule := trendRule(`{}`) // direction=any, consecutive=3
	trigger, err := TrendDetector{}.Evaluate(context.Background(), rule, trendVals([]float64{1, 2, 3, 4}))
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if trigger == nil {
		t.Error("default config should fire on 3-step trend")
	}
}

func TestTrendDetector_InvalidConfig(t *testing.T) {
	rule := trendRule(`{broken`)
	_, err := TrendDetector{}.Evaluate(context.Background(), rule, trendVals([]float64{1, 2, 3}))
	if err == nil {
		t.Fatal("expected error for invalid JSON config")
	}
}

func TestTrendDetector_NegativeSeriesDirection(t *testing.T) {
	// Flow metrics are routinely negative (ETF outflows). Rising through
	// negative values (-40 → -10) is an UP trend; dividing by a signed prev
	// would flip the sign and misclassify it as "down" (regression test).
	rule := trendRule(`{"direction":"up","consecutive":3}`)
	trigger, err := TrendDetector{}.Evaluate(context.Background(), rule, trendVals([]float64{-40, -30, -20, -10}))
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if trigger == nil {
		t.Fatal("expected trigger for a rising negative series, got nil")
	}

	// And falling deeper into negative (-10 → -40) is a DOWN trend.
	rule = trendRule(`{"direction":"down","consecutive":3}`)
	trigger, err = TrendDetector{}.Evaluate(context.Background(), rule, trendVals([]float64{-10, -20, -30, -40}))
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if trigger == nil {
		t.Fatal("expected trigger for a falling negative series, got nil")
	}
}

func TestTrendDetector_CrossingZeroUp(t *testing.T) {
	// Outflow → inflow reversal: -20 → -5 → 10 → 25 is one continuous up run.
	rule := trendRule(`{"direction":"up","consecutive":3}`)
	trigger, err := TrendDetector{}.Evaluate(context.Background(), rule, trendVals([]float64{-20, -5, 10, 25}))
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if trigger == nil {
		t.Fatal("expected trigger for an up run crossing zero, got nil")
	}
}
