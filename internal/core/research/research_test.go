package research

import (
	"context"
	"errors"
	"testing"
	"time"

	"capital_observatory/pkg/model"
)

// mockResearchStore is an in-memory ResearchStore for unit tests.
type mockResearchStore struct {
	observations []model.Observation
	entity       *model.Entity
	relations    []model.Relation
	saveCalled   int
	savedSnap    *model.ResearchSnapshot
}

func (m *mockResearchStore) GetObservations(_ context.Context, _ string, _, _ time.Time) ([]model.Observation, error) {
	return m.observations, nil
}

func (m *mockResearchStore) GetEntityByID(_ context.Context, _ string) (*model.Entity, error) {
	if m.entity == nil {
		return nil, errors.New("not found")
	}
	return m.entity, nil
}

func (m *mockResearchStore) GetRelatedEntities(_ context.Context, _ string) ([]model.Relation, error) {
	return m.relations, nil
}

func (m *mockResearchStore) SaveSnapshot(_ context.Context, snap model.ResearchSnapshot) error {
	m.saveCalled++
	m.savedSnap = &snap
	return nil
}

func TestAssembler_Assemble_BuildsContext(t *testing.T) {
	now := time.Now()
	store := &mockResearchStore{
		observations: []model.Observation{
			{Time: now.Add(-1 * time.Hour), MetricID: "gld_flow", MetricUID: "mtr_gld", Value: 100.0},
			{Time: now, MetricID: "gld_flow", MetricUID: "mtr_gld", Value: 600.0},
		},
		entity: &model.Entity{
			ID:         "GLD",
			Name:       "GLD",
			Namespace:  "us-etf",
			EntityType: model.EntityTypeAsset,
			Tags:       []string{"gold", "etf"},
		},
		relations: []model.Relation{
			{
				SourceID:     "GLD",
				TargetID:     "ETH",
				RelationType: "tracks",
				Direction:    "forward",
			},
		},
	}
	asm := NewAssembler(store)

	evidence := []byte(`{"current_value":500,"threshold":400,"entity_id":"GLD"}`)
	start := now.Add(-24 * time.Hour)
	alert := model.Alert{
		ID:          "alt_001",
		MetricID:    "gld_flow",
		WindowStart: &start,
		WindowEnd:   &now,
		Evidence:    evidence,
	}

	rc, err := asm.Assemble(context.Background(), alert)
	if err != nil {
		t.Fatalf("Assemble failed: %v", err)
	}

	if rc.AlertID != "alt_001" {
		t.Errorf("AlertID = %q, want alt_001", rc.AlertID)
	}
	if len(rc.RecentTrend) != 2 {
		t.Errorf("RecentTrend length = %d, want 2", len(rc.RecentTrend))
	}
	if rc.CurrentValue != 500 {
		t.Errorf("CurrentValue = %f, want 500", rc.CurrentValue)
	}
	if rc.MetricName != "GLD" {
		t.Errorf("MetricName = %q, want GLD (from entity)", rc.MetricName)
	}
	if len(rc.RelatedEntities) != 1 {
		t.Errorf("RelatedEntities = %d, want 1", len(rc.RelatedEntities))
	}
	if len(rc.Relations) != 1 {
		t.Errorf("Relations = %d, want 1", len(rc.Relations))
	}
}

func TestAssembler_Assemble_NoEntityInEvidence(t *testing.T) {
	store := &mockResearchStore{
		observations: []model.Observation{},
	}
	asm := NewAssembler(store)

	evidence := []byte(`{"current_value":10}`)
	now := time.Now()
	start := now.Add(-time.Hour)
	alert := model.Alert{
		ID:          "alt_002",
		MetricID:    "unknown_metric",
		WindowStart: &start,
		WindowEnd:   &now,
		Evidence:    evidence,
	}

	rc, err := asm.Assemble(context.Background(), alert)
	if err != nil {
		t.Fatalf("Assemble failed: %v", err)
	}
	if rc.MetricName != "" {
		t.Errorf("expected empty MetricName without entity_id, got %q", rc.MetricName)
	}
	if len(rc.RecentTrend) != 0 {
		t.Errorf("expected empty trend, got %d points", len(rc.RecentTrend))
	}
}

func TestAssembler_SaveSnapshot_PersistsContext(t *testing.T) {
	store := &mockResearchStore{}
	asm := NewAssembler(store)

	rc := &ResearchContext{
		AlertID:  "alt_save_test",
		MetricID: "mtr_test",
		Metadata: map[string]interface{}{"key": "val"},
	}

	err := asm.SaveSnapshot(context.Background(), rc)
	if err != nil {
		t.Fatalf("SaveSnapshot failed: %v", err)
	}
	if store.saveCalled != 1 {
		t.Errorf("SaveSnapshot called %d times, want 1", store.saveCalled)
	}
	if store.savedSnap == nil || store.savedSnap.AlertID != "alt_save_test" {
		t.Error("saved snapshot missing correct AlertID")
	}
}
