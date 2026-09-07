package pluginmgr

import (
	"strings"
	"testing"
	"time"

	"sonde/internal/core/detector"
	"sonde/pkg/model"
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
		SourceProvider: "yahoo_finance",
		SourceClass:    model.SourceClassReal,
	}

	a := triggerToAlert(trigger, "plg_etf")

	// ID is unique per alert row (alerts.id is the PK); dedup is the job of
	// dedup_key + the partial unique index, never the ID.
	if !strings.HasPrefix(a.ID, "alt_") {
		t.Errorf("alert.ID = %q, want alt_ prefix", a.ID)
	}
	if second := triggerToAlert(trigger, "plg_etf"); second.ID == a.ID {
		t.Errorf("two alerts share ID %q; a resolved-then-refired alert would collide on the PK", a.ID)
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
	if a.SourceProvider != "yahoo_finance" || a.SourceClass != model.SourceClassReal {
		t.Errorf("alert provenance = %q/%q, want yahoo_finance/real", a.SourceProvider, a.SourceClass)
	}
	if a.Mode != model.RuleModeLive {
		t.Errorf("alert.Mode = %q, want live for empty trigger mode", a.Mode)
	}
}

func TestTriggerToAlert_CopiesMode(t *testing.T) {
	a := triggerToAlert(&detector.Trigger{
		RuleName: "x", MetricID: "m", DedupKey: "k", Mode: model.RuleModeObserve,
	}, "p1")
	if a.Mode != model.RuleModeObserve {
		t.Errorf("mode = %q, want observe", a.Mode)
	}
}

func TestTriggerToAlert_NormalizesMissingSourceClass(t *testing.T) {
	a := triggerToAlert(&detector.Trigger{RuleName: "x", MetricID: "m", DedupKey: "k", SourceClass: "future"}, "p1")
	if a.SourceClass != model.SourceClassUnknown {
		t.Errorf("source class = %q, want unknown", a.SourceClass)
	}
}

// TestTriggerToAlert_SeverityMapping checks that proto Severity is correctly
// mapped through to model.Severity.
func TestTriggerToAlert_SeverityMapping(t *testing.T) {
	cases := []struct {
		name    string
		sev     model.Severity
		wantSev model.Severity
	}{
		{"warning", model.SeverityWarning, model.SeverityWarning},
		{"critical", model.SeverityCritical, model.SeverityCritical},
		{"info", model.SeverityInfo, model.SeverityInfo},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			trigger := &detector.Trigger{
				RuleName: "x",
				MetricID: "m",
				DedupKey: "k",
				Severity: tc.sev,
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

func TestFrequencyAwareLookback(t *testing.T) {
	mkRule := func(config string) model.Rule {
		return model.Rule{Config: []byte(config), Enabled: true}
	}
	day := 24 * time.Hour

	cases := []struct {
		name  string
		freq  string
		rules []model.Rule
		want  time.Duration
	}{
		{
			name:  "weekly trend consecutive 4 needs 5 periods plus headroom",
			freq:  "weekly",
			rules: []model.Rule{mkRule(`{"direction":"down","consecutive":4}`)},
			want:  time.Duration(5 * 1.5 * float64(7*day)),
		},
		{
			name:  "percentile min_observations dominates consecutive",
			freq:  "weekly",
			rules: []model.Rule{mkRule(`{"percentile":95,"consecutive":1,"min_observations":8}`)},
			want:  time.Duration(8 * 1.5 * float64(7*day)),
		},
		{
			name:  "empty config floors at 5 periods for percentile default min_observations",
			freq:  "weekly",
			rules: []model.Rule{mkRule(`{}`)},
			want:  time.Duration(5 * 1.5 * float64(7*day)),
		},
		{
			name:  "unknown frequency falls back to 7d",
			freq:  "fortnightly",
			rules: []model.Rule{mkRule(`{"consecutive":10}`)},
			want:  7 * day,
		},
		{
			name:  "realtime small requirement floors at 7d",
			freq:  "realtime",
			rules: []model.Rule{mkRule(`{"consecutive":2}`)},
			want:  7 * day,
		},
		{
			name:  "quarterly pathological config caps at 365d",
			freq:  "quarterly",
			rules: []model.Rule{mkRule(`{"consecutive":10}`)},
			want:  365 * day,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := frequencyAwareLookback(tc.freq, tc.rules)
			if got != tc.want {
				t.Errorf("frequencyAwareLookback(%q) = %v, want %v", tc.freq, got, tc.want)
			}
		})
	}
}

func TestObservationsLimit(t *testing.T) {
	day := 24 * time.Hour

	cases := []struct {
		name     string
		lookback time.Duration
		freq     string
		want     int
	}{
		{"unknown frequency uses safe default", 7 * day, "fortnightly", 500},
		{"realtime wide window caps at 500", 7 * day, "realtime", 500},
		{"weekly window floors at 20", 53 * day, "weekly", 20},
		{"daily window floors at 20", 8 * day, "daily", 20},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := observationsLimit(tc.lookback, tc.freq)
			if got != tc.want {
				t.Errorf("observationsLimit(%v, %q) = %d, want %d", tc.lookback, tc.freq, got, tc.want)
			}
		})
	}
}
