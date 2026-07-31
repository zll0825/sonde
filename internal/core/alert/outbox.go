// Package alert provides outbox worker logic for domain events.
package alert

import (
	"context"
	"encoding/json"
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

// Tick processes one batch of pending events. Dispatches them in insertion order.
func (w *OutboxWorker) Tick(ctx context.Context, batchSize int) (dispatched, failed int) {
	events, err := w.store.PickPending(ctx, batchSize)
	if err != nil {
		log.Error().Err(err).Msg("outbox pick failed")
		return 0, 0
	}

	for _, ev := range events {
		handler, ok := w.handlers[ev.EventType]
		if ok {
			if err := handler(ctx, ev); err != nil {
				log.Error().Err(err).Int("event_id", ev.ID).Str("type", ev.EventType).Msg("outbox handler failed")
				_ = w.store.MarkFailed(ctx, ev.ID, err.Error())
				failed++
				continue
			}
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

// MakeAlertEvent creates a JSON payload for an alert-triggered outbox event.
func MakeAlertEvent(alert interface{ AlertPayload() map[string]interface{} }) ([]byte, error) {
	payload := alert.AlertPayload()
	return json.Marshal(payload)
}

// AlertPayloadEvent is a helper struct for constructing outbox events.
type AlertPayloadEvent struct {
	AlertID   string `json:"alert_id"`
	Title     string `json:"title"`
	Severity  string `json:"severity"`
	MetricID  string `json:"metric_id"`
	RuleID    int    `json:"rule_id"`
	Triggered string `json:"triggered_at"`
}

func (e AlertPayloadEvent) AlertPayload() map[string]interface{} {
	return map[string]interface{}{
		"alert_id":  e.AlertID,
		"title":     e.Title,
		"severity":  e.Severity,
		"metric_id": e.MetricID,
		"rule_id":   e.RuleID,
		"triggered": e.Triggered,
	}
}

// _ ensure time is used
var _ = time.Now
