package core

import (
	"context"
	"time"
)

// ──────────────────────────────────────────────
// MetricService
// ──────────────────────────────────────────────

// MetricService 负责 Metric 的注册、存储和查询。
type MetricService interface {
	// Register 注册一个 Metric 定义。
	Register(ctx context.Context, m Metric) error

	// Get 获取一个 Metric 的定义。
	Get(ctx context.Context, id MetricID) (*Metric, error)

	// List 列出所有已注册的 Metric。
	List(ctx context.Context) ([]Metric, error)

	// Write 写入一个 Metric 快照。
	Write(ctx context.Context, snapshot MetricSnapshot) error

	// WriteBatch 批量写入 Metric 快照。
	WriteBatch(ctx context.Context, snapshots []MetricSnapshot) error

	// Query 查询某个 Metric 在时间范围内的快照。
	Query(ctx context.Context, id MetricID, from, to time.Time) ([]MetricSnapshot, error)

	// QueryLatest 获取某个 Metric 的最新 N 个快照。
	QueryLatest(ctx context.Context, id MetricID, n int) ([]MetricSnapshot, error)
}

// ──────────────────────────────────────────────
// OntologyService
// ──────────────────────────────────────────────

// OntologyService 负责 Entity/Relation 的存储和图查询。
type OntologyService interface {
	// RegisterEntity 注册一个 Entity。
	RegisterEntity(ctx context.Context, e Entity) error

	// RegisterRelation 注册一条 Relation。
	RegisterRelation(ctx context.Context, r Relation) error

	// GetEntity 获取一个 Entity。
	GetEntity(ctx context.Context, id string) (*Entity, error)

	// GetRelation 获取两个 Entity 之间的关系。
	GetRelation(ctx context.Context, sourceID, targetID string, relType RelationType) (*Relation, error)

	// ListEntities 列出所有 Entity。
	ListEntities(ctx context.Context) ([]Entity, error)

	// ListRelations 列出所有 Relation。
	ListRelations(ctx context.Context) ([]Relation, error)

	// GetNeighbors 获取一个 Entity 的所有邻居（关系图）。
	// depth 控制遍历深度，1 表示只看直接邻居。
	GetNeighbors(ctx context.Context, entityID string, depth int) (*EntityGraph, error)

	// GetAlertContext 为一个 Alert 构建关联上下文。
	// 通过 Metric 找到 Entity，再遍历关系图。
	GetAlertContext(ctx context.Context, metricID MetricID) (*ResearchContext, error)

	// FindPath 查找两个 Entity 之间的关系路径。
	FindPath(ctx context.Context, from, to string) ([]Relation, error)
}

// EntityGraph 是 Entity 关系图的查询结果。
type EntityGraph struct {
	Root      Entity       `json:"root"`
	Neighbors []GraphEdge  `json:"neighbors"`
}

// GraphEdge 是关系图中的一条边。
type GraphEdge struct {
	Target   Entity   `json:"target"`
	Relation Relation `json:"relation"`
}

// ──────────────────────────────────────────────
// DetectorService
// ──────────────────────────────────────────────

// DetectorService 负责异常检测。
type DetectorService interface {
	// Register 注册一个 Detector 配置。
	Register(ctx context.Context, config DetectorConfig) error

	// RunAll 对所有已注册的 Detector 执行一次检测。
	RunAll(ctx context.Context) ([]Alert, error)

	// RunForMetric 对指定 Metric 执行一次检测。
	RunForMetric(ctx context.Context, metricID MetricID) ([]Alert, error)
}

// ──────────────────────────────────────────────
// AlertService
// ──────────────────────────────────────────────

// AlertService 负责 Alert 的生命周期管理。
type AlertService interface {
	// Create 创建一个 Alert。
	Create(ctx context.Context, alert Alert) error

	// Get 获取一个 Alert。
	Get(ctx context.Context, id string) (*Alert, error)

	// ListRecent 获取最近 N 个 Alert。
	ListRecent(ctx context.Context, n int) ([]Alert, error)

	// ListBySeverity 按严重级别获取 Alert。
	ListBySeverity(ctx context.Context, severity Severity, n int) ([]Alert, error)

	// Dedup 检查一个 Alert 是否已存在（基于 dedup_key）。
	Dedup(ctx context.Context, dedupKey string) (bool, error)
}

// ──────────────────────────────────────────────
// ResearchService
// ──────────────────────────────────────────────

// ResearchService 负责 Research 页面的组装。
type ResearchService interface {
	// Build 为一个 Alert 构建完整的 Research Context。
	// 合并 Plugin 默认上下文和 Ontology 关系扩展。
	Build(ctx context.Context, alertID string) (*ResearchContext, error)
}
