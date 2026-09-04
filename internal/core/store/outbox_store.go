package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"

	"sonde/internal/core/alert"
	coreevent "sonde/internal/core/event"
)

// maxDispatchAttempts is the attempt ceiling after which an outbox event is
// moved to status='failed' instead of staying 'pending' for retry.
const maxDispatchAttempts = 5

// ResearchLinkAudit exposes the alert-to-snapshot invariant and terminal work
// failures for operational checks and release qualification.
type ResearchLinkAudit struct {
	MissingSnapshots   int64
	DuplicateSnapshots int64
	OrphanSnapshots    int64
	FailedRequests     int64
}

// PostgresOutboxStore implements alert.OutboxStore against a PostgreSQL pool.
type PostgresOutboxStore struct {
	db *pgxpool.Pool
}

// NewPostgresOutboxStore creates a new PostgresOutboxStore.
func NewPostgresOutboxStore(db *pgxpool.Pool) *PostgresOutboxStore {
	return &PostgresOutboxStore{db: db}
}

// PickPending returns up to limit pending events in FIFO order (oldest id first).
func (s *PostgresOutboxStore) PickPending(ctx context.Context, limit int) ([]alert.OutboxEvent, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, event_type, payload, dedup_key, attempts, last_error,
		       next_attempt_at, created_at
		FROM event_outbox
		WHERE status = 'pending' AND next_attempt_at <= NOW()
		ORDER BY next_attempt_at ASC, id ASC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("query pending outbox events: %w", err)
	}
	defer rows.Close()

	var events []alert.OutboxEvent
	for rows.Next() {
		var ev alert.OutboxEvent
		if err := rows.Scan(
			&ev.ID, &ev.EventType, &ev.Payload, &ev.DedupKey, &ev.Attempts,
			&ev.LastError, &ev.NextAttemptAt, &ev.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan outbox event: %w", err)
		}
		events = append(events, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate outbox rows: %w", err)
	}
	return events, nil
}

// MarkDispatched marks an outbox event as completed.
func (s *PostgresOutboxStore) MarkDispatched(ctx context.Context, id int) error {
	if _, err := s.db.Exec(ctx, `
		UPDATE event_outbox
		SET status = 'dispatched', updated_at = NOW()
		WHERE id = $1
	`, id); err != nil {
		return fmt.Errorf("mark outbox dispatched: %w", err)
	}
	return nil
}

// MarkFailed increments the attempt counter and, when the ceiling is reached,
// transitions the event to status='failed' so it is no longer picked. The error
// message is logged but not persisted (the outbox schema has no error column).
func (s *PostgresOutboxStore) MarkFailed(ctx context.Context, id int, errMsg string) error {
	var attempts int
	err := s.db.QueryRow(ctx, `
		UPDATE event_outbox
		SET attempts = attempts + 1,
		    last_error = $3,
		    next_attempt_at = NOW() + LEAST(
		        INTERVAL '5 seconds' * POWER(2, attempts),
		        INTERVAL '5 minutes'
		    ),
		    status = CASE WHEN attempts + 1 >= $2 THEN 'failed' ELSE status END,
		    updated_at = NOW()
		WHERE id = $1
		RETURNING attempts
	`, id, maxDispatchAttempts, errMsg).Scan(&attempts)
	if err != nil {
		return fmt.Errorf("mark outbox failed: %w", err)
	}

	logEvent := log.Warn()
	if attempts >= maxDispatchAttempts {
		logEvent = log.Error()
	}
	logEvent.
		Int("event_id", id).
		Str("error", errMsg).
		Int("attempts", attempts).
		Bool("exhausted", attempts >= maxDispatchAttempts).
		Msg("outbox event failed")
	return nil
}

// ReconcileDetectionRequests enqueues one idempotent request for the latest
// observation of every current metric. It repairs pre-outbox observations and
// the migration-to-binary rollout gap without scanning unbounded history.
func (s *PostgresOutboxStore) ReconcileDetectionRequests(ctx context.Context) (int64, error) {
	tag, err := s.db.Exec(ctx, `
		WITH current_metrics AS (
			SELECT DISTINCT ON (uid) id, uid
			FROM metric_definitions
			WHERE effective_to IS NULL AND active = TRUE
			ORDER BY uid, version DESC
		), latest AS (
			SELECT cm.id AS metric_id, cm.uid AS metric_uid,
			       o.time, o.source_plugin, o.source_provider,
			       o.labels_hash, o.quality_grade
			FROM current_metrics cm
			CROSS JOIN LATERAL (
				SELECT time, source_plugin, source_provider, labels_hash, quality_grade
				FROM observations
				WHERE metric_uid = cm.uid
				ORDER BY time DESC
				LIMIT 1
			) o
		), work AS (
			SELECT *, 'reconcile:' || MD5(CONCAT_WS(CHR(31),
				metric_uid, EXTRACT(EPOCH FROM time)::BIGINT::TEXT,
				source_plugin, source_provider, labels_hash, quality_grade
			)) AS detection_key
			FROM latest
		)
		INSERT INTO event_outbox (event_type, payload, dedup_key)
		SELECT $1,
		       JSONB_BUILD_OBJECT(
		           'detection_key', detection_key,
		           'metric_id', metric_id,
		           'metric_uid', metric_uid,
		           'plugin_id', source_plugin
		       ),
		       detection_key
		FROM work
		ON CONFLICT (event_type, dedup_key) WHERE dedup_key IS NOT NULL DO NOTHING
	`, coreevent.TypeDetectionRequested)
	if err != nil {
		return 0, fmt.Errorf("reconcile detection requests: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ReconcileResearchRequests enqueues bounded, idempotent work for alerts that
// predate the durable research event. Existing pending or failed work is left
// untouched so retry ownership and terminal failure visibility are preserved.
func (s *PostgresOutboxStore) ReconcileResearchRequests(ctx context.Context, limit int) (int64, error) {
	if limit <= 0 {
		limit = 100
	}
	tag, err := s.db.Exec(ctx, `
		WITH candidates AS (
			SELECT a.id
			FROM alerts a
			LEFT JOIN research_snapshots rs ON rs.alert_id = a.id
			LEFT JOIN event_outbox eo
			       ON eo.event_type = $1 AND eo.dedup_key = a.id
			WHERE rs.alert_id IS NULL AND eo.id IS NULL
			ORDER BY a.triggered_at ASC, a.id ASC
			LIMIT $2
		)
		INSERT INTO event_outbox (event_type, payload, dedup_key)
		SELECT $1, JSONB_BUILD_OBJECT('alert_id', id), id
		FROM candidates
		ON CONFLICT (event_type, dedup_key) WHERE dedup_key IS NOT NULL DO NOTHING
	`, coreevent.TypeResearchRequested, limit)
	if err != nil {
		return 0, fmt.Errorf("reconcile research requests: %w", err)
	}
	return tag.RowsAffected(), nil
}

// AuditResearchLinks counts broken one-to-one links and exhausted research
// work. The duplicate/orphan counts should remain structurally zero because
// research_snapshots.alert_id is both a primary key and an alert foreign key.
func (s *PostgresOutboxStore) AuditResearchLinks(ctx context.Context) (ResearchLinkAudit, error) {
	var audit ResearchLinkAudit
	err := s.db.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*)
			 FROM alerts a
			 LEFT JOIN research_snapshots rs ON rs.alert_id = a.id
			 WHERE rs.alert_id IS NULL),
			(SELECT COALESCE(SUM(snapshot_count - 1), 0)
			 FROM (
				SELECT COUNT(*) AS snapshot_count
				FROM research_snapshots
				GROUP BY alert_id
				HAVING COUNT(*) > 1
			 ) duplicates),
			(SELECT COUNT(*)
			 FROM research_snapshots rs
			 LEFT JOIN alerts a ON a.id = rs.alert_id
			 WHERE a.id IS NULL),
			(SELECT COUNT(*)
			 FROM event_outbox eo
			 LEFT JOIN research_snapshots rs ON rs.alert_id = eo.dedup_key
			 WHERE eo.event_type = $1
			   AND eo.status = 'failed'
			   AND rs.alert_id IS NULL)
	`, coreevent.TypeResearchRequested).Scan(
		&audit.MissingSnapshots,
		&audit.DuplicateSnapshots,
		&audit.OrphanSnapshots,
		&audit.FailedRequests,
	)
	if err != nil {
		return ResearchLinkAudit{}, fmt.Errorf("audit research links: %w", err)
	}
	return audit, nil
}
