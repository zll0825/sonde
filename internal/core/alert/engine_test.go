package alert

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"sonde/pkg/model"
)

// fakeAlertStore records calls so tests can assert dedup + outbox behavior.
type fakeAlertStore struct {
	active           map[string]*model.Alert
	getErr           error
	created          []model.Alert
	events           [][]PendingEvent
	createErr        error
	resolvedKeys     []string
	deduplicatedKeys []string
	dedupErr         error
}

func newFakeAlertStore() *fakeAlertStore {
	return &fakeAlertStore{active: make(map[string]*model.Alert)}
}

func (f *fakeAlertStore) CreateAlertWithEvents(_ context.Context, alert model.Alert, events []PendingEvent) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.created = append(f.created, alert)
	f.events = append(f.events, events)
	return nil
}

func (f *fakeAlertStore) ResolveAlert(_ context.Context, dedupKey string, _ time.Time) error {
	f.resolvedKeys = append(f.resolvedKeys, dedupKey)
	return nil
}

func (f *fakeAlertStore) GetActiveAlert(_ context.Context, dedupKey string) (*model.Alert, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.active[dedupKey], nil
}

func (f *fakeAlertStore) RecordDeduplication(_ context.Context, dedupKey string, _ time.Time) error {
	if f.dedupErr != nil {
		return f.dedupErr
	}
	f.deduplicatedKeys = append(f.deduplicatedKeys, dedupKey)
	return nil
}

func (f *fakeAlertStore) GetActiveAlertsByMetric(_ context.Context, metricID string) ([]model.Alert, error) {
	var result []model.Alert
	for _, a := range f.active {
		if a != nil && a.MetricID == metricID && a.Status == "active" {
			result = append(result, *a)
		}
	}
	return result, nil
}

func sampleAlert(dedupKey string) model.Alert {
	return model.Alert{
		ID:          "alt_001",
		Title:       "BTC breakout",
		Severity:    model.SeverityWarning,
		MetricID:    "m1",
		RuleID:      42,
		DedupKey:    dedupKey,
		TriggeredAt: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC),
	}
}

func TestHandleTrigger_NewAlertWritesAlertAndOutboxEvent(t *testing.T) {
	store := newFakeAlertStore()
	engine := NewEngine(store)
	alert := sampleAlert("m1|42")

	err := engine.HandleTrigger(context.Background(), alert)

	if err != nil {
		t.Fatalf("HandleTrigger returned error: %v", err)
	}
	if len(store.created) != 1 {
		t.Fatalf("created %d alerts, want 1", len(store.created))
	}
	if len(store.events) != 1 || len(store.events[0]) != 2 {
		t.Fatalf("events = %v, want one notification and one research event", store.events)
	}
	if store.events[0][0].EventType != EventTypeAlertTriggered {
		t.Errorf("event type = %q, want %q", store.events[0][0].EventType, EventTypeAlertTriggered)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(store.events[0][0].Payload, &payload); err != nil {
		t.Fatalf("payload is not valid JSON: %v", err)
	}
	if payload["alert_id"] != "alt_001" {
		t.Errorf("payload alert_id = %v, want alt_001", payload["alert_id"])
	}
	researchEvent := store.events[0][1]
	if researchEvent.EventType != "research.requested" {
		t.Errorf("research event type = %q, want research.requested", researchEvent.EventType)
	}
	if researchEvent.DedupKey == nil || *researchEvent.DedupKey != "alt_001" {
		t.Errorf("research dedup key = %v, want alt_001", researchEvent.DedupKey)
	}
}

func TestHandleTrigger_ActiveAlertDeduplicates(t *testing.T) {
	store := newFakeAlertStore()
	existing := sampleAlert("m1|42")
	store.active["m1|42"] = &existing
	engine := NewEngine(store)

	err := engine.HandleTrigger(context.Background(), sampleAlert("m1|42"))

	if err != nil {
		t.Fatalf("HandleTrigger returned error: %v", err)
	}
	if len(store.created) != 0 {
		t.Errorf("created %d alerts for a deduplicated trigger, want 0", len(store.created))
	}
	if len(store.deduplicatedKeys) != 1 || store.deduplicatedKeys[0] != "m1|42" {
		t.Errorf("dedup audit keys = %v, want [m1|42]", store.deduplicatedKeys)
	}
}

func TestHandleTrigger_DedupAuditErrorPropagates(t *testing.T) {
	store := newFakeAlertStore()
	existing := sampleAlert("m1|42")
	store.active["m1|42"] = &existing
	store.dedupErr = errors.New("db down")

	err := NewEngine(store).HandleTrigger(context.Background(), sampleAlert("m1|42"))
	if err == nil {
		t.Fatal("expected durable dedup audit failure")
	}
}

func TestHandleTrigger_InsertRaceRecordsDedup(t *testing.T) {
	store := newFakeAlertStore()
	store.createErr = ErrDuplicateAlert

	err := NewEngine(store).HandleTrigger(context.Background(), sampleAlert("m1|42"))
	if err != nil {
		t.Fatalf("HandleTrigger: %v", err)
	}
	if len(store.deduplicatedKeys) != 1 || store.deduplicatedKeys[0] != "m1|42" {
		t.Errorf("dedup audit keys = %v, want [m1|42]", store.deduplicatedKeys)
	}
}

func TestHandleTrigger_ObserveWritesAlertWithoutOutbox(t *testing.T) {
	store := newFakeAlertStore()
	engine := NewEngine(store)
	alert := sampleAlert("m1|42")
	alert.Mode = model.RuleModeObserve

	err := engine.HandleTrigger(context.Background(), alert)
	if err != nil {
		t.Fatalf("HandleTrigger returned error: %v", err)
	}
	if len(store.created) != 1 {
		t.Fatalf("created %d alerts, want 1", len(store.created))
	}
	if store.created[0].Mode != model.RuleModeObserve {
		t.Errorf("alert.mode = %q, want observe", store.created[0].Mode)
	}
	if len(store.events) != 1 || len(store.events[0]) != 0 {
		t.Fatalf("observe outbox events = %v, want empty slice", store.events)
	}
}

func TestHandleTrigger_LiveInfoStillWritesOutbox(t *testing.T) {
	store := newFakeAlertStore()
	engine := NewEngine(store)
	alert := sampleAlert("m1|42")
	alert.Severity = model.SeverityInfo
	alert.Mode = model.RuleModeLive

	err := engine.HandleTrigger(context.Background(), alert)
	if err != nil {
		t.Fatalf("HandleTrigger returned error: %v", err)
	}
	if len(store.events) != 1 || len(store.events[0]) != 2 {
		t.Fatalf("live+info events = %v, want alert.triggered and research.requested", store.events)
	}
	if store.events[0][0].EventType != EventTypeAlertTriggered {
		t.Errorf("event type = %q, want %q", store.events[0][0].EventType, EventTypeAlertTriggered)
	}
	if store.events[0][1].EventType != "research.requested" {
		t.Errorf("research event type = %q, want research.requested", store.events[0][1].EventType)
	}
}

func TestHandleTrigger_StoreLookupErrorPropagates(t *testing.T) {
	store := newFakeAlertStore()
	store.getErr = errors.New("db down")
	engine := NewEngine(store)

	err := engine.HandleTrigger(context.Background(), sampleAlert("m1|42"))

	if err == nil {
		t.Fatal("expected error when dedup lookup fails, got nil")
	}
	if len(store.created) != 0 {
		t.Errorf("created %d alerts despite lookup failure, want 0", len(store.created))
	}
}

func TestResolve_DelegatesToStore(t *testing.T) {
	store := newFakeAlertStore()
	engine := NewEngine(store)

	err := engine.Resolve(context.Background(), "m1|42")

	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if len(store.resolvedKeys) != 1 || store.resolvedKeys[0] != "m1|42" {
		t.Errorf("resolved keys = %v, want [m1|42]", store.resolvedKeys)
	}
}

func TestAutoResolveStaleAlerts_ResolvesStaleFiresKept(t *testing.T) {
	store := newFakeAlertStore()
	engine := NewEngine(store)

	// Two active alerts on the same metric for different rules.
	rule42 := sampleAlert("m1|42")
	rule42.RuleID = 42
	rule42.Status = "active"
	store.active["m1|42"] = &rule42

	rule99 := sampleAlert("m1|99")
	rule99.RuleID = 99
	rule99.Status = "active"
	store.active["m1|99"] = &rule99

	// This evaluation: only rule 42 fired. rule 99 is stale → resolve it.
	firedRuleIDs := map[int]struct{}{42: {}}

	err := engine.AutoResolveStaleAlerts(context.Background(), "m1", firedRuleIDs)
	if err != nil {
		t.Fatalf("AutoResolveStaleAlerts returned error: %v", err)
	}

	// Only rule 99's dedup_key should have been resolved.
	if len(store.resolvedKeys) != 1 {
		t.Fatalf("resolvedKeys = %v, want exactly 1 entry", store.resolvedKeys)
	}
	if store.resolvedKeys[0] != "m1|99" {
		t.Errorf("resolvedKeys = %v, want [m1|99] (rule 42 should stay active)", store.resolvedKeys)
	}
}

func TestAutoResolveStaleAlerts_NoActiveAlertsNoop(t *testing.T) {
	store := newFakeAlertStore()
	engine := NewEngine(store)

	// No active alerts — auto-resolve is a no-op.
	err := engine.AutoResolveStaleAlerts(context.Background(), "m1", map[int]struct{}{42: {}})
	if err != nil {
		t.Fatalf("AutoResolveStaleAlerts returned error: %v", err)
	}
	if len(store.resolvedKeys) != 0 {
		t.Errorf("resolvedKeys = %v, want empty (nothing to resolve)", store.resolvedKeys)
	}
}

func TestAutoResolveStaleAlerts_AllFiredNothingResolved(t *testing.T) {
	store := newFakeAlertStore()
	engine := NewEngine(store)

	rule42 := sampleAlert("m1|42")
	rule42.RuleID = 42
	rule42.Status = "active"
	store.active["m1|42"] = &rule42

	rule99 := sampleAlert("m1|99")
	rule99.RuleID = 99
	rule99.Status = "active"
	store.active["m1|99"] = &rule99

	// Both rules fired → neither should be resolved.
	firedRuleIDs := map[int]struct{}{42: {}, 99: {}}

	err := engine.AutoResolveStaleAlerts(context.Background(), "m1", firedRuleIDs)
	if err != nil {
		t.Fatalf("AutoResolveStaleAlerts returned error: %v", err)
	}
	if len(store.resolvedKeys) != 0 {
		t.Errorf("resolvedKeys = %v, want empty (all rules still firing)", store.resolvedKeys)
	}
}
