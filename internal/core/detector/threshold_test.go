package detector

import (
	"context"
	"testing"
	"time"

	"sonde/pkg/model"
)

func obs(metricUID string, ts time.Time, value float64) model.Observation {
	return model.Observation{MetricUID: metricUID, MetricID: metricUID, Time: ts, Value: value}
}

func thresholdRule(config string) model.Rule {
	return model.Rule{
		ID:            42,
		Name:          "test-rule",
		MetricID:      "m1",
		DetectorName:  "threshold",
		Severity:      model.SeverityWarning,
		Config:        []byte(config),
		Enabled:       true,
		Version:       1,
		EffectiveFrom: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestThresholdDetector_Operators(t *testing.T) {
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		config   string
		value    float64
		wantFire bool
	}{
		{"gt fires above", `{"operator":"gt","value":100}`, 101, true},
		{"gt silent at equal", `{"operator":"gt","value":100}`, 100, false},
		{"gte fires at equal", `{"operator":"gte","value":100}`, 100, true},
		{"gte silent below", `{"operator":"gte","value":100}`, 99.9, false},
		{"lt fires below", `{"operator":"lt","value":100}`, 99, true},
		{"lt silent at equal", `{"operator":"lt","value":100}`, 100, false},
		{"lte fires at equal", `{"operator":"lte","value":100}`, 100, true},
		{"lte silent above", `{"operator":"lte","value":100}`, 100.1, false},
		{"default operator is gt", `{"value":100}`, 101, true},
		{"unknown operator falls back to gt", `{"operator":"weird","value":100}`, 101, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := thresholdRule(tt.config)
			observations := []model.Observation{obs("mtr_a", base, tt.value)}

			trigger, err := ThresholdDetector{}.Evaluate(context.Background(), rule, observations)

			if err != nil {
				t.Fatalf("Evaluate returned error: %v", err)
			}
			if (trigger != nil) != tt.wantFire {
				t.Errorf("fired = %v, want %v", trigger != nil, tt.wantFire)
			}
		})
	}
}

func TestThresholdDetector_ConsecutiveBreaches(t *testing.T) {
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	config := `{"operator":"gt","value":100,"consecutive":3}`

	tests := []struct {
		name     string
		values   []float64 // oldest → newest
		wantFire bool
	}{
		{"three consecutive breaches fire", []float64{101, 102, 103}, true},
		{"two breaches insufficient", []float64{99, 101, 102}, false},
		{"gap resets the streak", []float64{101, 99, 101, 102}, false},
		{"streak must end at the newest side", []float64{101, 102, 103, 99}, false},
		{"longer history still fires", []float64{99, 99, 101, 102, 103}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := thresholdRule(config)
			observations := make([]model.Observation, 0, len(tt.values))
			for i, v := range tt.values {
				observations = append(observations, obs("mtr_a", base.Add(time.Duration(i)*time.Minute), v))
			}

			trigger, err := ThresholdDetector{}.Evaluate(context.Background(), rule, observations)

			if err != nil {
				t.Fatalf("Evaluate returned error: %v", err)
			}
			if (trigger != nil) != tt.wantFire {
				t.Errorf("fired = %v, want %v", trigger != nil, tt.wantFire)
			}
		})
	}
}

func TestThresholdDetector_SortsUnsortedInput(t *testing.T) {
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	rule := thresholdRule(`{"operator":"gt","value":100,"consecutive":2}`)
	// Newest-first input: the detector must sort before walking the streak.
	observations := []model.Observation{
		obs("mtr_a", base.Add(2*time.Minute), 103),
		obs("mtr_a", base, 99),
		obs("mtr_a", base.Add(time.Minute), 102),
	}

	trigger, err := ThresholdDetector{}.Evaluate(context.Background(), rule, observations)

	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if trigger == nil {
		t.Fatal("expected trigger, got nil")
	}
	if !trigger.WindowEnd.Equal(base.Add(2 * time.Minute)) {
		t.Errorf("WindowEnd = %v, want newest observation time %v", trigger.WindowEnd, base.Add(2*time.Minute))
	}
	if !trigger.WindowStart.Equal(base.Add(time.Minute)) {
		t.Errorf("WindowStart = %v, want start of breach run %v", trigger.WindowStart, base.Add(time.Minute))
	}
}

func TestThresholdDetector_EvidenceCarriesResearchFields(t *testing.T) {
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	rule := thresholdRule(`{"operator":"gt","value":100}`)
	observations := []model.Observation{obs("mtr_a", base, 123.45)}

	trigger, err := ThresholdDetector{}.Evaluate(context.Background(), rule, observations)

	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if trigger == nil {
		t.Fatal("expected trigger, got nil")
	}
	// research.Assembler reads current_value/threshold from alert evidence;
	// the detector must supply them or research context loses its numbers.
	if got := trigger.Evidence["current_value"]; got != 123.45 {
		t.Errorf("evidence current_value = %v, want 123.45", got)
	}
	if got := trigger.Evidence["threshold"]; got != 100.0 {
		t.Errorf("evidence threshold = %v, want 100", got)
	}
	if got := trigger.Evidence["metric_uid"]; got != "mtr_a" {
		t.Errorf("evidence metric_uid = %v, want mtr_a", got)
	}
}

func TestThresholdDetector_FreezesNewestObservationProvenance(t *testing.T) {
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	older := obs("mtr_a", base, 101)
	older.SourceProvider = "older-provider"
	older.SourceClass = model.SourceClassMock
	newest := obs("mtr_a", base.Add(time.Minute), 102)
	newest.SourceProvider = "upstream"
	newest.SourceClass = model.SourceClassReal

	trigger, err := ThresholdDetector{}.Evaluate(context.Background(), thresholdRule(`{"operator":"gt","value":100,"consecutive":2}`), []model.Observation{newest, older})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if trigger == nil {
		t.Fatal("expected trigger")
	}
	if trigger.SourceProvider != "upstream" || trigger.SourceClass != model.SourceClassReal {
		t.Errorf("provenance = %q/%q, want upstream/real", trigger.SourceProvider, trigger.SourceClass)
	}
}

func TestThresholdDetector_InvalidConfig(t *testing.T) {
	rule := thresholdRule(`{not json`)

	_, err := ThresholdDetector{}.Evaluate(context.Background(), rule, []model.Observation{
		obs("mtr_a", time.Now(), 1),
	})

	if err == nil {
		t.Fatal("expected error for invalid config, got nil")
	}
}

func TestComputeDedupKey_StableAcrossWindows(t *testing.T) {
	// The dedup key must not change between evaluation batches, otherwise
	// active-alert dedup (partial unique index WHERE status='active') never fires.
	first := ComputeDedupKey("m1", 42)
	second := ComputeDedupKey("m1", 42)
	other := ComputeDedupKey("m1", 43)

	if first != second {
		t.Errorf("dedup key unstable: %q != %q", first, second)
	}
	if first == other {
		t.Errorf("different rules must produce different keys, both %q", first)
	}
}
