// Package research assembles the research context for a given alert.
// ResearchContext = metric + entities + relations + recent observations.
package research

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"

	"capital_observatory/pkg/model"
)

// ResearchContext is the assembled research payload for the API / frontend.
type ResearchContext struct {
	AlertID         string                 `json:"alert_id"`
	MetricID        string                 `json:"metric_id"`
	MetricName      string                 `json:"metric_name"`
	WindowStart     time.Time              `json:"window_start"`
	WindowEnd       time.Time              `json:"window_end"`
	CurrentValue    float64                `json:"current_value"`
	Threshold       float64                `json:"threshold,omitempty"`
	RelatedEntities []EntityRef            `json:"related_entities"`
	Relations       []RelationRef          `json:"relations"`
	RecentTrend     []TrendPoint           `json:"recent_trend"`
	Metadata        map[string]interface{} `json:"metadata"`
	AssembledAt     time.Time              `json:"assembled_at"`
}

// EntityRef is a lightweight entity reference in research context.
type EntityRef struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Namespace  string   `json:"namespace"`
	EntityType string   `json:"entity_type"`
	Tags       []string `json:"tags,omitempty"`
}

// RelationRef is a lightweight relation in research context.
type RelationRef struct {
	SourceID     string `json:"source_id"`
	TargetID     string `json:"target_id"`
	RelationType string `json:"relation_type"`
	Direction    string `json:"direction"`
	Description  string `json:"description,omitempty"`
}

// TrendPoint is one observation in the recent trend window.
type TrendPoint struct {
	Time  time.Time `json:"time"`
	Value float64   `json:"value"`
}

// Assembler builds research contexts by traversing the entity-relation graph.
type Assembler struct {
	store ResearchStore
}

// ResearchStore is the persistence interface for research assembly.
type ResearchStore interface {
	GetObservations(ctx context.Context, metricUID string, since, until time.Time, limit int) ([]model.Observation, error)
	GetEntityByID(ctx context.Context, entityID string) (*model.Entity, error)
	GetRelatedEntities(ctx context.Context, entityID string) ([]model.Relation, error)
	SaveSnapshot(ctx context.Context, snapshot model.ResearchSnapshot) error
}

// NewAssembler creates a new research context assembler.
func NewAssembler(store ResearchStore) *Assembler {
	return &Assembler{store: store}
}

// Assemble builds the full research context for an alert.
func (a *Assembler) Assemble(ctx context.Context, alert model.Alert) (*ResearchContext, error) {
	out := &ResearchContext{
		AlertID:     alert.ID,
		MetricID:    alert.MetricID,
		WindowStart: derefTime(alert.WindowStart),
		WindowEnd:   derefTime(alert.WindowEnd),
		Metadata:    make(map[string]interface{}),
		AssembledAt: time.Now(),
	}

	// Decode evidence for current value / threshold.
	var evidence map[string]interface{}
	if len(alert.Evidence) > 0 {
		_ = json.Unmarshal(alert.Evidence, &evidence)
		if v, ok := evidence["current_value"]; ok {
			out.CurrentValue = toFloat(v)
		}
		if v, ok := evidence["threshold"]; ok {
			out.Threshold = toFloat(v)
		}
	}

	// Fetch recent trend (7-day lookback from WindowEnd). Research context uses
	// a fixed, narrow window — a single page of trend points is ample.
	//
	// Observations are keyed by metric_uid, NOT metric_id — every detector
	// records the uid in its trigger evidence, so resolve it from there.
	// Querying with the metric_id would silently return an empty trend
	// (same failure class as the GetCurrentMetrics("") incident).
	metricUID := alert.MetricID // fallback for alerts predating uid evidence
	if v, ok := evidence["metric_uid"].(string); ok && v != "" {
		metricUID = v
	}
	lookbackStart := out.WindowEnd.Add(-7 * 24 * time.Hour)
	const researchTrendLimit = 60
	observations, err := a.store.GetObservations(ctx, metricUID, lookbackStart, out.WindowEnd, researchTrendLimit)
	if err != nil {
		log.Error().Err(err).Str("metric_id", alert.MetricID).Msg("fetch observations failed")
	} else {
		out.RecentTrend = make([]TrendPoint, 0, len(observations))
		for _, obs := range observations {
			out.RecentTrend = append(out.RecentTrend, TrendPoint{Time: obs.Time, Value: obs.Value})
		}
	}

	// Fetch related entities + relations (via metric → entity → relations).
	// For MVP: use metric's entity from evidence.
	if entityID, ok := evidence["entity_id"].(string); ok && entityID != "" {
		entity, err := a.store.GetEntityByID(ctx, entityID)
		if err != nil {
			log.Error().Err(err).Str("entity_id", entityID).Msg("fetch entity failed")
		} else {
			out.MetricName = entity.Name
			out.RelatedEntities = append(out.RelatedEntities, EntityRef{
				ID:         entity.ID,
				Name:       entity.Name,
				Namespace:  entity.Namespace,
				EntityType: string(entity.EntityType),
				Tags:       entity.Tags,
			})
		}

		// Relations (2-hop in system-architecture.md §11.4 — single hop for MVP).
		relations, err := a.store.GetRelatedEntities(ctx, entityID)
		if err != nil {
			log.Error().Err(err).Str("entity_id", entityID).Msg("fetch relations failed")
		} else {
			for _, rel := range relations {
				out.Relations = append(out.Relations, RelationRef{
					SourceID:     rel.SourceID,
					TargetID:     rel.TargetID,
					RelationType: rel.RelationType,
					Direction:    rel.Direction,
					Description:  rel.Description,
				})
			}
		}
	}

	return out, nil
}

// PersistentSnapshotContext wraps a ResearchContext for storage as research_snapshots.
type PersistentSnapshotContext struct {
	AlertID          string
	Context          []byte
	OntologyFrozenAt time.Time
}

func (a *Assembler) SaveSnapshot(ctx context.Context, rc *ResearchContext) error {
	ctxJSON, err := json.Marshal(rc)
	if err != nil {
		return fmt.Errorf("marshal research context: %w", err)
	}
	return a.store.SaveSnapshot(ctx, model.ResearchSnapshot{
		AlertID:          rc.AlertID,
		Context:          ctxJSON,
		OntologyFrozenAt: time.Now(),
	})
}

// derefTime safely dereferences a time pointer.
func derefTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

// toFloat converts interface{} to float64.
func toFloat(v interface{}) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int:
		return float64(x)
	case int64:
		return float64(x)
	default:
		return 0
	}
}
