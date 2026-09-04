package detector

import (
	"context"
	"errors"
	"testing"
	"time"

	"sonde/pkg/model"
)

// stubDetector fires a fixed trigger (or error) regardless of input.
type stubDetector struct {
	name    string
	trigger *Trigger
	err     error
	calls   int
}

func (s *stubDetector) Name() string { return s.name }

func (s *stubDetector) Evaluate(_ context.Context, _ model.Rule, _ []model.Observation) (*Trigger, error) {
	s.calls++
	return s.trigger, s.err
}

func enabledRule(id int, metricID, detectorName string) model.Rule {
	return model.Rule{
		ID:           id,
		Name:         "r",
		MetricID:     metricID,
		DetectorName: detectorName,
		Enabled:      true,
		Config:       []byte(`{}`),
	}
}

func TestEvaluateBatch(t *testing.T) {
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	groups := map[string][]model.Observation{
		"m1": {obs("mtr_1", base, 1)},
		"m2": {obs("mtr_2", base, 2)},
	}

	t.Run("fires matching rules only", func(t *testing.T) {
		fire := &stubDetector{name: "fire", trigger: &Trigger{RuleID: 1}}
		engine := NewEngine(fire)
		rules := []model.Rule{
			enabledRule(1, "m1", "fire"),
			enabledRule(2, "m-unknown", "fire"), // no observations for this metric
		}

		triggers := engine.EvaluateBatch(context.Background(), groups, rules)

		if len(triggers) != 1 {
			t.Fatalf("got %d triggers, want 1", len(triggers))
		}
		if fire.calls != 1 {
			t.Errorf("detector called %d times, want 1", fire.calls)
		}
	})

	t.Run("skips disabled rules", func(t *testing.T) {
		fire := &stubDetector{name: "fire", trigger: &Trigger{RuleID: 1}}
		engine := NewEngine(fire)
		rule := enabledRule(1, "m1", "fire")
		rule.Enabled = false

		triggers := engine.EvaluateBatch(context.Background(), groups, []model.Rule{rule})

		if len(triggers) != 0 {
			t.Errorf("got %d triggers from disabled rule, want 0", len(triggers))
		}
	})

	t.Run("skips unknown detector types", func(t *testing.T) {
		engine := NewEngine()

		triggers := engine.EvaluateBatch(context.Background(), groups, []model.Rule{
			enabledRule(1, "m1", "nonexistent"),
		})

		if len(triggers) != 0 {
			t.Errorf("got %d triggers for unknown detector, want 0", len(triggers))
		}
	})

	t.Run("one failing detector does not stop others", func(t *testing.T) {
		failing := &stubDetector{name: "failing", err: errors.New("boom")}
		fire := &stubDetector{name: "fire", trigger: &Trigger{RuleID: 2}}
		engine := NewEngine(failing, fire)
		rules := []model.Rule{
			enabledRule(1, "m1", "failing"),
			enabledRule(2, "m2", "fire"),
		}

		triggers := engine.EvaluateBatch(context.Background(), groups, rules)

		if len(triggers) != 1 {
			t.Fatalf("got %d triggers, want 1 (failure must not cascade)", len(triggers))
		}
		if triggers[0].RuleID != 2 {
			t.Errorf("surviving trigger RuleID = %d, want 2", triggers[0].RuleID)
		}
	})
}

func TestMergeObservations_Deduplicates(t *testing.T) {
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	a := obs("mtr_1", base, 1)
	b := obs("mtr_1", base.Add(time.Minute), 2)

	merged := mergeObservations([]model.Observation{a, b}, []model.Observation{a, b})

	if len(merged) != 2 {
		t.Errorf("got %d observations after merge, want 2 (duplicates dropped)", len(merged))
	}
}
