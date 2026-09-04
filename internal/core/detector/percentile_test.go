package detector

import (
	"context"
	"testing"
	"time"

	"sonde/pkg/model"
)

func percentileRule(config string) model.Rule {
	return model.Rule{
		ID:            77,
		Name:          "pctl-rule",
		MetricID:      "m1",
		DetectorName:  "percentile",
		Severity:      model.SeverityWarning,
		Config:        []byte(config),
		Enabled:       true,
		Version:       1,
		EffectiveFrom: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

// sequentialObs builds n observations with values 1..n, 1 minute apart.
func sequentialObs(n int) []model.Observation {
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	out := make([]model.Observation, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, obs("mtr_seq", base.Add(time.Duration(i-1)*time.Minute), float64(i)))
	}
	return out
}

// flatObs builds n observations all with the same value.
func flatObs(n int, value float64) []model.Observation {
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	out := make([]model.Observation, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, obs("mtr_flat", base.Add(time.Duration(i)*time.Minute), value))
	}
	return out
}

// valuesObs builds observations from an explicit value list.
func valuesObs(vals []float64) []model.Observation {
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	out := make([]model.Observation, 0, len(vals))
	for i, v := range vals {
		out = append(out, obs("mtr_seq", base.Add(time.Duration(i)*time.Minute), v))
	}
	return out
}

func TestPercentileDetector_BasicSpike(t *testing.T) {
	rule := percentileRule(`{"percentile":95}`)
	// Values 1..100; newest=100 → way above P95 (~95.05). Should fire.
	trigger, err := PercentileDetector{}.Evaluate(context.Background(), rule, sequentialObs(100))
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if trigger == nil {
		t.Fatal("expected trigger for a value well above P95, got nil")
	}
	if trigger.Evidence["percentile"] != 95.0 {
		t.Errorf("evidence percentile = %v, want 95", trigger.Evidence["percentile"])
	}
}

func TestPercentileDetector_NoSpike(t *testing.T) {
	rule := percentileRule(`{"percentile":99}`)
	// All equal → P99 equals the constant; a value equal to the percentile
	// threshold does not strictly breach it → no fire.
	trigger, err := PercentileDetector{}.Evaluate(context.Background(), rule, flatObs(20, 100))
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if trigger != nil {
		t.Errorf("uniform data should not fire P99, got trigger: %+v", trigger)
	}
}

func TestPercentileDetector_MinObservationsGuard(t *testing.T) {
	rule := percentileRule(`{"percentile":50,"min_observations":10}`)
	trigger, err := PercentileDetector{}.Evaluate(context.Background(), rule, sequentialObs(3))
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if trigger != nil {
		t.Error("expected nil trigger when sample size below min_observations")
	}
}

func TestPercentileDetector_ConsecutiveBreachesNoFire(t *testing.T) {
	rule := percentileRule(`{"percentile":80,"consecutive":3}`)
	// P80 over {1..8, 95, 96}: sorted 1,2,3,4,5,6,7,8,95,96
	// rank = 0.8*9 = 7.2 → 8 + 0.2*(95-8) = 25.4
	// Last=96>25.4, prev=95>25.4, 3rd-to-last=8<25.4 → streak=2 < 3
	trigger, err := PercentileDetector{}.Evaluate(context.Background(), rule, valuesObs([]float64{1, 2, 3, 4, 5, 6, 7, 8, 95, 96}))
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if trigger != nil {
		t.Errorf("only 2 of last 3 breach P80; expected no trigger")
	}
}

func TestPercentileDetector_ConsecutiveStreak(t *testing.T) {
	rule := percentileRule(`{"percentile":50,"consecutive":3}`)
	// 6 values: 1,2,3,4,100,200. P50 rank=0.5*5=2.5 → 3+0.5*(4-3)=3.5
	// Last=200>3.5, prev=100>3.5, next=4>3.5 → streak=3 → fires.
	trigger, err := PercentileDetector{}.Evaluate(context.Background(), rule, valuesObs([]float64{1, 2, 3, 4, 100, 200}))
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if trigger == nil {
		t.Error("expected trigger: last 3 values all breach P50")
	}
}

func TestPercentileDetector_Defaults(t *testing.T) {
	rule := percentileRule(`{}`) // no config → percentile=95, consecutive=1, min_obs=5
	// Newest=20, P95 over 1..20 → rank=0.95*19=18.05 → 19+0.05*(20-19)=19.05
	// 20 > 19.05 → breaches. Fires.
	trigger, err := PercentileDetector{}.Evaluate(context.Background(), rule, sequentialObs(20))
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if trigger == nil {
		t.Error("empty config should use defaults; expected a spike trigger")
	}
}

func TestPercentileDetector_InvalidConfig(t *testing.T) {
	rule := percentileRule(`{broken`)
	_, err := PercentileDetector{}.Evaluate(context.Background(), rule, sequentialObs(10))
	if err == nil {
		t.Fatal("expected error for invalid JSON config")
	}
}

func TestComputePercentile_KnownValues(t *testing.T) {
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	obs := []model.Observation{
		{Time: base, Value: 10},
		{Time: base, Value: 20},
		{Time: base, Value: 30},
		{Time: base, Value: 40},
		{Time: base, Value: 50},
	}
	// P50 over 10..50 → median = 30.
	if got := computePercentile(obs, 50); got != 30 {
		t.Errorf("P50 = %v, want 30", got)
	}
	// P100 → max = 50.
	if got := computePercentile(obs, 100); got != 50 {
		t.Errorf("P100 = %v, want 50", got)
	}
	// P0 → min = 10.
	if got := computePercentile(obs, 0); got != 10 {
		t.Errorf("P0 = %v, want 10", got)
	}
	// P25 → rank=0.25*4=1.0 → exact 20.
	if got := computePercentile(obs, 25); got != 20 {
		t.Errorf("P25 = %v, want 20", got)
	}
	// P75 → rank=0.75*4=3.0 → exact 40.
	if got := computePercentile(obs, 75); got != 40 {
		t.Errorf("P75 = %v, want 40", got)
	}
}
