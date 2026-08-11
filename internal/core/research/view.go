// Package research 扩展视图：时间线、相关指标叠加、实体/关系图。
package research

import (
	"context"
	"fmt"
	"sort"
	"time"

	"capital_observatory/pkg/model"
)

// Timeline 按时间顺序排列的事件序列。
type Timeline struct {
	EntityID  string          `json:"entity_id"`
	Points    []TimelinePoint `json:"points"`
	SpanStart time.Time       `json:"span_start"`
	SpanEnd   time.Time       `json:"span_end"`
}

// TimelinePoint 时间线上的单个事件。
type TimelinePoint struct {
	Timestamp time.Time `json:"timestamp"`
	Type      string    `json:"type"` // "alert", "observation", "state_change"
	Severity  string    `json:"severity,omitempty"`
	MetricID  string    `json:"metric_id,omitempty"`
	Value     float64   `json:"value,omitempty"`
	Message   string    `json:"message,omitempty"`
}

// MetricOverlay 在同一时间轴上叠加的指标。
type MetricOverlay struct {
	MetricID   string            `json:"metric_id"`
	MetricName string            `json:"metric_name"`
	Unit       string            `json:"unit"`
	Points     []OverlayPoint    `json:"points"`
	SeriesMeta map[string]string `json:"series_meta,omitempty"`
}

// OverlayPoint 叠加图上的数据点。
type OverlayPoint struct {
	Time  time.Time `json:"time"`
	Value float64   `json:"value"`
}

// RelationGraph 实体及其关系的拓扑图。
type RelationGraph struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}

// GraphNode 图中的实体节点。
type GraphNode struct {
	EntityID   string            `json:"entity_id"`
	Name       string            `json:"name"`
	Namespace  string            `json:"namespace"`
	EntityType string            `json:"entity_type"`
	Tags       []string          `json:"tags,omitempty"`
	Properties map[string]string `json:"properties,omitempty"`
}

// GraphEdge 图中的关系边。
type GraphEdge struct {
	SourceID     string  `json:"source_id"`
	TargetID     string  `json:"target_id"`
	RelationType string  `json:"relation_type"`
	Direction    string  `json:"direction"`
	Weight       float64 `json:"weight,omitempty"` // 0..1, 关系强度
	Description  string  `json:"description,omitempty"`
}

// ViewBuilder 从告警上下文构建研究视图。
type ViewBuilder struct {
	store ResearchStore
}

// NewViewBuilder creates a view builder.
func NewViewBuilder(store ResearchStore) *ViewBuilder {
	return &ViewBuilder{store: store}
}

// BuildTimeline 为指定实体构建时间线。
func (vb *ViewBuilder) BuildTimeline(ctx context.Context, entityID string, since, until time.Time, limit int) (*Timeline, error) {
	if limit <= 0 {
		limit = 100
	}

	// 获取实体的所有关系（用于确定相关指标）
	relations, err := vb.store.GetRelatedEntities(ctx, entityID)
	if err != nil {
		return nil, fmt.Errorf("fetch relations for timeline: %w", err)
	}

	// 从关系中提取关联实体
	relatedEntityIDs := uniqueEntityIDs(entityID, relations)

	// 获取该实体及其关联实体的观测
	var allPoints []TimelinePoint

	// 获取实体自身的观测
	obs, err := vb.getObservationsForEntity(ctx, entityID, since, until, limit)
	if err != nil {
		return nil, err
	}
	allPoints = append(allPoints, obs...)

	// 获取关联实体的观测（每个实体限前 20 个）
	for _, rid := range relatedEntityIDs {
		robs, err := vb.getObservationsForEntity(ctx, rid, since, until, 20)
		if err != nil {
			continue
		}
		allPoints = append(allPoints, robs...)
	}

	// 按时间排序
	sort.Slice(allPoints, func(i, j int) bool {
		return allPoints[i].Timestamp.Before(allPoints[j].Timestamp)
	})

	// 截断到 limit
	if len(allPoints) > limit {
		allPoints = allPoints[len(allPoints)-limit:]
	}

	spanStart := since
	spanEnd := until
	if len(allPoints) > 0 {
		spanStart = allPoints[0].Timestamp
		spanEnd = allPoints[len(allPoints)-1].Timestamp
	}

	return &Timeline{
		EntityID:  entityID,
		Points:    allPoints,
		SpanStart: spanStart,
		SpanEnd:   spanEnd,
	}, nil
}

// BuildMetricOverlay 构建多指标叠加视图。
func (vb *ViewBuilder) BuildMetricOverlay(ctx context.Context, metricIDs []string, since, until time.Time, limit int) ([]MetricOverlay, error) {
	if limit <= 0 {
		limit = 60
	}

	overlays := make([]MetricOverlay, 0, len(metricIDs))
	for _, mid := range metricIDs {
		observations, err := vb.store.GetObservations(ctx, mid, since, until, limit)
		if err != nil {
			continue
		}
		points := make([]OverlayPoint, 0, len(observations))
		for _, o := range observations {
			points = append(points, OverlayPoint{Time: o.Time, Value: o.Value})
		}
		overlays = append(overlays, MetricOverlay{
			MetricID: mid,
			Points:   points,
		})
	}
	return overlays, nil
}

// BuildRelationGraph 构建实体关系图。
func (vb *ViewBuilder) BuildRelationGraph(ctx context.Context, rootEntityID string, hops int) (*RelationGraph, error) {
	if hops <= 0 {
		hops = 1
	}

	graph := &RelationGraph{
		Nodes: []GraphNode{},
		Edges: []GraphEdge{},
	}

	visited := make(map[string]bool)
	queue := []string{rootEntityID}

	for h := 0; h <= hops && len(queue) > 0; h++ {
		var nextQueue []string
		for _, eid := range queue {
			if visited[eid] {
				continue
			}
			visited[eid] = true

			entity, err := vb.store.GetEntityByID(ctx, eid)
			if err != nil {
				continue
			}
			if entity == nil {
				continue
			}

			graph.Nodes = append(graph.Nodes, GraphNode{
				EntityID:   entity.ID,
				Name:       entity.Name,
				Namespace:  entity.Namespace,
				EntityType: string(entity.EntityType),
				Tags:       entity.Tags,
			})

			relations, err := vb.store.GetRelatedEntities(ctx, eid)
			if err != nil {
				continue
			}
			for _, rel := range relations {
				graph.Edges = append(graph.Edges, GraphEdge{
					SourceID:     rel.SourceID,
					TargetID:     rel.TargetID,
					RelationType: rel.RelationType,
					Direction:    rel.Direction,
					Description:  rel.Description,
				})
				if !visited[rel.TargetID] {
					nextQueue = append(nextQueue, rel.TargetID)
				}
			}
		}
		queue = nextQueue
	}

	return graph, nil
}

// uniqueEntityIDs extracts unique target entity IDs from relations.
func uniqueEntityIDs(rootEntityID string, relations []model.Relation) []string {
	seen := map[string]bool{rootEntityID: true}
	var result []string
	for _, r := range relations {
		if !seen[r.TargetID] {
			seen[r.TargetID] = true
			result = append(result, r.TargetID)
		}
	}
	return result
}

// getObservationsForEntity converts observations to timeline points.
func (vb *ViewBuilder) getObservationsForEntity(ctx context.Context, entityID string, since, until time.Time, limit int) ([]TimelinePoint, error) {
	obs, err := vb.store.GetObservations(ctx, entityID, since, until, limit)
	if err != nil {
		return nil, err
	}
	points := make([]TimelinePoint, 0, len(obs))
	for _, o := range obs {
		points = append(points, TimelinePoint{
			Timestamp: o.Time,
			Type:      "observation",
			MetricID:  o.MetricID,
			Value:     o.Value,
		})
	}
	return points, nil
}
