package alert

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"capital_observatory/pkg/model"
)

// fakeAlertStore records calls so tests can assert dedup + outbox behavior.
type fakeAlertStore struct {
	active       map[string]*model.Alert
	getErr       error
	created      []model.Alert
	eventTypes   []string
	payloads     [][]byte
	createErr    error
	resolvedKeys []string
}

func newFakeAlertStore() *fakeAlertStore {
	return &fakeAlertStore{active: make(map[string]*model.Alert)}
}

func (f *fakeAlertStore) CreateAlertWithEvent(_ context.Context, alert model.Alert, eventType string, payload []byte) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.created = append(f.created, alert)
	f.eventTypes = append(f.eventTypes, eventType)
	f.payloads = append(f.payloads, payload)
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
	if store.eventTypes[0] != EventTypeAlertTriggered {
		t.Errorf("event type = %q, want %q", store.eventTypes[0], EventTypeAlertTriggered)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(store.payloads[0], &payload); err != nil {
		t.Fatalf("payload is not valid JSON: %v", err)
	}
	if payload["alert_id"] != "alt_001" {
		t.Errorf("payload alert_id = %v, want alt_001", payload["alert_id"])
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
