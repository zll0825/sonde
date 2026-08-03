// Package relationmgr 维护关系分类学注册表与分层评审逻辑：插件只能"建议"
// 关系，是否采纳由 Core 按来源优先级裁决（ADR-2）。
package relationmgr

import "capital_observatory/pkg/model"

// relationTypeLayer maps a relation_type string to its taxonomy layer.
// This is a hard-coded registry — plugin suggestions are advisory only;
// the layer is always derived from this table, never from plugin input.
var relationTypeLayer = map[string]model.RelationLayer{
	// structural
	"tracks":       model.RelationLayerStructural,
	"component_of": model.RelationLayerStructural,
	"issued_by":    model.RelationLayerStructural,
	"belongs_to":   model.RelationLayerStructural,
	// semantic
	"hedges":   model.RelationLayerSemantic,
	"competes": model.RelationLayerSemantic,
	"signals":  model.RelationLayerSemantic,
	// statistical
	"correlates":           model.RelationLayerStatistical,
	"inversely_correlates": model.RelationLayerStatistical,
	"leads":                model.RelationLayerStatistical,
	"lags":                 model.RelationLayerStatistical,
	// causal
	"causes":            model.RelationLayerCausal,
	"depends_on_regime": model.RelationLayerCausal,
}

// LayerOf returns the taxonomy layer for a given relation_type.
// Returns false if the relation_type is unknown to the registry.
func LayerOf(relationType string) (model.RelationLayer, bool) {
	layer, ok := relationTypeLayer[relationType]
	return layer, ok
}

// Layer is an alias for model.RelationLayer kept for callers that prefer
// the relationmgr namespace. The canonical definition lives in pkg/model.
type Layer = model.RelationLayer

// ReviewDecision represents the outcome of reviewing a relation suggestion.
type ReviewDecision struct {
	Decision string // "accepted" | "auto_accepted" | "rejected" | "pending"
	Reason   string
}

// Review applies the layer-based review strategy per ADR-2 and
// docs/domain-model.md §3.3.
//
//	structural   → auto-accept
//	semantic     → auto-accept with source=plugin_declared
//	statistical  → require evidence (p_value <= 0.05, sample_size >= 30)
//	causal       → always pending (needs human review)
func Review(layer model.RelationLayer, hasEvidence bool, pValue float64, sampleSize int) ReviewDecision {
	switch layer {
	case model.RelationLayerStructural:
		return ReviewDecision{
			Decision: "auto_accepted",
			Reason:   "structural relation: auto-accepted by registry",
		}

	case model.RelationLayerSemantic:
		return ReviewDecision{
			Decision: "auto_accepted",
			Reason:   "semantic relation: auto-accepted, source=plugin_declared",
		}

	case model.RelationLayerStatistical:
		if !hasEvidence {
			return ReviewDecision{
				Decision: "rejected",
				Reason:   "statistical relation requires evidence, none provided",
			}
		}
		if pValue > 0.05 {
			return ReviewDecision{
				Decision: "rejected",
				Reason:   "statistical relation requires p_value <= 0.05",
			}
		}
		if sampleSize < 30 {
			return ReviewDecision{
				Decision: "rejected",
				Reason:   "statistical relation requires sample_size >= 30",
			}
		}
		return ReviewDecision{
			Decision: "accepted",
			Reason:   "statistical relation meets evidence thresholds",
		}

	case model.RelationLayerCausal:
		return ReviewDecision{
			Decision: "pending",
			Reason:   "causal relation requires human review",
		}

	default:
		return ReviewDecision{
			Decision: "rejected",
			Reason:   "unknown relation layer: cannot review",
		}
	}
}
