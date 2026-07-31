// Package research 实现 Research 页面的组装逻辑。
//
// Research 页面的生成来自两个来源：
//  1. Plugin 提供的默认领域上下文
//  2. Ontology 提供的跨实体关系扩展
package research

import (
	"context"
	"fmt"
	"strings"

	"github.com/capital-observatory/capital-observatory/core"
)

// Service 是 Research 页面的组装服务。
type Service struct {
	alertSvc    core.AlertService
	metricSvc   core.MetricService
	ontologySvc core.OntologyService
	registry    *core.Registry
}

// NewService 创建一个 Research 服务。
func NewService(
	alertSvc core.AlertService,
	metricSvc core.MetricService,
	ontologySvc core.OntologyService,
	registry *core.Registry,
) *Service {
	return &Service{
		alertSvc:    alertSvc,
		metricSvc:   metricSvc,
		ontologySvc: ontologySvc,
		registry:    registry,
	}
}

// Build 为一个 Alert 构建完整的 Research Context。
func (s *Service) Build(ctx context.Context, alertID string) (*core.ResearchContext, error) {
	// 1. 获取 Alert
	alert, err := s.alertSvc.Get(ctx, alertID)
	if err != nil {
		return nil, fmt.Errorf("get alert: %w", err)
	}

	// 2. 获取 Metric 定义
	metric, err := s.metricSvc.Get(ctx, alert.MetricID)
	if err != nil {
		return nil, fmt.Errorf("get metric: %w", err)
	}

	// 3. 通过 Ontology 查找关联 Entity
	graph, err := s.ontologySvc.GetNeighbors(ctx, metric.ObservedEntityID, 2)
	if err != nil {
		// 即使没有 Ontology 关系，也返回基本的 Alert 信息
		return &core.ResearchContext{
			Alert: *alert,
		}, nil
	}

	// 4. 收集所有关联 Entity 的当前 Metric 快照
	var relatedEntities []core.EntitySnapshot
	for _, edge := range graph.Neighbors {
		snapshot := core.EntitySnapshot{
			Entity:         edge.Target,
			CurrentMetrics: make(map[string]interface{}),
			Relation:       &edge.Relation,
		}

		// 查找该 Entity 关联的所有 Metric
		allMetrics, _ := s.metricSvc.List(ctx)
		for _, m := range allMetrics {
			if m.ObservedEntityID == edge.Target.ID {
				latest, err := s.metricSvc.QueryLatest(ctx, m.ID, 1)
				if err == nil && len(latest) > 0 {
					snapshot.CurrentMetrics[string(m.ID)] = map[string]interface{}{
						"value":     latest[0].Value,
						"timestamp": latest[0].Timestamp,
						"name":      m.Name,
						"unit":      m.Unit,
					}
				}
			}
		}

		relatedEntities = append(relatedEntities, snapshot)
	}

	// 5. 生成叙述性摘要
	narrative := s.buildNarrative(alert, metric, relatedEntities)

	return &core.ResearchContext{
		Alert:           *alert,
		RelatedEntities: relatedEntities,
		Narrative:       narrative,
	}, nil
}

// buildNarrative 生成人类可读的叙述性摘要。
func (s *Service) buildNarrative(alert *core.Alert, metric *core.Metric, entities []core.EntitySnapshot) string {
	if len(entities) == 0 {
		return ""
	}

	var parts []string
	parts = append(parts, fmt.Sprintf("异常「%s」触发。", alert.Title))

	if len(entities) > 0 {
		parts = append(parts, fmt.Sprintf("关联 %d 个实体：", len(entities)))
		for _, e := range entities {
			if e.Relation != nil {
				parts = append(parts, fmt.Sprintf("  - %s（%s）", e.Entity.Name, e.Relation.Description))
			} else {
				parts = append(parts, fmt.Sprintf("  - %s", e.Entity.Name))
			}
		}
	}

	return strings.Join(parts, "\n")
}
