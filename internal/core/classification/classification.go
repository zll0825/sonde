// Package classification 实现两层告警分类：
//
// 第一层 (Event Clustering): 时间窗口 + 实体关联的保守告警聚类，将短周期内
// 同一实体或多实体相关触发合并为一个事件，防止告警风暴。
//
// 第二层 (Research Gating): 基于信号质量的研究触发门控，仅当满足跨指标
// 确认或置信度阈值时才升级事件为完整研究。
package classification

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"capital_observatory/pkg/model"
)

// ---- Layer 1: Event Clustering ----

// ClusteringConfig configures the Layer-1 event clusterer.
type ClusteringConfig struct {
	// Window is the time horizon for clustering alerts into the same event.
	// Alerts for the same entity within this window are coalesced.
	Window time.Duration

	// MaxAlertsPerCluster caps how many alerts a single event can absorb.
	// Beyond this, a new spillover event is created.
	MaxAlertsPerCluster int

	// EntityCooccurrenceWindow extends clustering to related entities (BTC-GLD).
	// Must be >= Window.
	EntityCooccurrenceWindow time.Duration
}

// DefaultClusteringConfig returns conservative free-tier defaults.
func DefaultClusteringConfig() ClusteringConfig {
	return ClusteringConfig{
		Window:                   1 * time.Hour,
		MaxAlertsPerCluster:      5,
		EntityCooccurrenceWindow: 4 * time.Hour,
	}
}

// EventCluster is the output of Layer 1 — one cluster replaces N raw alerts.
type EventCluster struct {
	ID             string         `json:"id"`
	PrimaryEntity  string         `json:"primary_entity"`
	EntityScope    []string       `json:"entity_scope"`
	Alerts         []string       `json:"alert_ids"` // alert IDs in this cluster
	MaxSeverity    model.Severity `json:"max_severity"`
	FirstTriggered time.Time      `json:"first_triggered"`
	LastTriggered  time.Time      `json:"last_triggered"`
	Coalesced     bool           `json:"coalesced"` // true if >1 alert joined
	TriggerCount  int            `json:"trigger_count"`
}

// Clusterer coalesces alerts within a time window into events.
type Clusterer struct {
	config ClusteringConfig
}

// NewClusterer creates a Layer-1 clusterer.
func NewClusterer(cfg ClusteringConfig) *Clusterer {
	return &Clusterer{config: cfg}
}

// CanCoalesce answers whether a new alert belongs to an existing event cluster.
// Rules:
//   - Same entity AND within Window -> coalesce
//   - Related entity (cross-metric co-occurrence) AND within EntityCooccurrenceWindow -> coalesce
//   - Cluster has capacity (under MaxAlertsPerCluster) -> coalesce
func (c *Clusterer) CanCoalesce(cluster EventCluster, candidate model.Alert, relatedEntities []string) bool {
	// Capacity guard
	if len(cluster.Alerts) >= c.config.MaxAlertsPerCluster {
		return false
	}

	candidateEntity := primaryEntity(candidate.MetricID)

	// Same entity, within window
	if cluster.PrimaryEntity == candidateEntity {
		if candidate.TriggeredAt.Sub(cluster.FirstTriggered) <= c.config.Window {
			return true
		}
		if candidate.TriggeredAt.Sub(cluster.LastTriggered) <= c.config.Window {
			return true
		}
	}

	// Cross-entity co-occurrence: if the cluster's primary entity has a
	// related entity that matches the candidate's entity, and within window.
	for _, related := range relatedEntities {
		if related == candidateEntity {
			if candidate.TriggeredAt.Sub(cluster.FirstTriggered) <= c.config.EntityCooccurrenceWindow {
				return true
			}
		}
	}
	return false
}

// AssignAlert assigns an alert to an existing cluster or returns nil to signal
// a new cluster should be created.
func (c *Clusterer) AssignAlert(clusters []EventCluster, alert model.Alert, relatedEntities []string) *EventCluster {
	for i := range clusters {
		if c.CanCoalesce(clusters[i], alert, relatedEntities) {
			return &clusters[i]
		}
	}
	return nil
}

// AddAlertToCluster appends an alert to an existing cluster and updates severity bounds.
func (c *Clusterer) AddAlertToCluster(cluster *EventCluster, alert model.Alert) {
	cluster.Alerts = append(cluster.Alerts, alert.ID)
	cluster.LastTriggered = alert.TriggeredAt
	cluster.Coalesced = true
	cluster.TriggerCount++
	if isHigherSeverity(alert.Severity, cluster.MaxSeverity) {
		cluster.MaxSeverity = alert.Severity
	}
}

// NewCluster creates a fresh cluster seeded by an alert.
func NewCluster(alert model.Alert) EventCluster {
	now := alert.TriggeredAt
	return EventCluster{
		ID:             clusterID(alert),
		PrimaryEntity:  primaryEntity(alert.MetricID),
		EntityScope:    []string{primaryEntity(alert.MetricID)},
		Alerts:         []string{alert.ID},
		MaxSeverity:    alert.Severity,
		FirstTriggered: now,
		LastTriggered:  now,
		Coalesced:      false,
		TriggerCount:   1,
	}
}

// ---- Layer 2: Research Gating ----

// GateConfig configures the Layer-2 research gate.
type GateConfig struct {
	// Cooldown suppresses repeat research for the same cluster within this period.
	Cooldown time.Duration

	// CrossMetricConfirmation requires N distinct metrics to confirm before
	// research escalation. 0 means no cross-metric requirement.
	CrossMetricConfirmation int

	// MinSeverityForResearch: below this, clusters never produce research.
	MinSeverityForResearch model.Severity

	// CoalesceBoost upgrades coalesced multi-alert events one severity level.
	CoalesceBoost bool
}

// DefaultGateConfig returns conservative defaults.
func DefaultGateConfig() GateConfig {
	return GateConfig{
		Cooldown:               30 * time.Minute,
		CrossMetricConfirmation: 0, // off by default (conservative)
		MinSeverityForResearch: model.SeverityInfo,
		CoalesceBoost:          true,
	}
}

// ResearchDecision is the output of Layer 2.
type ResearchDecision struct {
	ClusterID     string         `json:"cluster_id"`
	ShouldResearch bool           `json:"should_research"`
	Severity      model.Severity `json:"severity"`
	Reason        string         `json:"reason"`
	CooldownUntil *time.Time     `json:"cooldown_until,omitempty"`
}

// Gate applies Layer-2 filtering: cooldown, confirmation, severity threshold.
type Gate struct {
	config       GateConfig
	lastResearch map[string]time.Time // cluster primary entity -> last research time
}

// NewGate creates a Layer-2 research gate.
func NewGate(cfg GateConfig) *Gate {
	return &Gate{
		config:       cfg,
		lastResearch: make(map[string]time.Time),
	}
}

// Evaluate decides whether a cluster should trigger research.
func (g *Gate) Evaluate(cluster EventCluster, distinctMetrics int) ResearchDecision {
	now := cluster.LastTriggered

	// Cooldown check
	last, ok := g.lastResearch[cluster.PrimaryEntity]
	if ok && now.Sub(last) < g.config.Cooldown {
		return ResearchDecision{
			ClusterID:     cluster.ID,
			ShouldResearch: false,
			Severity:      cluster.MaxSeverity,
			Reason:        fmt.Sprintf("cooldown active (%s remaining)", g.config.Cooldown-now.Sub(last)),
			CooldownUntil: ptr(last.Add(g.config.Cooldown)),
		}
	}

	// Severity threshold
	if !meetsSeverityThreshold(cluster.MaxSeverity, g.config.MinSeverityForResearch) {
		return ResearchDecision{
			ClusterID:     cluster.ID,
			ShouldResearch: false,
			Severity:      cluster.MaxSeverity,
			Reason:        fmt.Sprintf("severity %s below threshold %s", cluster.MaxSeverity, g.config.MinSeverityForResearch),
		}
	}

	// Cross-metric confirmation
	if g.config.CrossMetricConfirmation > 0 && distinctMetrics < g.config.CrossMetricConfirmation {
		return ResearchDecision{
			ClusterID:     cluster.ID,
			ShouldResearch: false,
			Severity:      cluster.MaxSeverity,
			Reason:        fmt.Sprintf("insufficient cross-metric confirmation (%d/%d)", distinctMetrics, g.config.CrossMetricConfirmation),
		}
	}

	// Apply coalesce boost
	effective := cluster.MaxSeverity
	if g.config.CoalesceBoost && cluster.Coalesced {
		effective = bumpSeverity(effective)
	}

	// Record research time for cooldown
	g.lastResearch[cluster.PrimaryEntity] = now

	return ResearchDecision{
		ClusterID:     cluster.ID,
		ShouldResearch: true,
		Severity:      effective,
		Reason:        fmt.Sprintf("cluster confirmed: %d alerts, severity %s", len(cluster.Alerts), effective),
	}
}

// RecordExternalResearch manually records a research event (for manual triggers).
func (g *Gate) RecordExternalResearch(entity string, at time.Time) {
	g.lastResearch[entity] = at
}

// ---- Internal helpers ----

func clusterID(alert model.Alert) string {
	raw := fmt.Sprintf("%s\x1f%d", primaryEntity(alert.MetricID), bucketTimestamp(alert.TriggeredAt))
	sum := sha256.Sum256([]byte(raw))
	return "evt:" + hex.EncodeToString(sum[:])
}

func primaryEntity(metricID string) string {
	if parts := strings.SplitN(metricID, ".", 2); len(parts) >= 1 {
		return parts[0]
	}
	return metricID
}

func bucketTimestamp(t time.Time) int64 {
	return t.Truncate(time.Hour).Unix()
}

func isHigherSeverity(a, b model.Severity) bool {
	rank := map[model.Severity]int{
		model.SeverityInfo:     0,
		model.SeverityWarning:  1,
		model.SeverityCritical: 2,
	}
	return rank[a] > rank[b]
}

func meetsSeverityThreshold(severity, threshold model.Severity) bool {
	rank := map[model.Severity]int{
		model.SeverityInfo:     0,
		model.SeverityWarning:  1,
		model.SeverityCritical: 2,
	}
	return rank[severity] >= rank[threshold]
}

func bumpSeverity(s model.Severity) model.Severity {
	switch s {
	case model.SeverityInfo:
		return model.SeverityWarning
	case model.SeverityWarning:
		return model.SeverityCritical
	default:
		return s
	}
}

func ptr[T any](v T) *T { return &v }

// SortClustersBySeverity sorts clusters by severity desc, then by first triggered asc.
func SortClustersBySeverity(clusters []EventCluster) {
	sort.SliceStable(clusters, func(i, j int) bool {
		if clusters[i].MaxSeverity != clusters[j].MaxSeverity {
			return isHigherSeverity(clusters[i].MaxSeverity, clusters[j].MaxSeverity)
		}
		return clusters[i].FirstTriggered.Before(clusters[j].FirstTriggered)
	})
}
