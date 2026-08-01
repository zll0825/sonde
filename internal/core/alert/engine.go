// Package alert manages alert lifecycle: trigger → dedup → active → resolve.
package alert

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"

	"capital_observatory/pkg/model"
)

// EventTypeAlertTriggered is the outbox event_type written when a new alert fires.
const EventTypeAlertTriggered = "alert.triggered"

// ErrDuplicateAlert is returned by AlertStore implementations when the DB-level
// dedup guard (partial unique index on dedup_key WHERE status='active') rejects
// a concurrent insert. The engine treats it as a benign lost race, not a failure.
var ErrDuplicateAlert = errors.New("duplicate alert")

// Engine evaluates triggers and upserts alerts with deduplication.
type Engine struct {
	store AlertStore
}

// AlertStore is the persistence interface for alerts.
type AlertStore interface {
	// CreateAlertWithEvent inserts the alert row AND its outbox event in one
	// database transaction. Tier-1 events must never be emitted outside the
	// transaction that produced them (ADR-5: no silent data loss).
	CreateAlertWithEvent(ctx context.Context, alert model.Alert, eventType string, payload []byte) error
	ResolveAlert(ctx context.Context, dedupKey string, resolvedAt time.Time) error
	GetActiveAlert(ctx context.Context, dedupKey string) (*model.Alert, error)
}

// NewEngine creates a new alert engine with the given store.
func NewEngine(store AlertStore) *Engine {
	return &Engine{store: store}
}

// HandleTrigger processes a rule trigger:
// 1. Look up active alert by dedup_key → if found, skip (dedup).
// 2. Otherwise insert new alert + outbox event in one transaction (ADR-5).
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

	payload, err := json.Marshal(map[string]interface{}{
		"alert_id":     alert.ID,
		"title":        alert.Title,
		"severity":     string(alert.Severity),
		"metric_id":    alert.MetricID,
		"rule_id":      alert.RuleID,
		"triggered_at": alert.TriggeredAt,
	})
	if err != nil {
		return fmt.Errorf("marshal alert event payload: %w", err)
	}

	log.Info().
		Str("title", alert.Title).
		Str("metric", alert.MetricID).
		Str("severity", string(alert.Severity)).
		Msg("alert triggered")
	if err := e.store.CreateAlertWithEvent(ctx, alert, EventTypeAlertTriggered, payload); err != nil {
		if errors.Is(err, ErrDuplicateAlert) {
			// Lost the insert race to a concurrent trigger — same outcome as dedup.
			log.Debug().Str("dedup_key", alert.DedupKey).Msg("alert deduplicated by DB index")
			return nil
		}
		return err
	}
	return nil
}

// Resolve closes an active alert by its dedup key.
func (e *Engine) Resolve(ctx context.Context, dedupKey string) error {
	return e.store.ResolveAlert(ctx, dedupKey, time.Now())
}
