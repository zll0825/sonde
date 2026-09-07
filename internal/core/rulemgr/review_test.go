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

func TestNeedsVersionForDisplayName(t *testing.T) {
	tests := []struct {
		name       string
		source     Source
		current    string
		suggested  string
		wantChange bool
	}{
		{"empty to chinese versions", SourcePluginSuggested, "", "美联储资产负债表连续 4 周收缩", true},
		{"same label skips", SourcePluginSuggested, "美联储资产负债表连续 4 周收缩", "美联储资产负债表连续 4 周收缩", false},
		{"label rewrite versions", SourcePluginSuggested, "old", "new", true},
		{"user override is not rewritten", SourceUserOverride, "", "中文名", false},
		{"system default already accepts via Review", SourceSystemDefault, "", "中文名", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NeedsVersionForDisplayName(tt.source, tt.current, tt.suggested)
			if got != tt.wantChange {
				t.Fatalf("NeedsVersionForDisplayName(%q, %q, %q) = %v, want %v",
					tt.source, tt.current, tt.suggested, got, tt.wantChange)
			}
		})
	}
}

func TestNeedsVersionForMode(t *testing.T) {
	tests := []struct {
		name      string
		source    Source
		current   string
		suggested string
		want      bool
	}{
		{"live to observe versions", SourcePluginSuggested, "live", "observe", true},
		{"empty current to observe versions", SourcePluginSuggested, "", "observe", true},
		{"empty suggested equals live", SourcePluginSuggested, "live", "", false},
		{"empty both is live", SourcePluginSuggested, "", "", false},
		{"LIVE suggested equals live", SourcePluginSuggested, "live", "LIVE", false},
		{"illegal suggested still versions", SourcePluginSuggested, "live", "shadow", true},
		{"same observe skips", SourcePluginSuggested, "observe", "observe", false},
		{"observe back to live versions", SourcePluginSuggested, "observe", "live", true},
		{"user override is not rewritten", SourceUserOverride, "live", "observe", false},
		{"system default already accepts via Review", SourceSystemDefault, "live", "observe", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NeedsVersionForMode(tt.source, tt.current, tt.suggested)
			if got != tt.want {
				t.Fatalf("NeedsVersionForMode(%q, %q, %q) = %v, want %v",
					tt.source, tt.current, tt.suggested, got, tt.want)
			}
		})
	}
}
