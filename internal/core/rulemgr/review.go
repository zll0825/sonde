// Package rulemgr 负责规则评审：插件只能"建议"规则，是否采纳由 Core 按
// 来源优先级裁决（ADR-2）；采纳的规则以新版本行写入 rules 表。
package rulemgr

import (
	"bytes"

	"capital_observatory/pkg/model"
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
	sameConfig := bytes.Equal(currentConfig, suggestedConfig)

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
