package classification

import (
	"testing"
	"time"

	"capital_observatory/pkg/model"
)

func TestClusterer_SameEntityWithinWindow(t *testing.T) {
	c := NewClusterer(DefaultClusteringConfig())
	cluster := NewCluster(model.Alert{
		ID:          "a1",
		MetricID:    "gld.ass.price",
		Severity:    model.SeverityWarning,
		TriggeredAt: time.Now(),
	})

	// Same entity, within 1h window
	ok := c.CanCoalesce(cluster, model.Alert{
		MetricID:    "gld.ass.volume",
		TriggeredAt: time.Now().Add(30 * time.Minute),
	}, nil)
	if !ok {
		t.Error("expected same-entity alert within window to coalesce")
	}
}

func TestClusterer_SameEntityOutsideWindow(t *testing.T) {
	c := NewClusterer(DefaultClusteringConfig())
	cluster := NewCluster(model.Alert{
		ID:          "a1",
		MetricID:    "gld.ass.price",
		Severity:    model.SeverityWarning,
		TriggeredAt: time.Now().Add(-2 * time.Hour),
	})

	// Same entity, outside 1h window
	ok := c.CanCoalesce(cluster, model.Alert{
		MetricID:    "gld.ass.volume",
		TriggeredAt: time.Now(),
	}, nil)
	if ok {
		t.Error("expected same-entity alert outside window to NOT coalesce")
	}
}

func TestClusterer_CrossEntityCooccurrence(t *testing.T) {
	c := NewClusterer(DefaultClusteringConfig())
	cluster := NewCluster(model.Alert{
		ID:          "a1",
		MetricID:    "gld.ass.price",
		Severity:    model.SeverityWarning,
		TriggeredAt: time.Now(),
	})

	// Different entity, within co-occurrence window
	ok := c.CanCoalesce(cluster, model.Alert{
		MetricID:    "btc.ass.price",
		TriggeredAt: time.Now().Add(2 * time.Hour),
	}, []string{"btc"})
	if !ok {
		t.Error("expected cross-entity co-occurrence within window to coalesce")
	}
}

func TestClusterer_CapacityLimit(t *testing.T) {
	c := NewClusterer(DefaultClusteringConfig())
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

	ok := c.CanCoalesce(cluster, model.Alert{
		MetricID:    "gld.ass.volume",
		TriggeredAt: time.Now().Add(5 * time.Minute),
	}, nil)
	if ok {
		t.Error("expected cluster at max capacity to reject new alerts")
	}
}

func TestAddAlertToCluster_UpdatesSeverity(t *testing.T) {
	c := NewClusterer(DefaultClusteringConfig())
	cluster := NewCluster(model.Alert{
		ID:          "a1",
		MetricID:    "gld.ass.price",
		Severity:    model.SeverityInfo,
		TriggeredAt: time.Now(),
	})

	c.AddAlertToCluster(&cluster, model.Alert{
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
