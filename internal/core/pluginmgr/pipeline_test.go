package pluginmgr

import (
	"strings"
	"testing"
	"time"

	"capital_observatory/internal/core/detector"
	"capital_observatory/pkg/model"
)

// TestTriggerToAlert_BuildsPureAlert validates the triggerToAlert
// pure-conversion function. This is the only fully unit-testable function in
// the pipeline package; the full EvaluateAndAlert flow requires a live DB
// (integration tests live in internal/core/ontology/integration_test.go).
func TestTriggerToAlert_BuildsPureAlert(t *testing.T) {
	now := time.Now()
	trigger := &detector.Trigger{
		RuleID:         42,
		RuleName:       "gld_flow_spike",
		MetricID:       "gld_flow",
		RuleEffective:  now,
		WindowStart:    now.Add(-1 * time.Minute),
		WindowEnd:      now,
		Severity:       model.SeverityWarning,
		DetectorName:   "threshold",
		DedupKey:       "rule_42:gld_flow:5m",
		Evidence:       map[string]interface{}{"value": 722.0},
	}

	a := triggerToAlert(trigger, "plg_etf")

	// ID is derived from dedup_key → idempotent across retries.
	if a.ID != "alt_rule_42:gld_flow:5m" {
		t.Errorf("alert.ID = %q, want alt_<dedup_key>", a.ID)
	}
	if a.RuleID != 42 {
		t.Errorf("alert.RuleID = %d, want 42", a.RuleID)
	}
	if a.Title != "gld_flow_spike" {
		t.Errorf("alert.Title = %q", a.Title)
	}
	if a.Severity != model.SeverityWarning {
		t.Errorf("alert.Severity = %q", a.Severity)
	}
	if a.Status != "active" {
		t.Errorf("alert.Status = %q, want active", a.Status)
	}
	if a.PluginID != "plg_etf" {
		t.Errorf("alert.PluginID = %q, want plg_etf", a.PluginID)
	}
	if a.MetricID != "gld_flow" {
		t.Errorf("alert.MetricID = %q, want gld_flow", a.MetricID)
	}
	if a.DedupKey != "rule_42:gld_flow:5m" {
		t.Errorf("alert.DedupKey = %q", a.DedupKey)
	}
}

// TestTriggerToAlert_SeverityMapping checks that proto Severity is correctly
// mapped through to model.Severity.
func TestTriggerToAlert_SeverityMapping(t *testing.T) {
	cases := []struct {
		name     string
		sev      model.Severity
		wantSev  model.Severity
	}{
		{"warning", model.SeverityWarning, model.SeverityWarning},
		{"critical", model.SeverityCritical, model.SeverityCritical},
		{"info", model.SeverityInfo, model.SeverityInfo},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			trigger := &detector.Trigger{
				RuleName:  "x",
				MetricID:  "m",
				DedupKey:  "k",
				Severity:  tc.sev,
			}
			a := triggerToAlert(trigger, "p1")
			if a.Severity != tc.wantSev {
				t.Errorf("severity = %q, want %q", a.Severity, tc.wantSev)
			}
		})
	}
}

// TestTriggerToAlert_EvidenceContainsTriggerFields verifies that the
// EvidenceJSON helper serializes evidence such that it contains the value.
func TestTriggerToAlert_EvidenceContainsTriggerFields(t *testing.T) {
	trigger := &detector.Trigger{
		RuleName: "spike",
		MetricID: "m1",
		DedupKey: "k1",
		Evidence: map[string]interface{}{"current_value": 900.0},
	}
	a := triggerToAlert(trigger, "plg_test")
	if len(a.Evidence) == 0 {
		t.Fatal("Evidence should not be empty")
	}
	if !strings.Contains(string(a.Evidence), "current_value") {
		t.Errorf("Evidence = %s, want to contain 'current_value'", string(a.Evidence))
	}
}
