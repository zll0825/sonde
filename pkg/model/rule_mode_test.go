package model

import "testing"

func TestNormalizeRuleMode(t *testing.T) {
	for _, tc := range []struct {
		in, want RuleMode
	}{
		{"", RuleModeLive},
		{RuleModeLive, RuleModeLive},
		{RuleModeObserve, RuleModeObserve},
		{"future", "future"},
	} {
		if got := NormalizeRuleMode(tc.in); got != tc.want {
			t.Errorf("NormalizeRuleMode(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestParseRuleMode(t *testing.T) {
	tests := []struct {
		in      string
		want    RuleMode
		wantErr bool
	}{
		{"", RuleModeLive, false},
		{"live", RuleModeLive, false},
		{"LIVE", RuleModeLive, false},
		{" observe ", RuleModeObserve, false},
		{"foo", "", true},
		{"all", "", true},
	}
	for _, tc := range tests {
		got, err := ParseRuleMode(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseRuleMode(%q) err = nil, want error", tc.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseRuleMode(%q) err = %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseRuleMode(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
