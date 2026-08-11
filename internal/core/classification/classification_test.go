package classification

import (
	"context"
	"testing"
	"time"

	"capital_observatory/pkg/model"
)

func TestClusterer_SameEntityWithinWindow(t *testing.T) {
	c := NewClusterer(DefaultClusteringConfig())
	ctx := context.Background()
	cluster := NewCluster(model.Alert{
		ID:          "a1",
		MetricID:    "gld.ass.price",
		Severity:    model.SeverityWarning,
		TriggeredAt: time.Now(),
	})

	// Same entity, within 1h window
	ok := c.CanCoalesce(ctx, cluster, model.Alert{
		MetricID:    "gld.ass.volume",
		TriggeredAt: time.Now().Add(30 * time.Minute),
	}, nil)
	if !ok {
		t.Error("expected same-entity alert within window to coalesce")
	}
}

func TestClusterer_SameEntityOutsideWindow(t *testing.T) {
	c := NewClusterer(DefaultClusteringConfig())
	ctx := context.Background()
	cluster := NewCluster(model.Alert{
		ID:          "a1",
		MetricID:    "gld.ass.price",
		Severity:    model.SeverityWarning,
		TriggeredAt: time.Now().Add(-2 * time.Hour),
	})

	// Same entity, outside 1h window
	ok := c.CanCoalesce(ctx, cluster, model.Alert{
		MetricID:    "gld.ass.volume",
		TriggeredAt: time.Now(),
	}, nil)
	if ok {
		t.Error("expected same-entity alert outside window to NOT coalesce")
	}
}

func TestClusterer_CrossEntityCooccurrence(t *testing.T) {
	c := NewClusterer(DefaultClusteringConfig())
	ctx := context.Background()
	cluster := NewCluster(model.Alert{
		ID:          "a1",
		MetricID:    "gld.ass.price",
		Severity:    model.SeverityWarning,
		TriggeredAt: time.Now(),
	})

	// Different entity, within co-occurrence window (legacy path: relatedEntities list)
	ok := c.CanCoalesce(ctx, cluster, model.Alert{
		MetricID:    "btc.ass.price",
		TriggeredAt: time.Now().Add(2 * time.Hour),
	}, []string{"btc"})
	if !ok {
		t.Error("expected cross-entity co-occurrence within window to coalesce")
	}
}

func TestClusterer_CapacityLimit(t *testing.T) {
	c := NewClusterer(DefaultClusteringConfig())
	ctx := context.Background()
	cluster := NewCluster(model.Alert{
		ID:          "a1",
		MetricID:    "gld.ass.price",
		Severity:    model.SeverityWarning,
		TriggeredAt: time.Now(),
	})

	// Fill to max
	for i := 0; i < DefaultClusteringConfig().MaxAlertsPerCluster; i++ {
		cluster.Alerts = append(cluster.Alerts, "fill")
	}

	ok := c.CanCoalesce(ctx, cluster, model.Alert{
		MetricID:    "gld.ass.volume",
		TriggeredAt: time.Now().Add(5 * time.Minute),
	}, nil)
	if ok {
		t.Error("expected cluster at max capacity to reject new alerts")
	}
}

func TestAddAlertToCluster_UpdatesSeverity(t *testing.T) {
	c := NewClusterer(DefaultClusteringConfig())
	ctx := context.Background()
	cluster := NewCluster(model.Alert{
		ID:          "a1",
		MetricID:    "gld.ass.price",
		Severity:    model.SeverityInfo,
		TriggeredAt: time.Now(),
	})

	c.AddAlertToCluster(ctx, &cluster, model.Alert{
		ID:          "a2",
		MetricID:    "gld.ass.volume",
		Severity:    model.SeverityWarning,
		TriggeredAt: time.Now().Add(5 * time.Minute),
	})

	if cluster.MaxSeverity != model.SeverityWarning {
		t.Errorf("expected max severity Warning, got %s", cluster.MaxSeverity)
	}
	if !cluster.Coalesced {
		t.Error("expected cluster to be marked coalesced")
	}
	if cluster.TriggerCount != 2 {
		t.Errorf("expected trigger count 2, got %d", cluster.TriggerCount)
	}
	if len(cluster.MergeLog) != 1 {
		t.Errorf("expected 1 merge log entry, got %d", len(cluster.MergeLog))
	}
}

func TestGate_CooldownSuppresses(t *testing.T) {
	g := NewGate(DefaultGateConfig())
	cluster := NewCluster(model.Alert{
		ID:          "a1",
		MetricID:    "gld.ass.price",
		Severity:    model.SeverityWarning,
		TriggeredAt: time.Now(),
	})
	cluster.LastTriggered = time.Now()

	// First evaluation passes
	d := g.Evaluate(cluster, 1)
	if !d.ShouldResearch {
		t.Fatal("expected first evaluation to pass research gate")
	}

	// Second evaluation within cooldown suppressed
	cluster2 := NewCluster(model.Alert{
		ID:          "a2",
		MetricID:    "gld.ass.volume",
		Severity:    model.SeverityWarning,
		TriggeredAt: time.Now().Add(5 * time.Minute),
	})
	cluster2.LastTriggered = time.Now().Add(5 * time.Minute)
	d2 := g.Evaluate(cluster2, 1)
	if d2.ShouldResearch {
		t.Error("expected research suppressed during cooldown")
	}
	if d2.CooldownUntil == nil {
		t.Error("expected cooldown_until to be set")
	}
}

func TestGate_SeverityThreshold(t *testing.T) {
	// Default threshold is Info, so info-severity should trigger research.
	g := NewGate(DefaultGateConfig())
	cluster := NewCluster(model.Alert{
		ID:          "a1",
		MetricID:    "gld.ass.price",
		Severity:    model.SeverityInfo,
		TriggeredAt: time.Now(),
	})

	d := g.Evaluate(cluster, 1)
	if !d.ShouldResearch {
		t.Error("expected info-severity cluster to pass default Info threshold")
	}

	// With Warning threshold, info severity should NOT trigger research.
	g2 := NewGate(GateConfig{
		Cooldown:               30 * time.Minute,
		MinSeverityForResearch: model.SeverityWarning,
		CoalesceBoost:          false,
	})
	d2 := g2.Evaluate(cluster, 1)
	if d2.ShouldResearch {
		t.Error("expected info severity to NOT pass warning threshold")
	}
}

func TestGate_CoalesceBoost(t *testing.T) {
	g := NewGate(DefaultGateConfig())
	cluster := NewCluster(model.Alert{
		ID:          "a1",
		MetricID:    "gld.ass.price",
		Severity:    model.SeverityInfo,
		TriggeredAt: time.Now(),
	})
	cluster.Coalesced = true

	d := g.Evaluate(cluster, 1)
	if d.Severity != model.SeverityWarning {
		t.Errorf("expected coalesce boost Info->Warning, got %s", d.Severity)
	}
}

func TestGate_CrossMetricConfirmation(t *testing.T) {
	g := NewGate(GateConfig{
		Cooldown:                30 * time.Minute,
		CrossMetricConfirmation: 2,
		MinSeverityForResearch:  model.SeverityInfo,
		CoalesceBoost:           false,
	})
	cluster := NewCluster(model.Alert{
		ID:          "a1",
		MetricID:    "gld.ass.price",
		Severity:    model.SeverityWarning,
		TriggeredAt: time.Now(),
	})

	// Single metric, needs 2
	d := g.Evaluate(cluster, 1)
	if d.ShouldResearch {
		t.Error("expected single metric to fail 2-metric confirmation")
	}

	// Two metrics, meets threshold
	d2 := g.Evaluate(cluster, 2)
	if !d2.ShouldResearch {
		t.Error("expected 2 metrics to satisfy confirmation")
	}
}

func TestSeverityRanking(t *testing.T) {
	if !isHigherSeverity(model.SeverityWarning, model.SeverityInfo) {
		t.Error("warning > info")
	}
	if !isHigherSeverity(model.SeverityCritical, model.SeverityWarning) {
		t.Error("critical > warning")
	}
	if isHigherSeverity(model.SeverityInfo, model.SeverityWarning) {
		t.Error("info is NOT > warning")
	}
}

func TestSeverityBump(t *testing.T) {
	cases := []struct {
		input    model.Severity
		expected model.Severity
	}{
		{model.SeverityInfo, model.SeverityWarning},
		{model.SeverityWarning, model.SeverityCritical},
		{model.SeverityCritical, model.SeverityCritical},
	}
	for _, c := range cases {
		got := bumpSeverity(c.input)
		if got != c.expected {
			t.Errorf("bumpSeverity(%s) = %s, want %s", c.input, got, c.expected)
		}
	}
}

func TestPrimaryEntity(t *testing.T) {
	cases := []struct {
		metricID string
		want     string
	}{
		{"gld.ass.price", "gld"},
		{"btc.ass.hash_rate", "btc"},
		{"us.mkt.cpi", "us"},
		{"metric-no-entity", "metric-no-entity"},
	}
	for _, c := range cases {
		got := primaryEntity(c.metricID)
		if got != c.want {
			t.Errorf("primaryEntity(%q) = %q, want %q", c.metricID, got, c.want)
		}
	}
}

func TestSortClustersBySeverity(t *testing.T) {
	now := time.Now()
	clusters := []EventCluster{
		{ID: "a", MaxSeverity: model.SeverityInfo, FirstTriggered: now.Add(1 * time.Hour)},
		{ID: "b", MaxSeverity: model.SeverityCritical, FirstTriggered: now},
		{ID: "c", MaxSeverity: model.SeverityWarning, FirstTriggered: now},
		{ID: "d", MaxSeverity: model.SeverityCritical, FirstTriggered: now.Add(-1 * time.Hour)},
	}
	SortClustersBySeverity(clusters)

	expected := []string{"d", "b", "c", "a"}
	for i, c := range clusters {
		if c.ID != expected[i] {
			t.Errorf("position %d: got %s, want %s", i, c.ID, expected[i])
		}
	}
}

// ---- New tests for tightened clustering logic ----

func TestClusterer_RejectsOutOfOrderAlert(t *testing.T) {
	c := NewClusterer(DefaultClusteringConfig())
	ctx := context.Background()
	base := time.Now()
	cluster := NewCluster(model.Alert{
		ID:          "a1",
		MetricID:    "gld.ass.price",
		Severity:    model.SeverityWarning,
		TriggeredAt: base,
	})

	// Same cluster's last triggered is at base; candidate triggered BEFORE that.
	ok := c.CanCoalesce(ctx, cluster, model.Alert{
		ID:          "a0",
		MetricID:    "gld.ass.volume",
		TriggeredAt: base.Add(-1 * time.Minute),
	}, nil)
	if ok {
		t.Error("expected out-of-order alert (before cluster last) to be rejected")
	}
}

func TestClusterer_RejectsNegativeTimeDelta(t *testing.T) {
	c := NewClusterer(DefaultClusteringConfig())
	ctx := context.Background()
	base := time.Now()
	cluster := NewCluster(model.Alert{
		ID:          "a1",
		MetricID:    "gld.ass.price",
		Severity:    model.SeverityWarning,
		TriggeredAt: base,
	})

	// Candidate triggered before the cluster's first trigger
	ok := c.CanCoalesce(ctx, cluster, model.Alert{
		ID:          "a0",
		MetricID:    "gld.ass.volume",
		TriggeredAt: base.Add(-2 * time.Hour),
	}, nil)
	if ok {
		t.Error("expected alert triggered before cluster FIRST to be rejected (negative delta)")
	}
}

func TestMergeEntry_RecordedOnCoalesce(t *testing.T) {
	c := NewClusterer(DefaultClusteringConfig())
	ctx := context.Background()
	cluster := NewCluster(model.Alert{
		ID:          "a1",
		MetricID:    "gld.ass.price",
		Severity:    model.SeverityInfo,
		TriggeredAt: time.Now(),
	})

	c.AddAlertToCluster(ctx, &cluster, model.Alert{
		ID:          "a2",
		MetricID:    "gld.ass.volume",
		Severity:    model.SeverityInfo,
		TriggeredAt: time.Now().Add(5 * time.Minute),
	})

	if len(cluster.MergeLog) != 1 {
		t.Fatalf("expected 1 merge log entry, got %d", len(cluster.MergeLog))
	}
	entry := cluster.MergeLog[0]
	if entry.AlertID != "a2" {
		t.Errorf("merge log alert ID = %q, want a2", entry.AlertID)
	}
	if entry.Reason == "" {
		t.Error("merge log reason should not be empty")
	}
	if !entry.TriggeredAt.Equal(cluster.MergeLog[0].TriggeredAt) {
		t.Error("merge log triggered_at mismatch")
	}
}

// in-memory RelationReader mock for testing
type mockRelationReader struct {
	rels map[string]bool // key: "src->tgt"
}

func (m *mockRelationReader) HasAcceptedRelation(_ context.Context, src, tgt string) (bool, error) {
	return m.rels[src+"->"+tgt], nil
}

// in-memory EntityResolver mock for testing
type mockEntityResolver struct {
	metricToEntity map[string]string
}

func (m *mockEntityResolver) MetricToEntity(_ context.Context, metricID string) (string, bool, error) {
	eid, ok := m.metricToEntity[metricID]
	return eid, ok, nil
}

func TestClusterer_CrossEntityRequiresRelation(t *testing.T) {
	resolver := &mockEntityResolver{
		metricToEntity: map[string]string{
			"gld.ass.price": "gld",
			"btc.ass.price": "btc",
		},
	}
	relReader := &mockRelationReader{
		rels: map[string]bool{"gld->btc": true},
	}

	cfg := DefaultClusteringConfig()
	cfg.EntityResolver = resolver
	cfg.RelationReader = relReader
	c := NewClusterer(cfg)
	ctx := context.Background()

	cluster := NewClusterWithContext(ctx, model.Alert{
		ID:          "a1",
		MetricID:    "gld.ass.price",
		Severity:    model.SeverityWarning,
		TriggeredAt: time.Now(),
	}, resolver)

	// Cross-entity with accepted relation
	ok := c.CanCoalesce(ctx, cluster, model.Alert{
		ID:          "a2",
		MetricID:    "btc.ass.price",
		TriggeredAt: time.Now().Add(2 * time.Hour),
	}, nil)
	if !ok {
		t.Error("expected cross-entity alert WITH accepted relation to coalesce")
	}

	// Cross-entity without accepted relation
	relReaderNoMatch := &mockRelationReader{
		rels: map[string]bool{}, // no relations
	}
	cfg2 := DefaultClusteringConfig()
	cfg2.EntityResolver = resolver
	cfg2.RelationReader = relReaderNoMatch
	c2 := NewClusterer(cfg2)
	ok2 := c2.CanCoalesce(ctx, cluster, model.Alert{
		ID:          "a3",
		MetricID:    "btc.ass.price",
		TriggeredAt: time.Now().Add(2 * time.Hour),
	}, nil)
	if ok2 {
		t.Error("expected cross-entity alert WITHOUT accepted relation to be rejected")
	}
}

func TestNewClusterWithContext_ResolverOverridesHeuristic(t *testing.T) {
	ctx := context.Background()
	resolver := &mockEntityResolver{
		metricToEntity: map[string]string{
			"gld.ass.price": "custom-entity-id",
		},
	}

	cluster := NewClusterWithContext(ctx, model.Alert{
		ID:          "a1",
		MetricID:    "gld.ass.price",
		Severity:    model.SeverityInfo,
		TriggeredAt: time.Now(),
	}, resolver)

	if cluster.PrimaryEntity != "custom-entity-id" {
		t.Errorf("expected PrimaryEntity 'custom-entity-id', got %q", cluster.PrimaryEntity)
	}
}

func TestResolveCanonicalEntity(t *testing.T) {
	ctx := context.Background()
	resolver := &mockEntityResolver{
		metricToEntity: map[string]string{
			"gld.ass.price": "gld-entity",
		},
	}

	// With resolver found
	eid, err := resolveCanonicalEntity(ctx, "gld.ass.price", resolver)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if eid != "gld-entity" {
		t.Errorf("expected 'gld-entity', got %q", eid)
	}

	// With resolver not found — fallback to heuristic
	eid2, err := resolveCanonicalEntity(ctx, "unknown.metric.id", resolver)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if eid2 != "unknown" {
		t.Errorf("expected 'unknown' from heuristic fallback, got %q", eid2)
	}

	// Nil resolver — pure heuristic
	eid3, err := resolveCanonicalEntity(ctx, "foo.bar.baz", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if eid3 != "foo" {
		t.Errorf("expected 'foo', got %q", eid3)
	}
}
