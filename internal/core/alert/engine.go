// Package alert 管理告警生命周期：触发 → 去重 → active → 自动 resolve。
// 触发路径在同一事务内写入 alerts 与 event_outbox（outbox 模式），保证
// "告警落库"与"事件派发"要么都发生、要么都不发生。
package alert

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"

	coreevent "sonde/internal/core/event"
	"sonde/pkg/model"
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
	// CreateAlertWithEvents inserts the alert row AND its outbox events in one
	// database transaction. Tier-1 events must never be emitted outside the
	// transaction that produced them (ADR-5: no silent data loss).
	CreateAlertWithEvents(ctx context.Context, alert model.Alert, events []PendingEvent) error
	ResolveAlert(ctx context.Context, dedupKey string, resolvedAt time.Time) error
	GetActiveAlert(ctx context.Context, dedupKey string) (*model.Alert, error)
	RecordDeduplication(ctx context.Context, dedupKey string, at time.Time) error

	// GetActiveAlertsByMetric returns all currently-active alerts for a given
	// metric_id, keyed by dedup_key. Used by the auto-resolve sweeper to find
	// "stale" alerts whose rule condition no longer holds.
	GetActiveAlertsByMetric(ctx context.Context, metricID string) ([]model.Alert, error)
}

// NewEngine creates a new alert engine with the given store.
func NewEngine(store AlertStore) *Engine {
	return &Engine{store: store}
}

// HandleTrigger 处理一次规则触发：
// 1. 按 dedup_key 查 active 告警 → 命中则跳过（去重，不重复扰人）；
// 2. 未命中则在同一事务内插入新告警 + outbox 事件（ADR-5，原子性）。
func (e *Engine) HandleTrigger(ctx context.Context, alert model.Alert) error {
	// Dedup: an active alert with the same dedup_key means we already fired.
	existing, err := e.store.GetActiveAlert(ctx, alert.DedupKey)
	if err != nil {
		return fmt.Errorf("get active alert: %w", err)
	}
	if existing != nil {
		if err := e.store.RecordDeduplication(ctx, alert.DedupKey, alert.TriggeredAt); err != nil {
			return fmt.Errorf("record active alert deduplication: %w", err)
		}
		log.Debug().
			Str("dedup_key", alert.DedupKey).
			Str("title", alert.Title).
			Msg("alert deduplicated")
		return nil
	}

	alert.Mode = model.NormalizeRuleMode(alert.Mode)

	var events []PendingEvent
	if alert.Mode != model.RuleModeObserve {
		notificationPayload, err := json.Marshal(map[string]interface{}{
			"alert_id":     alert.ID,
			"title":        alert.Title,
			"summary":      alert.Summary,
			"severity":     string(alert.Severity),
			"metric_id":    alert.MetricID,
			"rule_id":      alert.RuleID,
			"triggered_at": alert.TriggeredAt,
		})
		if err != nil {
			return fmt.Errorf("marshal alert event payload: %w", err)
		}
		researchRequest := coreevent.NewResearchRequest(alert.ID)
		researchPayload, err := json.Marshal(researchRequest)
		if err != nil {
			return fmt.Errorf("marshal research request payload: %w", err)
		}
		researchKey := researchRequest.AlertID
		events = []PendingEvent{
			{EventType: EventTypeAlertTriggered, Payload: notificationPayload},
			{EventType: coreevent.TypeResearchRequested, Payload: researchPayload, DedupKey: &researchKey},
		}
	}

	log.Info().
		Str("title", alert.Title).
		Str("metric", alert.MetricID).
		Str("severity", string(alert.Severity)).
		Str("mode", string(alert.Mode)).
		Msg("alert triggered")
	if err := e.store.CreateAlertWithEvents(ctx, alert, events); err != nil {
		if errors.Is(err, ErrDuplicateAlert) {
			// Lost the insert race to a concurrent trigger. Audit this duplicate
			// through a separate atomic update after the failed transaction rolls back.
			if auditErr := e.store.RecordDeduplication(ctx, alert.DedupKey, alert.TriggeredAt); auditErr != nil {
				return fmt.Errorf("record raced alert deduplication: %w", auditErr)
			}
			log.Debug().Str("dedup_key", alert.DedupKey).Msg("alert deduplicated by DB index")
			return nil
		}
		return err
	}
	return nil
}

// Resolve closes an active alert by its dedup key.
// No-op if the alert is already resolved or never existed.
func (e *Engine) Resolve(ctx context.Context, dedupKey string) error {
	return e.store.ResolveAlert(ctx, dedupKey, time.Now())
}

// AutoResolveStaleAlerts 找出本轮评估中条件已不再成立的 active 告警并自动
// resolve（PRD §5.4：条件恢复即自动解除，无需人工关闭）。
//
// Loop:
//  1. Fetch active alerts for this metric (the "previously fired" set).
//  2. activeByRule maps rule_id → active alert (resolved-at-is-null).
//  3. fireSet = rules that DID trigger in this batch.
//  4. For each rule with an active alert but NOT in fireSet, resolve it.
//
// This implements PRD §5.4: "when a rule's condition is no longer true,
// the AlertEngine auto-marks the alert as resolved." A rule that fires every
// cycle keeps its alert alive; a rule that stops firing loses its alert on
// the next evaluation.
func (e *Engine) AutoResolveStaleAlerts(
	ctx context.Context,
	metricID string,
	firedRuleIDs map[int]struct{},
) error {
	activeAlerts, err := e.store.GetActiveAlertsByMetric(ctx, metricID)
	if err != nil {
		return fmt.Errorf("get active alerts by metric: %w", err)
	}

	for _, a := range activeAlerts {
		// This rule fired in the current batch → keep alive.
		if _, fired := firedRuleIDs[a.RuleID]; fired {
			continue
		}
		log.Info().
			Str("dedup_key", a.DedupKey).
			Str("metric_id", metricID).
			Int("rule_id", a.RuleID).
			Msg("auto-resolving stale alert (condition no longer triggered)")
		if rerr := e.store.ResolveAlert(ctx, a.DedupKey, time.Now()); rerr != nil {
			// Log + continue: one resolve failure must not abort the batch.
			log.Error().Err(rerr).
				Str("dedup_key", a.DedupKey).
				Msg("auto-resolve failed")
			continue
		}
	}
	return nil
}
