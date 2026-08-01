// Package store provides PostgreSQL-backed implementations of the alert and
// research persistence interfaces (Trellis M-3 deliverable).
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"

	"capital_observatory/internal/core/alert"
	"capital_observatory/pkg/model"
)

// PostgresAlertStore implements alert.AlertStore against a PostgreSQL pool.
type PostgresAlertStore struct {
	db *pgxpool.Pool
}

// NewPostgresAlertStore creates a new PostgresAlertStore.
func NewPostgresAlertStore(db *pgxpool.Pool) *PostgresAlertStore {
	return &PostgresAlertStore{db: db}
}

// CreateAlertWithEvent inserts the alert row AND its outbox event in one
// database transaction (ADR-5: no silent data loss). The unique partial index
// idx_alerts_active_dedup (WHERE status='active') guarantees dedup at the DB
// level: a concurrent insert that already created an active alert for the same
// dedup_key raises PG 23505, which we translate to ErrDuplicateAlert.
func (s *PostgresAlertStore) CreateAlertWithEvent(ctx context.Context, a model.Alert, eventType string, payload []byte) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		INSERT INTO alerts (
			id, title, summary, severity, status,
			metric_id, rule_id, rule_version, rule_effective_from,
			detector_name, dedup_key, window_start, window_end,
			evidence, plugin_id, triggered_at
		) VALUES (
			$1, $2, $3, $4, 'active',
			$5, $6, $7, $8,
			$9, $10, $11, $12,
			$13, $14, $15
		)
	`, a.ID, a.Title, a.Summary, string(a.Severity),
		a.MetricID, a.RuleID, a.RuleVersion, a.RuleEffectiveFrom,
		a.DetectorName, a.DedupKey, a.WindowStart, a.WindowEnd,
		a.Evidence, a.PluginID, a.TriggeredAt)

	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return alert.ErrDuplicateAlert
		}
		return fmt.Errorf("insert alert: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO event_outbox (event_type, payload) VALUES ($1, $2)
	`, eventType, payload); err != nil {
		return fmt.Errorf("insert outbox event: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// GetActiveAlert returns the active alert for a dedup_key, or nil if none exists.
func (s *PostgresAlertStore) GetActiveAlert(ctx context.Context, dedupKey string) (*model.Alert, error) {
	row := s.db.QueryRow(ctx, `
		SELECT id, title, summary, severity, status,
		       metric_id, rule_id, rule_version, rule_effective_from,
		       detector_name, dedup_key, window_start, window_end,
		       evidence, plugin_id, triggered_at, resolved_at
		FROM alerts
		WHERE dedup_key = $1 AND status = 'active'
	`, dedupKey)

	var alert model.Alert
	err := row.Scan(
		&alert.ID, &alert.Title, &alert.Summary, &alert.Severity, &alert.Status,
		&alert.MetricID, &alert.RuleID, &alert.RuleVersion, &alert.RuleEffectiveFrom,
		&alert.DetectorName, &alert.DedupKey, &alert.WindowStart, &alert.WindowEnd,
		&alert.Evidence, &alert.PluginID, &alert.TriggeredAt, &alert.ResolvedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan active alert: %w", err)
	}
	return &alert, nil
}

// ResolveAlert sets status='resolved' for the active alert matching dedupKey.
// Idempotent: if no active alert matches (already resolved or never existed),
// the UPDATE affects zero rows and a warning is logged.
func (s *PostgresAlertStore) ResolveAlert(ctx context.Context, dedupKey string, resolvedAt time.Time) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE alerts
		SET status = 'resolved', resolved_at = $2, updated_at = NOW()
		WHERE dedup_key = $1 AND status = 'active'
	`, dedupKey, resolvedAt)
	if err != nil {
		return fmt.Errorf("resolve alert: %w", err)
	}
	if tag.RowsAffected() == 0 {
		log.Warn().Str("dedup_key", dedupKey).Msg("no active alert found to resolve")
	}
	return nil
}

// GetActiveAlertsByMetric returns all active alerts for a given metric_id.
// Used by AutoResolveStaleAlerts to detect alerts whose condition no longer holds.
func (s *PostgresAlertStore) GetActiveAlertsByMetric(ctx context.Context, metricID string) ([]model.Alert, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, title, summary, severity, status,
		       metric_id, rule_id, rule_version, rule_effective_from,
		       detector_name, dedup_key, window_start, window_end,
		       evidence, plugin_id, triggered_at, resolved_at
		FROM alerts
		WHERE metric_id = $1 AND status = 'active'
		ORDER BY triggered_at DESC
	`, metricID)
	if err != nil {
		return nil, fmt.Errorf("query active alerts by metric: %w", err)
	}
	defer rows.Close()

	var alerts []model.Alert
	for rows.Next() {
		var a model.Alert
		if err := rows.Scan(
			&a.ID, &a.Title, &a.Summary, &a.Severity, &a.Status,
			&a.MetricID, &a.RuleID, &a.RuleVersion, &a.RuleEffectiveFrom,
			&a.DetectorName, &a.DedupKey, &a.WindowStart, &a.WindowEnd,
			&a.Evidence, &a.PluginID, &a.TriggeredAt, &a.ResolvedAt,
		); err != nil {
			return nil, fmt.Errorf("scan active alert: %w", err)
		}
		alerts = append(alerts, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active alert rows: %w", err)
	}
	return alerts, nil
}
