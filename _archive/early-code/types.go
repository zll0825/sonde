// Package core 定义 Capital Observatory 的核心类型和接口。
//
// Core 不包含任何金融知识。所有类型都是通用抽象。
// 具体的金融语义由 Plugin 注入。
package core

import (
	"time"
)

// ──────────────────────────────────────────────
// Metric
// ──────────────────────────────────────────────

// MetricID 是 Metric 的唯一标识，格式：<namespace>.<entity>.<metric>
// 例如：gold.etf.net_inflow, btc.exchange.balance
type MetricID string

// Metric 定义一个可观测指标的元信息。
type Metric struct {
	ID               MetricID      `json:"id"`
	Name             string        `json:"name"`
	Description      string        `json:"description"`
	Unit             string        `json:"unit"`
	Frequency        time.Duration `json:"frequency"`
	Plugin           string        `json:"plugin"`
	Tags             []string      `json:"tags"`
	ObservedEntityID string        `json:"observed_entity_id"`
	ObservedProperty string        `json:"observed_property"`
}

// MetricSnapshot 是某个 Metric 在某个时间点的观测值。
type MetricSnapshot struct {
	MetricID  MetricID  `json:"metric_id"`
	Value     float64   `json:"value"`
	Timestamp time.Time `json:"timestamp"`
}

// ──────────────────────────────────────────────
// Ontology: Entity
// ──────────────────────────────────────────────

// EntityType 是实体的类别。
type EntityType string

const (
	EntityAsset       EntityType = "asset"       // 可交易资产：BTC, ETH, 黄金
	EntityIndex       EntityType = "index"       // 指数：DXY, S&P 500
	EntityInstrument  EntityType = "instrument"  // 金融工具：ETF, 期货
	EntityFlow        EntityType = "flow"        // 资金流：北向资金, ETF净流入
	EntityInstitution EntityType = "institution" // 机构：美联储, 日本央行
	EntityIndicator   EntityType = "indicator"   // 宏观指标：CPI, PMI
	EntityMarket      EntityType = "market"      // 市场：A股, 美股
	EntityFactor      EntityType = "factor"      // 因子：利率, 汇率
	EntityChannel     EntityType = "channel"     // 通道：ETF申赎, 北向通道
	EntityJurisdiction EntityType = "jurisdiction" // 管辖区域：美国, 中国
)

// Entity 是 Ontology 中被观测的对象。
type Entity struct {
	ID         string                 `json:"id"`
	Name       string                 `json:"name"`
	Namespace  string                 `json:"namespace"`
	EntityType EntityType             `json:"entity_type"`
	Tags       []string               `json:"tags"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
	Plugin     string                 `json:"plugin"`
}

// ──────────────────────────────────────────────
// Ontology: Relation
// ──────────────────────────────────────────────

// RelationType 是两个 Entity 之间的关系类型。
type RelationType string

const (
	// 因果类
	RelCauses   RelationType = "causes"   // A 导致 B
	RelLeads    RelationType = "leads"    // A 领先于 B（时间上先变）
	RelSignals  RelationType = "signals"  // A 是 B 的信号

	// 相关类
	RelCorrelates         RelationType = "correlates"          // A 与 B 相关
	RelInverselyCorrelates RelationType = "inversely_correlates" // A 与 B 负相关

	// 结构类
	RelComponentOf RelationType = "component_of" // A 是 B 的组成部分
	RelTracks      RelationType = "tracks"       // A 追踪 B
	RelHedges      RelationType = "hedges"       // A 是 B 的对冲
	RelFlowsInto   RelationType = "flows_into"   // 资金从 A 流入 B
	RelFlowsOut    RelationType = "flows_out_of" // 资金从 A 流出
	RelExposedTo   RelationType = "exposed_to"   // A 暴露于 B 的风险
	RelInfluences  RelationType = "influences"   // A 影响 B
	RelDependsOnRegime RelationType = "depends_on_regime" // A 与 B 的关系依赖市场状态

	// 竞争类
	RelCompetes RelationType = "competes" // A 和 B 竞争
)

// Direction 是关系的方向。
type Direction string

const (
	DirForward      Direction = "forward"
	DirBackward     Direction = "backward"
	DirBidirectional Direction = "bidirectional"
)

// Relation 是两个 Entity 之间的有向连接。
type Relation struct {
	SourceID      string        `json:"source_id"`
	TargetID      string        `json:"target_id"`
	RelationType  RelationType  `json:"relation_type"`
	Direction     Direction     `json:"direction"`
	Confidence    float64       `json:"confidence"`
	TypicalLag    time.Duration `json:"typical_lag,omitempty"`
	Description   string        `json:"description,omitempty"`
	Plugin        string        `json:"plugin"`
}

// ──────────────────────────────────────────────
// Detector
// ──────────────────────────────────────────────

// DetectorType 是检测器的类型。
type DetectorType string

const (
	DetThreshold      DetectorType = "threshold"
	DetPercentile     DetectorType = "percentile"
	DetTrend          DetectorType = "trend"
	DetVolatility     DetectorType = "volatility"
	DetMovingAverage  DetectorType = "moving_average"
)

// DetectorConfig 是一个检测器的配置。
type DetectorConfig struct {
	ID         string            `json:"id"`
	Type       DetectorType      `json:"type"`
	MetricID   MetricID          `json:"metric_id"`
	Params     map[string]interface{} `json:"params"`
	Enabled    bool              `json:"enabled"`
}

// ──────────────────────────────────────────────
// Alert
// ──────────────────────────────────────────────

// Severity 是 Alert 的严重级别。
type Severity string

const (
	SeverityCritical Severity = "Critical"
	SeverityWarning  Severity = "Warning"
	SeverityInfo     Severity = "Info"
)

// Alert 是异常事件的标准化表达。
type Alert struct {
	ID         string                 `json:"id"`
	Title      string                 `json:"title"`
	Summary    string                 `json:"summary"`
	Severity   Severity               `json:"severity"`
	MetricID   MetricID               `json:"metric_id"`
	DetectorID string                 `json:"detector_id"`
	Timestamp  time.Time              `json:"timestamp"`
	Window     time.Duration          `json:"window"`
	Plugin     string                 `json:"plugin"`
	Context    map[string]interface{} `json:"context,omitempty"`
	DedupKey   string                 `json:"dedup_key"`
	Evidence   map[string]interface{} `json:"evidence,omitempty"`
}

// ──────────────────────────────────────────────
// Research
// ──────────────────────────────────────────────

// ResearchContext 是 Research 页面的完整上下文。
type ResearchContext struct {
	Alert           Alert             `json:"alert"`
	RelatedEntities []EntitySnapshot  `json:"related_entities"`
	Narrative       string            `json:"narrative,omitempty"`
}

// EntitySnapshot 是一个 Entity 在某个时刻的 Metric 快照。
type EntitySnapshot struct {
	Entity          Entity              `json:"entity"`
	CurrentMetrics  map[string]interface{} `json:"current_metrics"`
	Relation        *Relation           `json:"relation,omitempty"`
}
