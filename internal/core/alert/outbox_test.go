package alert

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeOutboxStore serves a fixed batch and records state transitions.
type fakeOutboxStore struct {
	pending    []OutboxEvent
	pickErr    error
	dispatched []int
	failed     map[int]string
}

func newFakeOutboxStore(events ...OutboxEvent) *fakeOutboxStore {
	return &fakeOutboxStore{pending: events, failed: make(map[int]string)}
}

func (f *fakeOutboxStore) PickPending(_ context.Context, limit int) ([]OutboxEvent, error) {
	if f.pickErr != nil {
		return nil, f.pickErr
	}
	if len(f.pending) > limit {
		return f.pending[:limit], nil
	}
	return f.pending, nil
}

func (f *fakeOutboxStore) MarkDispatched(_ context.Context, id int) error {
	f.dispatched = append(f.dispatched, id)
	return nil
}

func (f *fakeOutboxStore) MarkFailed(_ context.Context, id int, errMsg string) error {
	f.failed[id] = errMsg
	return nil
}

func event(id int, eventType string) OutboxEvent {
	return OutboxEvent{ID: id, EventType: eventType, Payload: []byte(`{}`), CreatedAt: time.Now()}
}

func TestTick_DispatchesHandledEvents(t *testing.T) {
	store := newFakeOutboxStore(event(1, "alert.triggered"), event(2, "alert.triggered"))
	worker := NewOutboxWorker(store, time.Second)
	var handled []int
	worker.RegisterHandler("alert.triggered", func(_ context.Context, ev OutboxEvent) error {
		handled = append(handled, ev.ID)
		return nil
	})

	dispatched, failed := worker.Tick(context.Background(), 10)

	if dispatched != 2 || failed != 0 {
		t.Errorf("dispatched=%d failed=%d, want 2/0", dispatched, failed)
	}
	if len(handled) != 2 {
		t.Errorf("handler ran %d times, want 2", len(handled))
	}
	if len(store.dispatched) != 2 {
		t.Errorf("marked dispatched %d events, want 2", len(store.dispatched))
	}
}

func TestTick_HandlerErrorMarksFailed(t *testing.T) {
	store := newFakeOutboxStore(event(1, "alert.triggered"))
	worker := NewOutboxWorker(store, time.Second)
	worker.RegisterHandler("alert.triggered", func(_ context.Context, _ OutboxEvent) error {
		return errors.New("downstream unavailable")
	})

	dispatched, failed := worker.Tick(context.Background(), 10)

	if dispatched != 0 || failed != 1 {
		t.Errorf("dispatched=%d failed=%d, want 0/1", dispatched, failed)
	}
	if _, ok := store.failed[1]; !ok {
		t.Error("event 1 was not marked failed")
	}
	if len(store.dispatched) != 0 {
		t.Error("failed event must not be marked dispatched")
	}
}

func TestTick_NoHandlerIsNotSilentLoss(t *testing.T) {
	// Hard constraint #5: a Tier-1 event with no registered handler must never
	// be marked dispatched — that would silently drop it.
	store := newFakeOutboxStore(event(1, "unknown.type"))
	worker := NewOutboxWorker(store, time.Second)

	dispatched, failed := worker.Tick(context.Background(), 10)

	if dispatched != 0 || failed != 1 {
		t.Errorf("dispatched=%d failed=%d, want 0/1", dispatched, failed)
	}
	if len(store.dispatched) != 0 {
		t.Error("unhandled event was marked dispatched — silent data loss")
	}
	if msg := store.failed[1]; msg == "" {
		t.Error("unhandled event must be marked failed with a reason")
	}
}

func TestTick_PickErrorReturnsZero(t *testing.T) {
	store := newFakeOutboxStore()
	store.pickErr = errors.New("db down")
	worker := NewOutboxWorker(store, time.Second)

	dispatched, failed := worker.Tick(context.Background(), 10)

	if dispatched != 0 || failed != 0 {
		t.Errorf("dispatched=%d failed=%d, want 0/0 on pick error", dispatched, failed)
	}
}

func TestTick_RespectsBatchLimit(t *testing.T) {
	store := newFakeOutboxStore(event(1, "t"), event(2, "t"), event(3, "t"))
	worker := NewOutboxWorker(store, time.Second)
	worker.RegisterHandler("t", func(_ context.Context, _ OutboxEvent) error { return nil })

	dispatched, _ := worker.Tick(context.Background(), 2)

	if dispatched != 2 {
		t.Errorf("dispatched=%d, want 2 (batch limit)", dispatched)
	}
}
