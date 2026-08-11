// Package research 在告警触发后组装研究上下文（ResearchContext = 指标 +
// 实体 + 关系 + 近期观测趋势），落盘为 research_snapshots 供前端展示与研判。
// 观测查询一律使用触发证据（evidence）里的 metric_uid，而非 alert.MetricID。
package research

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

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
	Timeline        []TimelineEntry        `json:"timeline"`
	Overlays        []OverlaySeries        `json:"overlays"`
	Metadata        map[string]interface{} `json:"metadata"`
	AssembledAt     time.Time              `json:"assembled_at"`
}

// TimelineEntry is one point on the Research Timeline view: the metric's
// observed value at a specific moment. Built from the same observation store
// that feeds RecentTrend; a wider lookback (14 days) and a higher point limit
// give the timeline a fuller picture than the narrow trend strip.
type TimelineEntry struct {
	Time      time.Time `json:"time"`
	MetricUID string    `json:"metric_uid"`
	Value     float64   `json:"value"`
}

// OverlaySeries is a named metric series rendered on the Research Overlay view.
type OverlaySeries struct {
	MetricUID string         `json:"metric_uid"`
	Points    []OverlayPoint `json:"points"`
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

// Assemble 为一条告警组装完整研究上下文：从触发证据取 metric_uid → 拉取
// 回看窗口内的观测形成 RecentTrend → 补充实体与关联关系 → 汇总元数据。
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
		if err := json.Unmarshal(alert.Evidence, &evidence); err != nil {
			return nil, fmt.Errorf("decode alert evidence: %w", err)
		}
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
		return nil, fmt.Errorf("fetch observations for %s: %w", alert.MetricID, err)
	}
	out.RecentTrend = make([]TrendPoint, 0, len(observations))
	for _, obs := range observations {
		out.RecentTrend = append(out.RecentTrend, TrendPoint{Time: obs.Time, Value: obs.Value})
	}

	// Build the Timeline view (14-day lookback, up to 120 points). Separately
	// from RecentTrend which uses a 7-day / 60-point window — the Timeline pane
	// in the Research view needs context beyond the recent strip.
	timelineStart := out.WindowEnd.Add(-14 * 24 * time.Hour)
	timelineEntries, err := a.emitTimeline(ctx, metricUID, timelineStart, out.WindowEnd)
	if err != nil {
		return nil, fmt.Errorf("emit timeline for %s: %w", alert.MetricID, err)
	}
	out.Timeline = timelineEntries

	// Overlays are reserved for future multi-series comparisons. The field is
	// emitted as an empty slice so the API contract is stable from day one.
	out.Overlays = []OverlaySeries{}

	// Fetch related entities + relations (via metric → entity → relations).
	// For MVP: use metric's entity from evidence.
	if entityID, ok := evidence["entity_id"].(string); ok && entityID != "" {
		entity, err := a.store.GetEntityByID(ctx, entityID)
		if err != nil {
			return nil, fmt.Errorf("fetch entity %s: %w", entityID, err)
		}
		if entity != nil {
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
			return nil, fmt.Errorf("fetch relations for %s: %w", entityID, err)
		}
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

// emitTimeline pulls observations for the given metric UID within [start, end]
// and maps them to TimelineEntries. It reuses store.GetObservations so no
// additional store methods are needed.
func (a *Assembler) emitTimeline(ctx context.Context, metricUID string, start, end time.Time) ([]TimelineEntry, error) {
	const timelineLimit = 120
	obs, err := a.store.GetObservations(ctx, metricUID, start, end, timelineLimit)
	if err != nil {
		return nil, err
	}
	entries := make([]TimelineEntry, 0, len(obs))
	for _, o := range obs {
		entries = append(entries, TimelineEntry{
			Time:      o.Time,
			MetricUID: o.MetricUID,
			Value:     o.Value,
		})
	}
	return entries, nil
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
