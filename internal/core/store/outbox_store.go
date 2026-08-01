package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"

	"capital_observatory/internal/core/alert"
)

// maxDispatchAttempts is the attempt ceiling after which an outbox event is
// moved to status='failed' instead of staying 'pending' for retry.
const maxDispatchAttempts = 5

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
		SELECT id, event_type, payload, attempts, created_at
		FROM event_outbox
		WHERE status = 'pending'
		ORDER BY id ASC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("query pending outbox events: %w", err)
	}
	defer rows.Close()

	var events []alert.OutboxEvent
	for rows.Next() {
		var ev alert.OutboxEvent
		if err := rows.Scan(&ev.ID, &ev.EventType, &ev.Payload, &ev.Attempts, &ev.CreatedAt); err != nil {
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
		UPDATE event_outbox SET status = 'dispatched' WHERE id = $1
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
		    status = CASE WHEN attempts + 1 >= $2 THEN 'failed' ELSE status END
		WHERE id = $1
		RETURNING attempts
	`, id, maxDispatchAttempts).Scan(&attempts)
	if err != nil {
		return fmt.Errorf("mark outbox failed: %w", err)
	}

	log.Error().
		Int("event_id", id).
		Str("error", errMsg).
		Int("attempts", attempts).
		Bool("exhausted", attempts >= maxDispatchAttempts).
		Msg("outbox event failed")
	return nil
}
