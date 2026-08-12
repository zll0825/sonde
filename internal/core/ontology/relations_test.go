package ontology

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type candidateResolverFunc func(context.Context, string) (string, bool, error)

func (f candidateResolverFunc) MetricUIDForEntity(ctx context.Context, entityID string) (string, bool, error) {
	return f(ctx, entityID)
}

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

func TestCreateRejectsInvalidRelationContractBeforeDatabase(t *testing.T) {
	rm := NewRelationManager(nil)
	cases := []RelationInput{
		{SourceID: "same", TargetID: "same", RelationType: "tracks", Direction: "forward"},
		{SourceID: "a", TargetID: "b", RelationType: "supplies", Direction: "forward"},
		{SourceID: "a", TargetID: "b", RelationType: "tracks", Direction: "sideways"},
	}
	for _, input := range cases {
		if _, err := rm.Create(context.Background(), input); err == nil {
			t.Fatalf("Create(%+v) succeeded, want validation error", input)
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

func TestCandidateFinderResolvesEntityIDsToMetricUIDs(t *testing.T) {
	var fetched []string
	finder := NewCandidateFinder(nil, CandidateConfig{
		MinCorrelation:  0.6,
		MinObservations: 2,
		MaxLag:          time.Hour,
		Lookback:        24 * time.Hour,
	})
	finder.SetEntityResolver(candidateResolverFunc(func(_ context.Context, entityID string) (string, bool, error) {
		return "uid_" + entityID, true, nil
	}))
	finder.SetAPI(ObservationAPI{Fetch: func(_ context.Context, uid string, _, _ time.Time, _ int) ([]ObservationPoint, error) {
		fetched = append(fetched, uid)
		return []ObservationPoint{{Time: time.Unix(1, 0), Value: 1}, {Time: time.Unix(2, 0), Value: 2}}, nil
	}})

	if _, err := finder.FindBetween(context.Background(), "source", "target"); err != nil {
		t.Fatalf("FindBetween: %v", err)
	}
	if len(fetched) != 2 || fetched[0] != "uid_source" || fetched[1] != "uid_target" {
		t.Fatalf("fetched metric UIDs = %#v, want uid_source and uid_target", fetched)
	}
}

func TestCandidateFinderRejectsEntityWithoutMetric(t *testing.T) {
	finder := NewCandidateFinder(nil, DefaultCandidateConfig())
	finder.SetEntityResolver(candidateResolverFunc(func(_ context.Context, entityID string) (string, bool, error) {
		if entityID == "source" {
			return "", false, nil
		}
		return "uid_" + entityID, true, nil
	}))
	finder.SetAPI(ObservationAPI{Fetch: func(context.Context, string, time.Time, time.Time, int) ([]ObservationPoint, error) {
		return nil, errors.New("fetch must not run")
	}})

	_, err := finder.FindBetween(context.Background(), "source", "target")
	if err == nil || !strings.Contains(err.Error(), "no current metric for source entity") {
		t.Fatalf("error = %v, want missing source metric", err)
	}
}

var _ = context.Background
