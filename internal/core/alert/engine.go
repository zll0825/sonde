// Package alert manages alert lifecycle: trigger → dedup → active → resolve.
package alert

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"

	"capital_observatory/pkg/model"
)

// Engine evaluates triggers and upserts alerts with deduplication.
type Engine struct {
	store AlertStore
}

// AlertStore is the persistence interface for alerts.
type AlertStore interface {
	UpsertAlert(ctx context.Context, alert model.Alert) error
	ResolveAlert(ctx context.Context, dedupKey string, resolvedAt time.Time) error
	GetActiveAlert(ctx context.Context, dedupKey string) (*model.Alert, error)
	InsertOutboxEvent(ctx context.Context, eventType string, payload []byte) error
}

// NewEngine creates a new alert engine with the given store.
func NewEngine(store AlertStore) *Engine {
	return &Engine{store: store}
}

// HandleTrigger processes a rule trigger:
// 1. Look up active alert by dedup_key → if found, skip (dedup).
// 2. Otherwise insert new alert + outbox event.
func (e *Engine) HandleTrigger(ctx context.Context, alert model.Alert) error {
	// Dedup: an active alert with the same dedup_key means we already fired.
	existing, err := e.store.GetActiveAlert(ctx, alert.DedupKey)
	if err != nil {
		return fmt.Errorf("get active alert: %w", err)
	}
	if existing != nil {
		log.Debug().
			Str("dedup_key", alert.DedupKey).
			Str("title", alert.Title).
			Msg("alert deduplicated")
		return nil
	}

	log.Info().
		Str("title", alert.Title).
		Str("metric", alert.MetricID).
		Str("severity", string(alert.Severity)).
		Msg("alert triggered")
	return e.store.UpsertAlert(ctx, alert)
}

// Resolve closes an active alert by its dedup key.
func (e *Engine) Resolve(ctx context.Context, dedupKey string) error {
	return e.store.ResolveAlert(ctx, dedupKey, time.Now())
}

// AdapterTrigger wraps a model.Alert as a trigger-to-alert adapter.
type AdapterTrigger struct {
	model.Alert
}

func (a AdapterTrigger) ToAlert() model.Alert { return a.Alert }
