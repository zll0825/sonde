package rulemgr

import "testing"

// TestReview covers the source × suggestion decision table from
// docs/domain-model.md §4.6.
func TestReview(t *testing.T) {
	configA := []byte(`{"operator":"gt","value":100}`)
	configB := []byte(`{"operator":"gt","value":200}`)

	tests := []struct {
		name            string
		current         Source
		currentConfig   []byte
		suggestedConfig []byte
		wantAction      string
	}{
		{"plugin_suggested same config skips", SourcePluginSuggested, configA, configA, "skip"},
		{"plugin_suggested new config accepts", SourcePluginSuggested, configA, configB, "accept"},
		{"user_override same config silent skip", SourceUserOverride, configA, configA, "skip"},
		{"user_override different config conflicts", SourceUserOverride, configA, configB, "pending_conflict"},
		{"system_default same config accepts", SourceSystemDefault, configA, configA, "accept"},
		{"system_default different config accepts", SourceSystemDefault, configA, configB, "accept"},
		{"unknown source same config skips", Source("mystery"), configA, configA, "skip"},
		{"unknown source different config accepts", Source("mystery"), configA, configB, "accept"},
		{"nil configs compare equal", SourcePluginSuggested, nil, nil, "skip"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Review(tt.current, tt.currentConfig, tt.suggestedConfig)

			if got.Action != tt.wantAction {
				t.Errorf("Review(%q) action = %q, want %q", tt.current, got.Action, tt.wantAction)
			}
			if got.Reason == "" {
				t.Error("Review returned empty Reason; every decision must be explainable")
			}
		})
	}
}
