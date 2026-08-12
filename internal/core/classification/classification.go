// Package classification 实现两层告警分类：
//
// 第一层 (Event Clustering): 时间窗口 + 实体关联的保守告警聚类，将短周期内
// 同一实体或多实体相关触发合并为一个事件，防止告警风暴。
//
// 第二层 (Research Gating): 基于信号质量的研究触发门控，仅当满足跨指标
// 确认或置信度阈值时才升级事件为完整研究。
package classification

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"capital_observatory/pkg/model"
)

// ---- Resolver interfaces ----

// EntityResolver maps a metric ID to its owning entity ID.
// Implementations should query the entity store / metric_definitions table.
type EntityResolver interface {
	// MetricToEntity returns the entity ID that owns the given metric.
	// found=false when the metric is not registered to any entity.
	MetricToEntity(ctx context.Context, metricID string) (entityID string, found bool, err error)
}

// RelationReader checks whether an accepted structural/semantic relation
// exists between two entities in the relation store.
type RelationReader interface {
	// HasAcceptedRelation returns true when sourceEntity and targetEntity
	// share at least one accepted relation (structural or semantic layer).
	HasAcceptedRelation(ctx context.Context, sourceEntity, targetEntity string) (bool, error)
}

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

	// EntityResolver resolves metric IDs to entity IDs. Optional; if nil the
	// fallback (first segment of metric ID) is used. Production callers MUST
	// provide a resolver backed by the entity store.
	EntityResolver EntityResolver

	// RelationReader validates cross-entityMergeOption clusters. Optional; if nil
	// cross-entity co-occurrence will be rejected conservatively (same-metric
	// only) unless explicit related entities are provided.
	RelationReader RelationReader
}

// DefaultClusteringConfig returns conservative free-tier defaults.
func DefaultClusteringConfig() ClusteringConfig {
	return ClusteringConfig{
		Window:                   1 * time.Hour,
		MaxAlertsPerCluster:      5,
		EntityCooccurrenceWindow: 4 * time.Hour,
	}
}

// MergeEntry records a single alert-into-cluster merge decision.
type MergeEntry struct {
	AlertID          string    `json:"alert_id"`
	MetricID         string    `json:"metric_id"`
	Reason           string    `json:"reason"`
	Coalesced        bool      `json:"coalesced"`
	TriggeredAt      time.Time `json:"triggered_at"`
	RelationEvidence string    `json:"_relation_evidence"`
}

// EventCluster is the output of Layer 1 — one cluster replaces N raw alerts.
type EventCluster struct {
	ID             string         `json:"id"`
	PrimaryEntity  string         `json:"primary_entity"`
	EntityScope    []string       `json:"entity_scope"`
	Alerts         []string       `json:"alert_ids"`  // alert IDs in this cluster
	MetricIDs      []string       `json:"metric_ids"` // distinct counting includes the seed alert
	MaxSeverity    model.Severity `json:"max_severity"`
	FirstTriggered time.Time      `json:"first_triggered"`
	LastTriggered  time.Time      `json:"last_triggered"`
	Coalesced      bool           `json:"coalesced"` // true if >1 alert joined
	TriggerCount   int            `json:"trigger_count"`
	MergeLog       []MergeEntry   `json:"merge_log"` // audit trail of every merge
}

// Clusterer coalesces alerts within a time window into events.
type Clusterer struct {
	config ClusteringConfig

	// active holds the open event clusters in memory so that Receive can
	// assign successive alerts to the correct cluster. Protected by mu
	// because Receive is invoked from a goroutine in the outbox pipeline.
	mu     sync.Mutex
	active []EventCluster
}

// NewClusterer creates a Layer-1 clusterer.
//
// Deprecated: prefer NewClusterer with ClusteringConfig for production.
// This convenience constructor passes nil resolvers, which means canonical
// entity resolution falls back to the first-segment heuristic, and
// cross-entity co-occurrence is conservatively restricted.
func NewClusterer(cfg ClusteringConfig) *Clusterer {
	return &Clusterer{config: cfg}
}

// Receive assigns an alert to an existing active cluster or opens a new one.
// The returned *EventCluster points into the Clusterer's internal active slice;
// callers must not retain the pointer across subsequent Receive calls.
//
// Thread-safe: the outbox handler invokes this from a goroutine after the
// alert record has been durably written, so the receiver must serialize access.
func (c *Clusterer) Receive(ctx context.Context, alert model.Alert) *EventCluster {
	c.mu.Lock()
	defer c.mu.Unlock()

	for i := range c.active {
		if c.CanCoalesce(ctx, c.active[i], alert, nil) {
			c.AddAlertToCluster(ctx, &c.active[i], alert)
			return &c.active[i]
		}
	}

	nc := NewClusterWithContext(ctx, alert, c.config.EntityResolver)
	c.active = append(c.active, nc)
	return &c.active[len(c.active)-1]
}

// resolvePrimaryEntity resolves the canonical entity for a metric ID.
//
// Priority:
//  1. If an EntityResolver is configured, use it (DB-backed).
//  2. Otherwise, fall back to the first dot-segment heuristic with a log
//     warning that this is unreliable.
func (c *Clusterer) resolvePrimaryEntity(ctx context.Context, metricID string) string {
	if c.config.EntityResolver != nil {
		eid, found, err := c.config.EntityResolver.MetricToEntity(ctx, metricID)
		if err == nil && found {
			return eid
		}
		if err != nil {
			log.Printf("classification: EntityResolver error for metric %s: %v (falling back to heuristic)", metricID, err)
		}
	}
	// Fallback: primaryEntity uses first dot-segment. Unreliable when entity
	// IDs differ from the metric prefix. Production callers MUST supply a
	// resolver to avoid mis-clustering.
	return primaryEntity(metricID)
}

// CanCoalesce answers whether a new alert belongs to an existing event cluster.
//
// Rules (order matters):
//  1. Capacity guard — reject if cluster is full.
//  2. Chronological order — reject if incoming triggered before cluster last.
//  3. Negative time-delta sanity — reject if incoming triggered before cluster first.
//  4. Same entity AND within Window -> coalesce.
//  5. Cross-entity: require an accepted relation AND within EntityCooccurrenceWindow.
func (c *Clusterer) CanCoalesce(ctx context.Context, cluster EventCluster, candidate model.Alert, relatedEntities []string) bool {
	// Capacity guard
	if len(cluster.Alerts) >= c.config.MaxAlertsPerCluster {
		return false
	}

	// Chronological-order check: incoming must not precede the cluster's
	// most recent alert. Out-of-order arrivals indicate pipeline anomalies
	// and must not be silently merged.
	if candidate.TriggeredAt.Before(cluster.LastTriggered) {
		log.Printf("classification: rejecting out-of-order alert %s (triggered %s before cluster last %s)",
			candidate.ID, candidate.TriggeredAt.Format(time.RFC3339), cluster.LastTriggered.Format(time.RFC3339))
		return false
	}

	// Negative time-delta sanity check: if the candidate falls entirely
	// before the cluster's first trigger, reject.
	if candidate.TriggeredAt.Before(cluster.FirstTriggered) {
		log.Printf("classification: rejecting alert %s — triggered before cluster first (%s < %s)",
			candidate.ID, candidate.TriggeredAt.Format(time.RFC3339), cluster.FirstTriggered.Format(time.RFC3339))
		return false
	}

	candidateEntity := c.resolvePrimaryEntity(ctx, candidate.MetricID)

	// Same entity, within window
	if cluster.PrimaryEntity == candidateEntity {
		delta := candidate.TriggeredAt.Sub(cluster.FirstTriggered)
		if delta < 0 {
			log.Printf("classification: rejecting same-entity alert %s — negative delta %s", candidate.ID, delta)
			return false
		}
		if delta <= c.config.Window {
			return true
		}
		// Window exceeded — too-wide window means the cluster spans too long
		log.Printf("classification: rejecting alert %s — window exceeded (%s > %s)", candidate.ID, delta, c.config.Window)
		return false
	}

	// Cross-entity co-occurrence: require an accepted relation when a
	// RelationReader is configured; otherwise fall back to the relatedEntities
	// list (legacy path).
	if c.config.RelationReader != nil {
		hasRel, err := c.config.RelationReader.HasAcceptedRelation(ctx, cluster.PrimaryEntity, candidateEntity)
		if err != nil {
			log.Printf("classification: RelationReader error entities %s<->%s: %v — conservatively rejecting cross-entity",
				cluster.PrimaryEntity, candidateEntity, err)
			return false
		}
		if !hasRel {
			log.Printf("classification: rejecting cross-entity coalesce %s<->%s — no accepted relation",
				cluster.PrimaryEntity, candidateEntity)
			return false
		}
		delta := candidate.TriggeredAt.Sub(cluster.FirstTriggered)
		if delta < 0 {
			log.Printf("classification: rejecting cross-entity alert %s — negative delta %s", candidate.ID, delta)
			return false
		}
		if delta <= c.config.EntityCooccurrenceWindow {
			return true
		}
		log.Printf("classification: rejecting cross-entity alert %s — co-occurrence window exceeded (%s > %s)",
			candidate.ID, delta, c.config.EntityCooccurrenceWindow)
		return false
	}

	// Legacy: no RelationReader — use the caller-provided relatedEntities list
	// as long as the candidate's entity appears in it.
	for _, related := range relatedEntities {
		if related == candidateEntity {
			delta := candidate.TriggeredAt.Sub(cluster.FirstTriggered)
			if delta >= 0 && delta <= c.config.EntityCooccurrenceWindow {
				return true
			}
			log.Printf("classification: rejecting cross-entity alert %s — window exceeded on legacy path (%s > %s)",
				candidate.ID, delta, c.config.EntityCooccurrenceWindow)
			return false
		}
	}
	return false
}

// AssignAlert assigns an alert to an existing cluster or returns nil to signal
// a new cluster should be created.
//
// This method accepts a context so the Clusterer can resolve entities via the
// configured EntityResolver.
func (c *Clusterer) AssignAlert(ctx context.Context, clusters []EventCluster, alert model.Alert, relatedEntities []string) *EventCluster {
	for i := range clusters {
		if c.CanCoalesce(ctx, clusters[i], alert, relatedEntities) {
			return &clusters[i]
		}
	}
	return nil
}

// AddAlertToCluster appends an alert to an existing cluster, updates severity
// bounds, and records a MergeEntry in the audit log.
func (c *Clusterer) AddAlertToCluster(ctx context.Context, cluster *EventCluster, alert model.Alert) {
	entry := MergeEntry{
		AlertID:     alert.ID,
		MetricID:    alert.MetricID,
		Reason:      "same-entity within window",
		Coalesced:   true,
		TriggeredAt: alert.TriggeredAt,
	}
	cluster.Alerts = append(cluster.Alerts, alert.ID)
	cluster.MetricIDs = appendUnique(cluster.MetricIDs, alert.MetricID)
	cluster.LastTriggered = alert.TriggeredAt
	cluster.Coalesced = true
	cluster.TriggerCount++
	if isHigherSeverity(alert.Severity, cluster.MaxSeverity) {
		cluster.MaxSeverity = alert.Severity
	}
	cluster.MergeLog = append(cluster.MergeLog, entry)
}

// NewCluster creates a fresh cluster seeded by an alert.
func NewCluster(alert model.Alert) EventCluster {
	now := alert.TriggeredAt
	return EventCluster{
		ID:             clusterID(alert),
		PrimaryEntity:  primaryEntity(alert.MetricID),
		EntityScope:    []string{primaryEntity(alert.MetricID)},
		Alerts:         []string{alert.ID},
		MetricIDs:      []string{alert.MetricID},
		MaxSeverity:    alert.Severity,
		FirstTriggered: now,
		LastTriggered:  now,
		Coalesced:      false,
		TriggerCount:   1,
		MergeLog:       nil,
	}
}

// NewClusterWithContext creates a fresh cluster seeded by an alert, resolving
// the canonical entity via the provided EntityResolver.
func NewClusterWithContext(ctx context.Context, alert model.Alert, resolver EntityResolver) EventCluster {
	pe := primaryEntity(alert.MetricID)
	if resolver != nil {
		if eid, found, err := resolver.MetricToEntity(ctx, alert.MetricID); err == nil && found {
			pe = eid
		}
	}
	now := alert.TriggeredAt
	return EventCluster{
		ID:             clusterID(alert),
		PrimaryEntity:  pe,
		EntityScope:    []string{pe},
		Alerts:         []string{alert.ID},
		MetricIDs:      []string{alert.MetricID},
		MaxSeverity:    alert.Severity,
		FirstTriggered: now,
		LastTriggered:  now,
		Coalesced:      false,
		TriggerCount:   1,
		MergeLog:       nil,
	}
}

func appendUnique(values []string, value string) []string {
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
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
		Cooldown:                30 * time.Minute,
		CrossMetricConfirmation: 0, // off by default (conservative)
		MinSeverityForResearch:  model.SeverityInfo,
		CoalesceBoost:           true,
	}
}

// ResearchDecision is the output of Layer 2.
type ResearchDecision struct {
	ClusterID      string         `json:"cluster_id"`
	ShouldResearch bool           `json:"should_research"`
	Severity       model.Severity `json:"severity"`
	Reason         string         `json:"reason"`
	CooldownUntil  *time.Time     `json:"cooldown_until,omitempty"`
}

// Gate applies Layer-2 filtering: cooldown, confirmation, severity threshold.
// Gate is the Layer-2 research / notification gate.
//
// Gate methods are called from the outbox handler goroutine (cmd/core/main.go),
// which fires one goroutine per alert — concurrent Evaluate / RecordExternalResearch
// calls are the norm. lastResearch is guarded by mu to stay race-clean.
type Gate struct {
	mu           sync.Mutex
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
//
// Gate 当前是本地静态 logic，生产环境应从接口拉取跨指标确认结果。
// This round does not introduce a new cross-metric data flow; distinctMetrics
// is an approximation from the caller. Production deployments should resolve
// true cross-indicator confirmation via a dedicated service or the metric
// store before calling Evaluate.
func (g *Gate) Evaluate(cluster EventCluster, distinctMetrics int) ResearchDecision {
	now := cluster.LastTriggered

	g.mu.Lock()
	// Cooldown check
	last, ok := g.lastResearch[cluster.PrimaryEntity]
	if ok && now.Sub(last) < g.config.Cooldown {
		g.mu.Unlock()
		return ResearchDecision{
			ClusterID:      cluster.ID,
			ShouldResearch: false,
			Severity:       cluster.MaxSeverity,
			Reason:         fmt.Sprintf("cooldown active (%s remaining)", g.config.Cooldown-now.Sub(last)),
			CooldownUntil:  ptr(last.Add(g.config.Cooldown)),
		}
	}
	g.mu.Unlock()

	// Severity threshold
	if !meetsSeverityThreshold(cluster.MaxSeverity, g.config.MinSeverityForResearch) {
		return ResearchDecision{
			ClusterID:      cluster.ID,
			ShouldResearch: false,
			Severity:       cluster.MaxSeverity,
			Reason:         fmt.Sprintf("severity %s below threshold %s", cluster.MaxSeverity, g.config.MinSeverityForResearch),
		}
	}

	// Cross-metric confirmation
	if g.config.CrossMetricConfirmation > 0 && distinctMetrics < g.config.CrossMetricConfirmation {
		return ResearchDecision{
			ClusterID:      cluster.ID,
			ShouldResearch: false,
			Severity:       cluster.MaxSeverity,
			Reason:         fmt.Sprintf("insufficient cross-metric confirmation (%d/%d)", distinctMetrics, g.config.CrossMetricConfirmation),
		}
	}

	// Apply coalesce boost
	effective := cluster.MaxSeverity
	if g.config.CoalesceBoost && cluster.Coalesced {
		effective = bumpSeverity(effective)
	}

	// All gates passed — record research timestamp for cooldown. Hold the
	// mutex for the write to avoid racing another Evaluate that just read
	// the same map entry.
	g.mu.Lock()
	g.lastResearch[cluster.PrimaryEntity] = now
	g.mu.Unlock()

	return ResearchDecision{
		ClusterID:      cluster.ID,
		ShouldResearch: true,
		Severity:       effective,
		Reason:         fmt.Sprintf("cluster confirmed: %d alerts, severity %s", len(cluster.Alerts), effective),
	}
}

// RecordExternalResearch manually records a research event (for manual triggers).
func (g *Gate) RecordExternalResearch(entity string, at time.Time) {
	g.mu.Lock()
	g.lastResearch[entity] = at
	g.mu.Unlock()
}

// ---- Internal helpers ----

func clusterID(alert model.Alert) string {
	raw := fmt.Sprintf("%s\x1f%d", primaryEntity(alert.MetricID), bucketTimestamp(alert.TriggeredAt))
	sum := sha256.Sum256([]byte(raw))
	return "evt:" + hex.EncodeToString(sum[:])
}

// primaryEntity returns the first dot-segment of the metric ID as the entity
// identifier. This is a heuristic fallback used when no EntityResolver is
// configured — it is unreliable when metric IDs do not map 1:1 to entity IDs.
func primaryEntity(metricID string) string {
	if parts := strings.SplitN(metricID, ".", 2); len(parts) >= 1 {
		return parts[0]
	}
	return metricID
}

// resolveCanonicalEntity is the preferred helper when an EntityResolver is
// available. It falls back to the heuristic if the resolver is nil or returns
// not-found, with a clear signal that the result may be unreliable.
//
// Deprecated: use Clusterer.resolvePrimaryEntity instead. This standalone
// helper remains for backward compatibility.
func resolveCanonicalEntity(ctx context.Context, metricID string, resolver EntityResolver) (string, error) {
	if resolver != nil {
		eid, found, err := resolver.MetricToEntity(ctx, metricID)
		if err != nil {
			return "", fmt.Errorf("resolving entity for metric %s: %w", metricID, err)
		}
		if found {
			return eid, nil
		}
	}
	// Fallback heuristic
	return primaryEntity(metricID), nil
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
