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
	observations   []model.Observation
	entity         *model.Entity
	relations      []model.Relation
	observationErr error
	entityErr      error
	relationErr    error
	saveErr        error
	saveCalled     int
	savedSnap      *model.ResearchSnapshot
	queriedUID     string
}

func (m *mockResearchStore) GetObservations(_ context.Context, metricUID string, _, _ time.Time, _ int) ([]model.Observation, error) {
	m.queriedUID = metricUID
	return m.observations, m.observationErr
}

func (m *mockResearchStore) GetEntityByID(_ context.Context, _ string) (*model.Entity, error) {
	return m.entity, m.entityErr
}

func (m *mockResearchStore) GetRelatedEntities(_ context.Context, _ string) ([]model.Relation, error) {
	return m.relations, m.relationErr
}

func (m *mockResearchStore) SaveSnapshot(_ context.Context, snap model.ResearchSnapshot) error {
	m.saveCalled++
	m.savedSnap = &snap
	return m.saveErr
}

func (m *mockResearchStore) MetricUIDForEntity(_ context.Context, entityID string) (string, bool, error) {
	if entityID == "" {
		return "", false, nil
	}
	return "mtr_" + entityID, true, nil
}

func TestAssembler_Assemble_PropagatesTransientStoreFailure(t *testing.T) {
	store := &mockResearchStore{observationErr: errors.New("database unavailable")}
	now := time.Now()
	alert := model.Alert{
		ID:        "alt_retry",
		MetricID:  "metric.retry",
		WindowEnd: &now,
		Evidence:  []byte(`{"metric_uid":"mtr_retry"}`),
	}

	_, err := NewAssembler(store).Assemble(context.Background(), alert)
	if err == nil || !errors.Is(err, store.observationErr) {
		t.Fatalf("Assemble() error = %v, want wrapped transient error", err)
	}
}

func TestAssembler_Assemble_RejectsMalformedEvidence(t *testing.T) {
	alert := model.Alert{ID: "alt_bad", Evidence: []byte(`{"broken"`)}

	if _, err := NewAssembler(&mockResearchStore{}).Assemble(context.Background(), alert); err == nil {
		t.Fatal("Assemble() accepted malformed evidence")
	}
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

func TestAssembler_Assemble_QueriesObservationsByEvidenceUID(t *testing.T) {
	now := time.Now()
	ws, we := now.Add(-time.Hour), now
	store := &mockResearchStore{}

	alert := model.Alert{
		ID:          "alt_uid_test",
		MetricID:    "gld.ass.price",
		WindowStart: &ws,
		WindowEnd:   &we,
		Evidence:    []byte(`{"metric_uid":"mtr_abc123","current_value":42}`),
	}
	if _, err := NewAssembler(store).Assemble(context.Background(), alert); err != nil {
		t.Fatalf("Assemble error: %v", err)
	}
	// Observations are keyed by metric_uid — querying with the metric_id
	// silently returns an empty trend (regression test).
	if store.queriedUID != "mtr_abc123" {
		t.Errorf("queried uid = %q, want mtr_abc123 (from evidence)", store.queriedUID)
	}

	// Without uid evidence, fall back to MetricID rather than erroring.
	store2 := &mockResearchStore{}
	alert.Evidence = []byte(`{"current_value":42}`)
	if _, err := NewAssembler(store2).Assemble(context.Background(), alert); err != nil {
		t.Fatalf("Assemble error: %v", err)
	}
	if store2.queriedUID != "gld.ass.price" {
		t.Errorf("fallback uid = %q, want gld.ass.price", store2.queriedUID)
	}
}
