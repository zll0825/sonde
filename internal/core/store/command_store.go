// Package store provides persistence for command_log — the control plane
// by which the Core asks a connected Plugin to run an immediate Sync or Backfill.
//
// PRD §6.2: API writes a `pending` command into command_log, the Core polls
// for it, routes the command to the target Plugin over the active stream, and
// updates `accepted_at` / `completed_at` / `collected_count` as CommandAcks arrive.
package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// CommandType enumerates the command kinds understood by the control plane.
type CommandType string

const (
	CmdSync     CommandType = "sync"
	CmdBackfill CommandType = "backfill"
)

// CommandStatus reports where a command is in its lifecycle.
type CommandStatus string

const (
	CmdStatusPending    CommandStatus = "pending"
	CmdStatusDispatched CommandStatus = "dispatched" // accepted by plugin, in flight
	CmdStatusCompleted  CommandStatus = "completed"
	CmdStatusFailed     CommandStatus = "failed"
)

// Command is one row of command_log, the unit of work for the CommandDispatcher.
type Command struct {
	CommandID      string
	CommandType    CommandType
	TargetPlugin   string
	RequestedBy    string
	Status         CommandStatus
	Reason         string
	MetricIDs      []string
	WindowStart    *time.Time
	WindowEnd      *time.Time
	CollectedCount int
	Error          string
	RequestedAt    time.Time
	AcceptedAt     *time.Time
	CompletedAt    *time.Time
}

// CommandStore abstracts command_log access.
type CommandStore interface {
	// GetPendingCommands returns all commands in `pending` state.
	// The dispatcher polls this, sends to the plugin, then marks dispatched.
	GetPendingCommands(ctx context.Context) ([]Command, error)

	// MarkDispatched sets status='dispatched', accepted_at=NOW().
	// Called after the command is successfully pushed to the plugin's stream.
	MarkDispatched(ctx context.Context, commandID string) error

	// MarkCompleted sets status='completed', completed_at=NOW(), collected_count.
	// Called when a CommandAck arrives with success status.
	MarkCompleted(ctx context.Context, commandID string, collectedCount int) error

	// MarkFailed sets status='failed', error msg, completed_at=NOW().
	MarkFailed(ctx context.Context, commandID string, errMsg string) error
}

// PostgresCommandStore implements CommandStore on top of Postgres.
type PostgresCommandStore struct {
	db *pgxpool.Pool
}

// NewPostgresCommandStore creates a CommandStore backed by a pgx pool.
func NewPostgresCommandStore(db *pgxpool.Pool) *PostgresCommandStore {
	return &PostgresCommandStore{db: db}
}

// GetPendingCommands returns all commands with status='pending' (FIFO by requested_at).
func (s *PostgresCommandStore) GetPendingCommands(ctx context.Context) ([]Command, error) {
	rows, err := s.db.Query(ctx, `
		SELECT command_id, command_type, target_plugin, requested_by, status,
		       reason, metric_ids, window_start, window_end, collected_count,
		       error, requested_at, accepted_at, completed_at
		FROM command_log
		WHERE status = 'pending'
		ORDER BY requested_at ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("query pending commands: %w", err)
	}
	defer rows.Close()

	var cmds []Command
	for rows.Next() {
		var c Command
		var metricIDs []byte
		if err := rows.Scan(
			&c.CommandID, &c.CommandType, &c.TargetPlugin, &c.RequestedBy,
			&c.Status, &c.Reason, &metricIDs, &c.WindowStart, &c.WindowEnd,
			&c.CollectedCount, &c.Error, &c.RequestedAt, &c.AcceptedAt, &c.CompletedAt,
		); err != nil {
			return nil, fmt.Errorf("scan command row: %w", err)
		}
		if len(metricIDs) > 0 {
			_ = json.Unmarshal(metricIDs, &c.MetricIDs)
		}
		cmds = append(cmds, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate command rows: %w", err)
	}
	return cmds, nil
}

// MarkDispatched sets accepted_at for a command (transitions pending → dispatched).
func (s *PostgresCommandStore) MarkDispatched(ctx context.Context, commandID string) error {
	now := time.Now()
	tag, err := s.db.Exec(ctx, `
		UPDATE command_log
		SET status = 'dispatched', accepted_at = $2
		WHERE command_id = $1 AND status = 'pending'
	`, commandID, now)
	if err != nil {
		return fmt.Errorf("mark dispatched: %w", err)
	}
	if tag.RowsAffected() == 0 {
		log.Warn().Str("command_id", commandID).Msg("no pending command found to mark dispatched")
	}
	return nil
}

// MarkCompleted records a successful plugin-side execution (dispatched → completed).
func (s *PostgresCommandStore) MarkCompleted(ctx context.Context, commandID string, collectedCount int) error {
	now := time.Now()
	tag, err := s.db.Exec(ctx, `
		UPDATE command_log
		SET status = 'completed', completed_at = $2, collected_count = $3
		WHERE command_id = $1
	`, commandID, now, collectedCount)
	if err != nil {
		return fmt.Errorf("mark completed: %w", err)
	}
	if tag.RowsAffected() == 0 {
		log.Warn().Str("command_id", commandID).Msg("no command found to mark completed")
	}
	return nil
}

// MarkFailed records a plugin-side execution failure.
func (s *PostgresCommandStore) MarkFailed(ctx context.Context, commandID string, errMsg string) error {
	now := time.Now()
	tag, err := s.db.Exec(ctx, `
		UPDATE command_log
		SET status = 'failed', completed_at = $2, error = $3
		WHERE command_id = $1
	`, commandID, now, errMsg)
	if err != nil {
		return fmt.Errorf("mark failed: %w", err)
	}
	if tag.RowsAffected() == 0 {
		log.Warn().Str("command_id", commandID).Msg("no command found to mark failed")
	}
	return nil
}

// Compile-time check that PostgresCommandStore satisfies CommandStore.
var _ CommandStore = (*PostgresCommandStore)(nil)
