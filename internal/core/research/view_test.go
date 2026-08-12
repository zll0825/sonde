package research

import (
	"context"
	"testing"
	"time"

	"capital_observatory/pkg/model"
)

// mockViewStore 用于测试的模拟存储。
type mockViewStore struct {
	observations []model.Observation
	entities     map[string]*model.Entity
	relations    []model.Relation
}

func newMockViewStore() *mockViewStore {
	return &mockViewStore{
		entities: make(map[string]*model.Entity),
	}
}

func (m *mockViewStore) GetObservations(ctx context.Context, metricUID string, since, until time.Time, limit int) ([]model.Observation, error) {
	var result []model.Observation
	for _, o := range m.observations {
		if o.Time.After(since) && o.Time.Before(until) {
			result = append(result, o)
			if len(result) >= limit {
				break
			}
		}
	}
	return result, nil
}

func (m *mockViewStore) GetEntityByID(ctx context.Context, entityID string) (*model.Entity, error) {
	return m.entities[entityID], nil
}

func (m *mockViewStore) GetRelatedEntities(ctx context.Context, entityID string) ([]model.Relation, error) {
	return m.relations, nil
}

func (m *mockViewStore) SaveSnapshot(ctx context.Context, snapshot model.ResearchSnapshot) error {
	return nil
}

func (m *mockViewStore) MetricUIDForEntity(_ context.Context, entityID string) (string, bool, error) {
	if entityID == "" {
		return "", false, nil
	}
	return "mtr_" + entityID, true, nil
}

func TestBuildTimeline_BasicFlow(t *testing.T) {
	store := newMockViewStore()
	now := time.Now()

	// 添加观测
	store.observations = []model.Observation{
		{MetricID: "gld.ass.price", Time: now.Add(-3 * time.Hour), Value: 2150.5},
		{MetricID: "gld.ass.price", Time: now.Add(-2 * time.Hour), Value: 2155.0},
		{MetricID: "gld.ass.price", Time: now.Add(-1 * time.Hour), Value: 2160.3},
	}

	// 添加实体和关系
	store.entities["gld"] = &model.Entity{ID: "gld", Name: "GLD", Namespace: "gld"}
	store.relations = []model.Relation{
		{SourceID: "gld", TargetID: "btc", RelationType: "correlates"},
	}
	store.entities["btc"] = &model.Entity{ID: "btc", Name: "BTC", Namespace: "btc"}

	vb := NewViewBuilder(store)
	timeline, err := vb.BuildTimeline(context.Background(), "gld", now.Add(-24*time.Hour), now, 60)
	if err != nil {
		t.Fatalf("BuildTimeline: %v", err)
	}
	if len(timeline.Points) == 0 {
		t.Fatal("expected timeline points")
	}
	if timeline.EntityID != "gld" {
		t.Errorf("EntityID = %q, want gld", timeline.EntityID)
	}
}

func TestBuildTimeline_SortsByTime(t *testing.T) {
	store := newMockViewStore()
	now := time.Now()

	// 逆序添加
	store.observations = []model.Observation{
		{MetricID: "m1", Time: now.Add(-1 * time.Hour), Value: 100},
		{MetricID: "m1", Time: now.Add(-3 * time.Hour), Value: 80},
		{MetricID: "m1", Time: now.Add(-2 * time.Hour), Value: 90},
	}
	store.entities["e1"] = &model.Entity{ID: "e1", Name: "E1", Namespace: "n1"}

	vb := NewViewBuilder(store)
	timeline, err := vb.BuildTimeline(context.Background(), "e1", now.Add(-4*time.Hour), now, 60)
	if err != nil {
		t.Fatalf("BuildTimeline: %v", err)
	}

	// 检查是否按时间排序
	for i := 1; i < len(timeline.Points); i++ {
		if timeline.Points[i].Timestamp.Before(timeline.Points[i-1].Timestamp) {
			t.Errorf("points not sorted: %v before %v",
				timeline.Points[i-1].Timestamp, timeline.Points[i].Timestamp)
		}
	}
}

func TestBuildMetricOverlay(t *testing.T) {
	store := newMockViewStore()
	now := time.Now()

	store.observations = []model.Observation{
		{MetricID: "gld.ass.price", Time: now.Add(-2 * time.Hour), Value: 2150},
		{MetricID: "gld.ass.volume", Time: now.Add(-2 * time.Hour), Value: 5e6},
	}

	vb := NewViewBuilder(store)
	overlays, err := vb.BuildMetricOverlay(context.Background(), []string{"gld.ass.price", "gld.ass.volume"}, now.Add(-6*time.Hour), now, 60)
	if err != nil {
		t.Fatalf("BuildMetricOverlay: %v", err)
	}
	if len(overlays) != 2 {
		t.Errorf("expected 2 overlays, got %d", len(overlays))
	}
}

func TestBuildRelationGraph_SingleHop(t *testing.T) {
	store := newMockViewStore()

	// 设置关系
	store.entities = map[string]*model.Entity{
		"gld": {ID: "gld", Name: "GLD", Namespace: "gld"},
		"btc": {ID: "btc", Name: "BTC", Namespace: "btc"},
		"us":  {ID: "us", Name: "US", Namespace: "us"},
	}
	store.relations = []model.Relation{
		{SourceID: "gld", TargetID: "btc", RelationType: "correlates", Direction: "forward"},
		{SourceID: "gld", TargetID: "us", RelationType: "influenced_by", Direction: "forward"},
	}

	vb := NewViewBuilder(store)
	graph, err := vb.BuildRelationGraph(context.Background(), "gld", 1)
	if err != nil {
		t.Fatalf("BuildRelationGraph: %v", err)
	}

	// 应该至少有 3 个节点 (gld, btc, us)
	if len(graph.Nodes) < 3 {
		t.Errorf("expected 3+ nodes, got %d", len(graph.Nodes))
	}
	// 应该至少有 2 条边
	if len(graph.Edges) < 2 {
		t.Errorf("expected 2+ edges, got %d", len(graph.Edges))
	}
}

func TestBuildRelationGraph_VisitsOnce(t *testing.T) {
	store := newMockViewStore()

	// 避免循环
	store.entities = map[string]*model.Entity{
		"a": {ID: "a", Name: "A", Namespace: "n"},
	}
	store.relations = []model.Relation{
		{SourceID: "a", TargetID: "a", RelationType: "self"}, // 自环
	}

	vb := NewViewBuilder(store)
	graph, err := vb.BuildRelationGraph(context.Background(), "a", 2)
	if err != nil {
		t.Fatalf("BuildRelationGraph: %v", err)
	}
	// 自环只应访问一次
	if len(graph.Nodes) != 1 {
		t.Errorf("expected 1 node (visited once), got %d", len(graph.Nodes))
	}
}

func TestUniqueEntityIDs(t *testing.T) {
	relations := []model.Relation{
		{SourceID: "gld", TargetID: "btc"},
		{SourceID: "gld", TargetID: "us"},
		{SourceID: "btc", TargetID: "us"}, // duplicate target
	}
	result := uniqueEntityIDs("gld", relations)
	if len(result) != 2 {
		t.Errorf("expected 2 unique, got %d", len(result))
	}
	seen := make(map[string]bool)
	for _, id := range result {
		if seen[id] {
			t.Errorf("duplicate id %q", id)
		}
		seen[id] = true
	}
}
