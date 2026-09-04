package detector

import (
	"context"
	"testing"
	"time"

	"sonde/pkg/model"
)

func volRule(config string) model.Rule {
	return model.Rule{
		ID:            77,
		Name:          "vol-test",
		MetricID:      "v1",
		DetectorName:  "volatility",
		Severity:      model.SeverityWarning,
		Config:        []byte(config),
		Enabled:       true,
		Version:       1,
		EffectiveFrom: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestVolatilityDetector_FiresWhenCVHigh(t *testing.T) {
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	// σ/μ ≈ 0.289 > 0.15 threshold → should fire
	observations := []model.Observation{
		{MetricUID: "mtr_vol", MetricID: "v1", Time: base, Value: 10},
		{MetricUID: "mtr_vol", MetricID: "v1", Time: base.Add(time.Minute), Value: 15},
		{MetricUID: "mtr_vol", MetricID: "v1", Time: base.Add(2 * time.Minute), Value: 5},
		{MetricUID: "mtr_vol", MetricID: "v1", Time: base.Add(3 * time.Minute), Value: 20},
	}

	trigger, err := VolatilityDetector{}.Evaluate(context.Background(), volRule(`{"threshold":0.15}`), observations)
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if trigger == nil {
		t.Fatal("expected trigger (cv≈0.289 > 0.15), got nil")
	}
	if trigger.DetectorName != "volatility" {
		t.Errorf("DetectorName = %q", trigger.DetectorName)
	}
	if got := trigger.Evidence["cv"].(float64); got < 0.15 {
		t.Errorf("evidence cv = %v, should be ≥ 0.15", got)
	}
}

func TestVolatilityDetector_SilentWhenStable(t *testing.T) {
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	// Constant values → cv = 0 → should not fire
	observations := []model.Observation{
		{MetricUID: "mtr_vol", MetricID: "v1", Time: base, Value: 100},
		{MetricUID: "mtr_vol", MetricID: "v1", Time: base.Add(time.Minute), Value: 100},
		{MetricUID: "mtr_vol", MetricID: "v1", Time: base.Add(2 * time.Minute), Value: 100},
	}

	trigger, err := VolatilityDetector{}.Evaluate(context.Background(), volRule(`{"threshold":0.1}`), observations)
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if trigger != nil {
		t.Errorf("expected no trigger for constant series, got %+v", trigger)
	}
}

func TestVolatilityDetector_NeedsAtLeastTwo(t *testing.T) {
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	observations := []model.Observation{
		{MetricUID: "mtr_vol", MetricID: "v1", Time: base, Value: 42},
	}

	trigger, err := VolatilityDetector{}.Evaluate(context.Background(), volRule(`{"threshold":0.05}`), observations)
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if trigger != nil {
		t.Error("single observation must not fire")
	}
}

func TestVolatilityDetector_DefaultThreshold(t *testing.T) {
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	// Uniform values → cv = 0 → even the 0.2 default threshold should not fire.
	observations := []model.Observation{
		{MetricUID: "mtr_vol", MetricID: "v1", Time: base, Value: 50},
		{MetricUID: "mtr_vol", MetricID: "v1", Time: base.Add(time.Minute), Value: 50},
		{MetricUID: "mtr_vol", MetricID: "v1", Time: base.Add(2 * time.Minute), Value: 50},
	}

	trigger, err := VolatilityDetector{}.Evaluate(context.Background(), volRule(`{}`), observations)
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if trigger != nil {
		t.Error("default threshold should not fire on uniform series")
	}
}

func TestVolatilityDetector_InvalidConfig(t *testing.T) {
	_, err := VolatilityDetector{}.Evaluate(context.Background(), volRule(`{not json`), []model.Observation{
		{MetricUID: "x", Time: time.Now(), Value: 1},
		{MetricUID: "x", Time: time.Now(), Value: 2},
	})
	if err == nil {
		t.Fatal("expected error for invalid config")
	}
}
