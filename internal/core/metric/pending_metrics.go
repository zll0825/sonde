package metric

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// DB is the minimal database interface required for metric operations.
// It is satisfied by both pgxpool.Pool and pgx.Conn.
type DB interface {
	Query(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row
	Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error)
}

// PendingMetric represents a parked observation source awaiting resolution.
type PendingMetric struct {
	MetricID      string
	FirstSeenAt   time.Time
	FirstSeenFrom string
	Status        string
}

// PendingTracker manages the pending_metrics table for observations whose
// metric_id has not yet been registered in metric_definitions.
type PendingTracker struct {
	db DB
}

// NewPendingTracker creates a new tracker.
func NewPendingTracker(db DB) *PendingTracker {
	return &PendingTracker{db: db}
}

// Record inserts or updates a pending metric entry, setting its status
// based on whether the metric_id exists in metric_definitions:
//   - metric_id not in metric_definitions → "unknown_source"
//   - metric_id exists → "registered"
func (t *PendingTracker) Record(ctx context.Context, metricID, pluginID, provider string) error {
	var exists bool
	err := t.db.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM metric_definitions WHERE id = $1 AND effective_to IS NULL
		)`, metricID).Scan(&exists)

	if errors.Is(err, pgx.ErrNoRows) {
		exists = false
	} else if err != nil {
		return fmt.Errorf("check metric definition existence: %w", err)
	}

	status := "unknown_source"
	if exists {
		status = "registered"
	}

	_, err = t.db.Exec(ctx, `
		INSERT INTO pending_metrics (metric_id, first_seen_at, first_seen_from_plugin, status)
		VALUES ($1, now(), $2, $3)
		ON CONFLICT (metric_id) DO UPDATE SET
			status = EXCLUDED.status,
			first_seen_at = pending_metrics.first_seen_at`,
		metricID, pluginID, status)

	if err != nil {
		return fmt.Errorf("upsert pending metric: %w", err)
	}
	return nil
}

// GetPending returns entries still awaiting resolution (status='unknown_source').
func (t *PendingTracker) GetPending(ctx context.Context) ([]PendingMetric, error) {
	rows, err := t.db.Query(ctx, `
		SELECT metric_id, first_seen_at, first_seen_from_plugin, status
		FROM pending_metrics
		WHERE status = 'unknown_source'
		ORDER BY first_seen_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("query pending metrics: %w", err)
	}
	defer rows.Close()

	var results []PendingMetric
	for rows.Next() {
		var pm PendingMetric
		if err := rows.Scan(&pm.MetricID, &pm.FirstSeenAt, &pm.FirstSeenFrom, &pm.Status); err != nil {
			return nil, fmt.Errorf("scan pending metric: %w", err)
		}
		results = append(results, pm)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending metrics: %w", err)
	}
	return results, nil
}
