package detector

import (
	"context"
	"testing"
	"time"

	"capital_observatory/pkg/model"
)

func maRule(config string) model.Rule {
	return model.Rule{
		ID:            88,
		Name:          "ma-test",
		MetricID:      "ma1",
		DetectorName:  "moving_average",
		Severity:      model.SeverityWarning,
		Config:        []byte(config),
		Enabled:       true,
		Version:       1,
		EffectiveFrom: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestMovingAverageDetector_AboveFires(t *testing.T) {
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	// 5-period MA over last 5: (10+10+10+10+50)/5 = 18. latest=50, margin=5
	// 50 ≥ 18+5 = 23 → fire above.
	observations := []model.Observation{
		{MetricUID: "mtr_ma", MetricID: "ma1", Time: base, Value: 10},
		{MetricUID: "mtr_ma", MetricID: "ma1", Time: base.Add(time.Minute), Value: 10},
		{MetricUID: "mtr_ma", MetricID: "ma1", Time: base.Add(2 * time.Minute), Value: 10},
		{MetricUID: "mtr_ma", MetricID: "ma1", Time: base.Add(3 * time.Minute), Value: 10},
		{MetricUID: "mtr_ma", MetricID: "ma1", Time: base.Add(4 * time.Minute), Value: 50},
	}

	trigger, err := MovingAverageDetector{}.Evaluate(context.Background(), maRule(`{"window":5,"margin":5,"above":true}`), observations)
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if trigger == nil {
		t.Fatal("expected trigger (latest 50 >> MA 18 + margin 5), got nil")
	}
	if got := trigger.Evidence["ma"].(float64); got != 18.0 {
		t.Errorf("evidence ma = %v, want 18", got)
	}
	if got := trigger.Evidence["cross"].(float64); got != 32.0 {
		t.Errorf("evidence cross = %v, want 32", got)
	}
}

func TestMovingAverageDetector_BelowFires(t *testing.T) {
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	// 5-period MA: (50+50+50+50+10)/5 = 42. latest=10, margin=5
	// 10 ≤ 42-5 = 37 → fire below.
	observations := []model.Observation{
		{MetricUID: "mtr_ma", MetricID: "ma1", Time: base, Value: 50},
		{MetricUID: "mtr_ma", MetricID: "ma1", Time: base.Add(time.Minute), Value: 50},
		{MetricUID: "mtr_ma", MetricID: "ma1", Time: base.Add(2 * time.Minute), Value: 50},
		{MetricUID: "mtr_ma", MetricID: "ma1", Time: base.Add(3 * time.Minute), Value: 50},
		{MetricUID: "mtr_ma", MetricID: "ma1", Time: base.Add(4 * time.Minute), Value: 10},
	}

	trigger, err := MovingAverageDetector{}.Evaluate(context.Background(), maRule(`{"window":5,"margin":5,"above":false}`), observations)
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if trigger == nil {
		t.Fatal("expected trigger (latest 10 << MA 42 - margin 5), got nil")
	}
}

func TestMovingAverageDetector_NoCrossNoFire(t *testing.T) {
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	// Constant series → MA = 100, latest = 100, margin = 1
	// Above: 100 ≥ 101? No. Below: 100 ≤ 99? No. → no trigger either direction.
	observations := []model.Observation{
		{MetricUID: "mtr_ma", MetricID: "ma1", Time: base, Value: 100},
		{MetricUID: "mtr_ma", MetricID: "ma1", Time: base.Add(time.Minute), Value: 100},
		{MetricUID: "mtr_ma", MetricID: "ma1", Time: base.Add(2 * time.Minute), Value: 100},
	}

	above, err := MovingAverageDetector{}.Evaluate(context.Background(), maRule(`{"window":3,"margin":1,"above":true}`), observations)
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if above != nil {
		t.Error("above: constant series should not cross")
	}

	below, err := MovingAverageDetector{}.Evaluate(context.Background(), maRule(`{"window":3,"margin":1,"above":false}`), observations)
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if below != nil {
		t.Error("below: constant series should not cross")
	}
}

func TestMovingAverageDetector_WindowCappedByData(t *testing.T) {
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	// Only 2 observations, but window asks for 20 → effective window = 2.
	// MA = (10+50)/2 = 30, latest=50, margin=5. 50 ≥ 35 → fire.
	observations := []model.Observation{
		{MetricUID: "mtr_ma", MetricID: "ma1", Time: base, Value: 10},
		{MetricUID: "mtr_ma", MetricID: "ma1", Time: base.Add(time.Minute), Value: 50},
	}

	trigger, err := MovingAverageDetector{}.Evaluate(context.Background(), maRule(`{"window":20,"margin":5,"above":true}`), observations)
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if trigger == nil {
		t.Fatal("expected trigger (capped window, latest 50 >> MA 30 + margin 5)")
	}
	if got := trigger.Evidence["window"].(int); got != 2 {
		t.Errorf("evidence window = %v, want 2 (capped)", got)
	}
}

func TestMovingAverageDetector_InvalidConfig(t *testing.T) {
	_, err := MovingAverageDetector{}.Evaluate(context.Background(), maRule(`{not json`), []model.Observation{
		{MetricUID: "x", Time: time.Now(), Value: 1},
		{MetricUID: "x", Time: time.Now(), Value: 2},
	})
	if err == nil {
		t.Fatal("expected error for invalid config")
	}
}
