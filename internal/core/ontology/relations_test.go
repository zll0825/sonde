package ontology

import (
	"context"
	"testing"
	"time"
)

func TestClampWeight(t *testing.T) {
	cases := []struct {
		input    float64
		expected float64
	}{
		{-0.5, 0},
		{0, 0},
		{0.5, 0.5},
		{1, 1},
		{1.5, 1},
	}
	for _, c := range cases {
		got := clampWeight(c.input)
		if got != c.expected {
			t.Errorf("clampWeight(%v) = %v, want %v", c.input, got, c.expected)
		}
	}
}

func TestPearsonCorrelation_PerfectPositive(t *testing.T) {
	obs := []ObservationPoint{
		{Time: time.Now(), Value: 1},
		{Time: time.Now(), Value: 2},
		{Time: time.Now(), Value: 3},
		{Time: time.Now(), Value: 4},
		{Time: time.Now(), Value: 5},
	}
	corr := pearsonCorrelation(obs, obs)
	if corr < 0.99 {
		t.Errorf("expected ~1.0, got %v", corr)
	}
}

func TestPearsonCorrelation_PerfectNegative(t *testing.T) {
	a := []ObservationPoint{
		{Time: time.Now(), Value: 1},
		{Time: time.Now(), Value: 2},
		{Time: time.Now(), Value: 3},
	}
	b := []ObservationPoint{
		{Time: time.Now(), Value: 3},
		{Time: time.Now(), Value: 2},
		{Time: time.Now(), Value: 1},
	}
	corr := pearsonCorrelation(a, b)
	if corr > -0.99 {
		t.Errorf("expected ~-1.0, got %v", corr)
	}
}

func TestPearsonCorrelation_Empty(t *testing.T) {
	corr := pearsonCorrelation(nil, nil)
	if corr != 0 {
		t.Errorf("expected 0 for empty, got %v", corr)
	}
}

func TestDurationMarshal(t *testing.T) {
	d := Duration{2 * time.Hour}
	b, err := d.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	if string(b) != `"2h0m0s"` {
		t.Errorf("got %s, want %q", string(b), `"2h0m0s"`)
	}
}

func TestCandidateConfig_Defaults(t *testing.T) {
	cfg := DefaultCandidateConfig()
	if cfg.MinCorrelation <= 0 || cfg.MinCorrelation > 1 {
		t.Errorf("MinCorrelation should be 0..1, got %v", cfg.MinCorrelation)
	}
	if cfg.MinObservations < 10 {
		t.Errorf("MinObservations too low: %v", cfg.MinObservations)
	}
}

var _ = context.Background
