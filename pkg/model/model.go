// Package model 定义 Core、API 与插件共享的领域类型——无行为的纯结构体，
// 是 docs/domain-model.md 中五个领域对象的权威 Go 表示。
package model

import "time"

// SourceClass identifies how an individual provider snapshot was produced.
// It is explicit provenance: callers must never derive it from plugin or
// provider names. NormalizeSourceClass protects persistence/API boundaries
// from missing or future values.
type SourceClass string

const (
	SourceClassReal    SourceClass = "real"
	SourceClassMock    SourceClass = "mock"
	SourceClassTest    SourceClass = "test"
	SourceClassUnknown SourceClass = "unknown"
)

func NormalizeSourceClass(class SourceClass) SourceClass {
	switch class {
	case SourceClassReal, SourceClassMock, SourceClassTest, SourceClassUnknown:
		return class
	default:
		return SourceClassUnknown
	}
}

// ---- Entity ----

type Entity struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Namespace     string            `json:"namespace"`
	EntityType    EntityType        `json:"entity_type"`
	PluginID      string            `json:"plugin_id"`
	Tags          []string          `json:"tags,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	Version       int               `json:"version"`
	EffectiveFrom time.Time         `json:"effective_from"`
	EffectiveTo   *time.Time        `json:"effective_to,omitempty"`
	Supersedes    *int              `json:"supersedes,omitempty"`
	ChangeLog     string            `json:"change_log,omitempty"`
}

type EntityType string

const (
	EntityTypeAsset       EntityType = "asset"
	EntityTypeInstrument  EntityType = "instrument"
	EntityTypeFlow        EntityType = "flow"
	EntityTypeInstitution EntityType = "institution"
	EntityTypeIndicator   EntityType = "indicator"
	EntityTypeIndex       EntityType = "index"
	EntityTypeMarket      EntityType = "market"
)

// ---- Metric ----

type MetricDefinition struct {
	ID            string            `json:"id"`
	UID           string            `json:"uid"`
	Name          string            `json:"name"`
	Description   string            `json:"description,omitempty"`
	Unit          string            `json:"unit"`
	Frequency     string            `json:"frequency"`
	EntityID      string            `json:"entity_id"`
	PluginID      string            `json:"plugin_id"`
	Tags          map[string]string `json:"tags,omitempty"`
	Active        bool              `json:"active"`
	Version       int               `json:"version"`
	EffectiveFrom time.Time         `json:"effective_from"`
	EffectiveTo   *time.Time        `json:"effective_to,omitempty"`
	Supersedes    *int              `json:"supersedes,omitempty"`
	ChangeLog     string            `json:"change_log,omitempty"`
}

// ---- Observation ----

type Observation struct {
	Time                time.Time   `json:"time"`
	MetricID            string      `json:"metric_id"`
	MetricUID           string      `json:"metric_uid"`
	Value               float64     `json:"value"`
	Labels              []byte      `json:"labels,omitempty"` // JSONB
	LabelsHash          string      `json:"labels_hash"`
	SourcePlugin        string      `json:"source_plugin"`
	SourcePluginVersion string      `json:"source_plugin_version"`
	SourceProvider      string      `json:"source_provider"`
	SourceClass         SourceClass `json:"source_class"`
	SourceFetchedAt     time.Time   `json:"source_fetched_at"`
	QualityGrade        string      `json:"quality_grade"`
	QualityConfidence   float64     `json:"quality_confidence"`
	SystemQualityScore  *float64    `json:"system_quality_score,omitempty"`
}

// ---- Relation ----

type Relation struct {
	ID            int           `json:"id"`
	SourceID      string        `json:"source_id"`
	TargetID      string        `json:"target_id"`
	RelationType  string        `json:"relation_type"`
	Layer         RelationLayer `json:"layer"`
	Direction     string        `json:"direction"`
	Confidence    float64       `json:"confidence"`
	TypicalLag    *string       `json:"typical_lag,omitempty"` // INTERVAL as string
	Description   string        `json:"description,omitempty"`
	Source        string        `json:"source"` // plugin_suggested | system_inferred | user_defined
	Version       int           `json:"version"`
	EffectiveFrom time.Time     `json:"effective_from"`
	EffectiveTo   *time.Time    `json:"effective_to,omitempty"`
}

type RelationLayer string

const (
	RelationLayerStructural  RelationLayer = "structural"
	RelationLayerSemantic    RelationLayer = "semantic"
	RelationLayerStatistical RelationLayer = "statistical"
	RelationLayerCausal      RelationLayer = "causal"
)

// ---- Rule ----

type Rule struct {
	ID            int        `json:"id"`
	Name          string     `json:"name"`
	MetricID      string     `json:"metric_id"`
	DetectorName  string     `json:"detector_name"`
	Severity      Severity   `json:"severity"`
	Config        []byte     `json:"config"` // JSONB
	Description   string     `json:"description,omitempty"`
	Enabled       bool       `json:"enabled"`
	Source        RuleSource `json:"source"`
	IsOverride    bool       `json:"is_override"`
	Version       int        `json:"version"`
	EffectiveFrom time.Time  `json:"effective_from"`
	EffectiveTo   *time.Time `json:"effective_to,omitempty"`
}

type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityWarning  Severity = "warning"
	SeverityInfo     Severity = "info"
)

type RuleSource string

const (
	RuleSourcePluginSuggested RuleSource = "plugin_suggested"
	RuleSourceUserOverride    RuleSource = "user_override"
	RuleSourceSystemDefault   RuleSource = "system_default"
)

// ---- Alert ----

type Alert struct {
	ID                 string      `json:"id"`
	Title              string      `json:"title"`
	Summary            string      `json:"summary"`
	Severity           Severity    `json:"severity"`
	Status             string      `json:"status"` // active | resolved
	MetricID           string      `json:"metric_id"`
	RuleID             int         `json:"rule_id"`
	RuleVersion        int         `json:"rule_version"`
	RuleEffectiveFrom  time.Time   `json:"rule_effective_from"`
	DetectorName       string      `json:"detector_name"`
	DedupKey           string      `json:"dedup_key"`
	WindowStart        *time.Time  `json:"window_start,omitempty"`
	WindowEnd          *time.Time  `json:"window_end,omitempty"`
	Evidence           []byte      `json:"evidence"` // JSONB
	PluginID           string      `json:"plugin_id"`
	SourceProvider     string      `json:"source_provider"`
	SourceClass        SourceClass `json:"source_class"`
	DedupCount         int         `json:"dedup_count"`
	LastDeduplicatedAt *time.Time  `json:"last_deduplicated_at,omitempty"`
	TriggeredAt        time.Time   `json:"triggered_at"`
	ResolvedAt         *time.Time  `json:"resolved_at,omitempty"`
}

// ---- Research Snapshot ----

type ResearchSnapshot struct {
	AlertID          string    `json:"alert_id"`
	Context          []byte    `json:"context"` // JSONB
	OntologyFrozenAt time.Time `json:"ontology_frozen_at"`
}
