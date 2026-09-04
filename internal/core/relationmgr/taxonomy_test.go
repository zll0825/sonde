package relationmgr

import (
	"testing"

	"sonde/pkg/model"
)

func TestLayerOf(t *testing.T) {
	tests := []struct {
		relationType string
		wantLayer    model.RelationLayer
		wantKnown    bool
	}{
		// structural
		{"tracks", model.RelationLayerStructural, true},
		{"component_of", model.RelationLayerStructural, true},
		{"issued_by", model.RelationLayerStructural, true},
		{"belongs_to", model.RelationLayerStructural, true},
		// semantic
		{"hedges", model.RelationLayerSemantic, true},
		{"competes", model.RelationLayerSemantic, true},
		{"signals", model.RelationLayerSemantic, true},
		// statistical
		{"correlates", model.RelationLayerStatistical, true},
		{"inversely_correlates", model.RelationLayerStatistical, true},
		{"leads", model.RelationLayerStatistical, true},
		{"lags", model.RelationLayerStatistical, true},
		// causal
		{"causes", model.RelationLayerCausal, true},
		{"depends_on_regime", model.RelationLayerCausal, true},
		// Legacy pre-v1 spelling must stay outside the registry. It may be
		// quarantined as a suggestion, but it must never be accepted as a
		// canonical relation.
		{"influences", "", false},
		// unknown types are not in the registry
		{"made_up_type", "", false},
		{"", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.relationType, func(t *testing.T) {
			layer, known := LayerOf(tt.relationType)

			if known != tt.wantKnown {
				t.Fatalf("LayerOf(%q) known = %v, want %v", tt.relationType, known, tt.wantKnown)
			}
			if known && layer != tt.wantLayer {
				t.Errorf("LayerOf(%q) layer = %q, want %q", tt.relationType, layer, tt.wantLayer)
			}
		})
	}
}

// TestReview covers the layer-based review decision table from
// docs/domain-model.md §3.3.
func TestReview(t *testing.T) {
	tests := []struct {
		name         string
		layer        model.RelationLayer
		hasEvidence  bool
		pValue       float64
		sampleSize   int
		wantDecision string
	}{
		{"structural auto-accepted", model.RelationLayerStructural, false, 0, 0, "auto_accepted"},
		{"structural ignores evidence", model.RelationLayerStructural, true, 0.9, 1, "auto_accepted"},
		{"semantic auto-accepted", model.RelationLayerSemantic, false, 0, 0, "auto_accepted"},
		{"statistical without evidence rejected", model.RelationLayerStatistical, false, 0, 0, "rejected"},
		{"statistical p-value too high rejected", model.RelationLayerStatistical, true, 0.06, 100, "rejected"},
		{"statistical sample too small rejected", model.RelationLayerStatistical, true, 0.01, 29, "rejected"},
		{"statistical meets thresholds accepted", model.RelationLayerStatistical, true, 0.05, 30, "accepted"},
		{"statistical strong evidence accepted", model.RelationLayerStatistical, true, 0.001, 500, "accepted"},
		{"causal always pending", model.RelationLayerCausal, true, 0.001, 500, "pending"},
		{"unknown layer rejected", model.RelationLayer("mystery"), true, 0.01, 100, "rejected"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Review(tt.layer, tt.hasEvidence, tt.pValue, tt.sampleSize)

			if got.Decision != tt.wantDecision {
				t.Errorf("Review(%q, %v, %v, %d) = %q, want %q",
					tt.layer, tt.hasEvidence, tt.pValue, tt.sampleSize, got.Decision, tt.wantDecision)
			}
			if got.Reason == "" {
				t.Error("Review returned empty Reason; every decision must be explainable")
			}
		})
	}
}
