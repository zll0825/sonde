// outbox.go — 领域事件出箱工作器：轮询 event_outbox 待派发事件，按事件
// 类型分发给注册的处理器（通知推送、研究组装等异步消费方）；处理失败按
// 重试预算重投，超限标记为 failed。
package alert

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
)

// OutboxWorker polls the event_outbox table and dispatches events.
// Tier 1 events (alerts, plugin commands) MUST go through outbox for at-least-once
// delivery to async consumers (research assembly, notification hooks).
type OutboxWorker struct {
	store        OutboxStore
	handlers     map[string]EventHandler
	pollInterval time.Duration
}

// OutboxStore is the minimal persistence interface for the outbox.
type OutboxStore interface {
	PickPending(ctx context.Context, limit int) ([]OutboxEvent, error)
	MarkDispatched(ctx context.Context, id int) error
	MarkFailed(ctx context.Context, id int, errMsg string) error
}

// EventHandler processes a single outbox event by type.
type EventHandler func(ctx context.Context, event OutboxEvent) error

// OutboxEvent is a row from the event_outbox table.
type OutboxEvent struct {
	ID        int
	EventType string
	Payload   []byte
	Attempts  int
	CreatedAt time.Time
}

// NewOutboxWorker creates a worker that polls at the given interval.
func NewOutboxWorker(store OutboxStore, interval time.Duration) *OutboxWorker {
	return &OutboxWorker{
		store:        store,
		handlers:     make(map[string]EventHandler),
		pollInterval: interval,
	}
}

// RegisterHandler sets the handler for a given event type.
func (w *OutboxWorker) RegisterHandler(eventType string, h EventHandler) {
	w.handlers[eventType] = h
}

// Run polls the outbox at the configured interval until ctx is canceled.
func (w *OutboxWorker) Run(ctx context.Context, batchSize int) {
	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.Tick(ctx, batchSize)
		}
	}
}

// Tick processes one batch of pending events. Dispatches them in insertion order.
// An event with no registered handler is marked failed, never dispatched:
// silently dropping a Tier-1 event would violate the no-silent-loss constraint.
func (w *OutboxWorker) Tick(ctx context.Context, batchSize int) (dispatched, failed int) {
	events, err := w.store.PickPending(ctx, batchSize)
	if err != nil {
		log.Error().Err(err).Msg("outbox pick failed")
		return 0, 0
	}

	for _, ev := range events {
		handler, ok := w.handlers[ev.EventType]
		if !ok {
			log.Warn().Int("event_id", ev.ID).Str("type", ev.EventType).Msg("no handler registered for outbox event")
			_ = w.store.MarkFailed(ctx, ev.ID, "no handler registered for event type "+ev.EventType)
			failed++
			continue
		}
		if err := handler(ctx, ev); err != nil {
			log.Error().Err(err).Int("event_id", ev.ID).Str("type", ev.EventType).Msg("outbox handler failed")
			_ = w.store.MarkFailed(ctx, ev.ID, err.Error())
			failed++
			continue
		}
		if err := w.store.MarkDispatched(ctx, ev.ID); err != nil {
			log.Error().Err(err).Int("event_id", ev.ID).Msg("mark dispatched failed")
			failed++
			continue
		}
		dispatched++
	}
	return dispatched, failed
}
