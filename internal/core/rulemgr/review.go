// Package rulemgr 负责规则评审：插件只能"建议"规则，是否采纳由 Core 按
// 来源优先级裁决（ADR-2）；采纳的规则以新版本行写入 rules 表。
package rulemgr

import (
	"bytes"
	"encoding/json"
	"reflect"

	"sonde/pkg/model"
)

// Source aliases model.RuleSource — the canonical typed enum — so review
// decisions can't drift from the domain model's source vocabulary.
type Source = model.RuleSource

// Source constants re-exported for rulemgr callers. Priority (high → low):
//
//	user_override > system_default > plugin_suggested
const (
	SourcePluginSuggested = model.RuleSourcePluginSuggested
	SourceUserOverride    = model.RuleSourceUserOverride
	SourceSystemDefault   = model.RuleSourceSystemDefault
)

// ReviewOutcome represents the decision when a plugin suggests a new rule version.
type ReviewOutcome struct {
	Action string // "accept" | "pending_conflict" | "skip"
	Reason string
}

// Review evaluates a plugin's new rule suggestion against the current rule state.
//
// Priority: user_override > system_default > plugin_suggested
//
// Decision table (docs/domain-model.md §4.6):
//
//	current=plugin_suggested, same config        → skip
//	current=plugin_suggested, different config   → accept (auto)
//	current=user_override,    same config        → skip (silent)
//	current=user_override,    different config   → pending_conflict
//	current=system_default,   any                → accept (auto)
func Review(current Source, currentConfig, suggestedConfig []byte) ReviewOutcome {
	sameConfig := configsEqual(currentConfig, suggestedConfig)

	switch current {
	case SourcePluginSuggested:
		if sameConfig {
			return ReviewOutcome{
				Action: "skip",
				Reason: "current rule is plugin_suggested with identical config",
			}
		}
		return ReviewOutcome{
			Action: "accept",
			Reason: "current rule is plugin_suggested, accepting new plugin suggestion",
		}

	case SourceUserOverride:
		if sameConfig {
			return ReviewOutcome{
				Action: "skip",
				Reason: "user_override unchanged, silent skip",
			}
		}
		return ReviewOutcome{
			Action: "pending_conflict",
			Reason: "user_override differs from plugin suggestion, requires human decision",
		}

	case SourceSystemDefault:
		return ReviewOutcome{
			Action: "accept",
			Reason: "current rule is system_default, accepting plugin suggestion",
		}

	default:
		// Unknown source — treat like the lowest priority (plugin_suggested)
		// when same config, otherwise accept the suggestion.
		if sameConfig {
			return ReviewOutcome{
				Action: "skip",
				Reason: "unknown source with identical config",
			}
		}
		return ReviewOutcome{
			Action: "accept",
			Reason: "unknown source, accepting plugin suggestion",
		}
	}
}

// configsEqual reports whether two rule configs mean the same thing.
//
// Byte equality is not enough. currentConfig comes back from a Postgres jsonb
// column, which re-renders with its own key order and a space after every
// colon; suggestedConfig is whatever literal the plugin compiled in. A plugin
// declaring `{"direction":"down","consecutive":4,"tolerance":0.002}` reads back
// as `{"direction": "down", "tolerance": 0.002, "consecutive": 4}`. Those
// differ as bytes on essentially every rule, so bytes.Equal made every core
// restart look like a config change: each re-registration retired the current
// row and minted a new version with identical content, 9 rows per stack
// restart, until "rule version" no longer meant "something changed".
//
// Numbers compare by their literal text (json.Decoder.UseNumber), not as
// float64. That can call two spellings of one number different (2e-3 vs 0.002),
// which costs one spurious version — the pre-existing behaviour. float64 would
// do the opposite: call two different integers past 2^53 equal and silently
// swallow a real config change. Fail toward versioning.
func configsEqual(current, suggested []byte) bool {
	if bytes.Equal(current, suggested) {
		return true
	}
	cur, ok := decodeJSONValue(current)
	if !ok {
		return false
	}
	sug, ok := decodeJSONValue(suggested)
	if !ok {
		return false
	}
	return reflect.DeepEqual(cur, sug)
}

// decodeJSONValue decodes exactly one JSON value, keeping numbers as text.
// Anything unparseable or followed by trailing content is reported as
// undecodable so the caller falls back to "changed" rather than guessing.
func decodeJSONValue(raw []byte) (any, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, false
	}
	if dec.More() {
		return nil, false
	}
	return v, true
}

// NeedsVersionForDisplayName is true when Review would skip (identical
// config) but a plugin_suggested row still needs a new version because the
// UI label changed. user_override is excluded: acceptRule would rewrite
// source to plugin_suggested.
func NeedsVersionForDisplayName(current Source, currentDisplayName, suggestedDisplayName string) bool {
	return current == SourcePluginSuggested && currentDisplayName != suggestedDisplayName
}

// NeedsVersionForMode is true when Review would skip (identical config)
// but a plugin_suggested row still needs a new version because mode
// changed. Empty and live compare equal. user_override is excluded:
// acceptRule would rewrite source to plugin_suggested.
func NeedsVersionForMode(current Source, currentMode, suggestedMode string) bool {
	if current != SourcePluginSuggested {
		return false
	}
	cur, err1 := model.ParseRuleMode(currentMode)
	sug, err2 := model.ParseRuleMode(suggestedMode)
	if err1 != nil || err2 != nil {
		// Illegal suggested mode must still acceptRule so registration fails
		// closed instead of silently keeping the current row.
		return true
	}
	return cur != sug
}
