// command_store.go — 命令日志（command_log）存取，控制面通路（PRD §6.2）：
// API 写入 pending 命令 → Core 轮询并经活跃流派发给目标插件 → CommandAck
// 回执更新 accepted_at / completed_at / collected_count，并保留租约重试历史。
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
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
	CmdStatusDispatched CommandStatus = "dispatched" // claimed by Core, lease in flight
	CmdStatusCompleted  CommandStatus = "completed"
	CmdStatusFailed     CommandStatus = "failed"
)

const (
	// CommandMaxAttempts is shared by lease expiry, send failure, and plugin
	// failure so every path consumes one bounded retry budget.
	CommandMaxAttempts = 5
	// CommandLeaseDuration bounds how long a dispatched attempt may wait for ACK.
	CommandLeaseDuration = 60 * time.Second
	// CommandDispatchBatch bounds each database claim and dispatcher tick.
	CommandDispatchBatch = 100
)

// ErrCommandNotDispatched means a transition lost a race to another terminal
// state or referenced a command that is not currently leased.
var ErrCommandNotDispatched = errors.New("command is not dispatched")

// Command is one row of command_log, the unit of work for the CommandDispatcher.
type Command struct {
	CommandID        string
	CommandType      CommandType
	TargetPlugin     string
	RequestedBy      string
	Status           CommandStatus
	Reason           string
	MetricIDs        []string
	WindowStart      *time.Time
	WindowEnd        *time.Time
	CollectedCount   int
	Error            string
	RequestedAt      time.Time
	AcceptedAt       *time.Time
	CompletedAt      *time.Time
	Attempts         int
	LastDispatchedAt *time.Time
	LeaseExpiresAt   *time.Time
	LastError        string
	UpdatedAt        time.Time
}

// CommandStore abstracts command_log access.
type CommandStore interface {
	// ClaimDispatchable atomically terminalizes exhausted leases and claims a
	// bounded batch for currently connected plugins before stream enqueue.
	ClaimDispatchable(ctx context.Context, pluginIDs []string, limit int, lease time.Duration) ([]Command, error)

	// MarkCompleted sets status='completed', completed_at=NOW(), collected_count.
	// A late success may close a previously exhausted command with the same ID.
	MarkCompleted(ctx context.Context, commandID string, collectedCount int) error

	// RenewLease extends an active lease when accepted/running progress arrives.
	RenewLease(ctx context.Context, commandID string, lease time.Duration) error

	// MarkAttemptFailed releases the lease for retry, or marks the fifth attempt
	// terminal. Used for both stream enqueue and plugin execution failures.
	MarkAttemptFailed(ctx context.Context, commandID string, errMsg string) (CommandStatus, error)

	// MarkTerminalFailed ends non-retryable work such as an unknown command type.
	MarkTerminalFailed(ctx context.Context, commandID string, errMsg string) error
}

// PostgresCommandStore implements CommandStore on top of Postgres.
type PostgresCommandStore struct {
	db *pgxpool.Pool
}

// NewPostgresCommandStore creates a CommandStore backed by a pgx pool.
func NewPostgresCommandStore(db *pgxpool.Pool) *PostgresCommandStore {
	return &PostgresCommandStore{db: db}
}

// ClaimDispatchable owns the pending/expired -> dispatched transition. The
// candidates are locked and updated in one statement so concurrent Core workers
// cannot claim the same attempt. Offline plugin IDs are excluded by the caller's
// connected-plugin snapshot and therefore consume no attempts.
func (s *PostgresCommandStore) ClaimDispatchable(
	ctx context.Context,
	pluginIDs []string,
	limit int,
	lease time.Duration,
) ([]Command, error) {
	if limit <= 0 {
		limit = CommandDispatchBatch
	}
	if lease <= 0 {
		lease = CommandLeaseDuration
	}

	rows, err := s.db.Query(ctx, `
		WITH exhausted AS (
			UPDATE command_log
			SET status = 'failed',
			    error = 'command lease expired after retry budget',
			    last_error = 'command lease expired after retry budget',
			    lease_expires_at = NULL,
			    completed_at = NOW(),
			    updated_at = NOW()
			WHERE status = 'dispatched'
			  AND lease_expires_at <= NOW()
			  AND attempts >= $4
			RETURNING command_id, command_type, target_plugin, requested_by,
			          status, COALESCE(reason, '') AS reason, metric_ids,
			          window_start, window_end,
			          COALESCE(collected_count, 0) AS collected_count,
			          COALESCE(error, '') AS error, requested_at, accepted_at,
			          completed_at, attempts, last_dispatched_at,
			          lease_expires_at, COALESCE(last_error, '') AS last_error,
			          updated_at
		), candidates AS (
			SELECT command_id
			FROM command_log
			WHERE target_plugin = ANY($1::TEXT[])
			  AND attempts < $4
			  AND (
			      status = 'pending'
			      OR (status = 'dispatched' AND lease_expires_at <= NOW())
			  )
			ORDER BY requested_at ASC, command_id ASC
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		), claimed AS (
			UPDATE command_log c
			SET status = 'dispatched',
			    attempts = c.attempts + 1,
			    last_dispatched_at = NOW(),
			    lease_expires_at = NOW() + ($3 * INTERVAL '1 millisecond'),
			    error = CASE
			        WHEN c.status = 'dispatched' THEN 'command lease expired before acknowledgement'
			        ELSE c.error
			    END,
			    last_error = CASE
			        WHEN c.status = 'dispatched' THEN 'command lease expired before acknowledgement'
			        ELSE c.last_error
			    END,
			    updated_at = NOW()
			FROM candidates
			WHERE c.command_id = candidates.command_id
			RETURNING c.command_id, c.command_type, c.target_plugin,
			          c.requested_by, c.status, COALESCE(c.reason, ''),
			          c.metric_ids, c.window_start, c.window_end,
			          COALESCE(c.collected_count, 0), COALESCE(c.error, ''),
			          c.requested_at, c.accepted_at, c.completed_at,
			          c.attempts, c.last_dispatched_at, c.lease_expires_at,
			          COALESCE(c.last_error, ''), c.updated_at
		)
		SELECT 'exhausted'::TEXT AS transition, exhausted.* FROM exhausted
		UNION ALL
		SELECT 'claimed'::TEXT AS transition, claimed.* FROM claimed
		ORDER BY requested_at ASC, command_id ASC
	`, pluginIDs, limit, lease.Milliseconds(), CommandMaxAttempts)
	if err != nil {
		return nil, fmt.Errorf("claim dispatchable commands: %w", err)
	}
	defer rows.Close()

	var cmds []Command
	for rows.Next() {
		var c Command
		var metricIDs []byte
		var transition string
		if err := rows.Scan(
			&transition,
			&c.CommandID, &c.CommandType, &c.TargetPlugin, &c.RequestedBy,
			&c.Status, &c.Reason, &metricIDs, &c.WindowStart, &c.WindowEnd,
			&c.CollectedCount, &c.Error, &c.RequestedAt, &c.AcceptedAt, &c.CompletedAt,
			&c.Attempts, &c.LastDispatchedAt, &c.LeaseExpiresAt, &c.LastError, &c.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan claimed command row: %w", err)
		}
		if len(metricIDs) > 0 {
			if err := json.Unmarshal(metricIDs, &c.MetricIDs); err != nil {
				return nil, fmt.Errorf("decode command %s metric_ids: %w", c.CommandID, err)
			}
		}
		if transition == "exhausted" {
			log.Error().
				Str("command_id", c.CommandID).
				Str("plugin_id", c.TargetPlugin).
				Int("attempts", c.Attempts).
				Str("error", c.LastError).
				Msg("command lease retry budget exhausted")
			continue
		}
		cmds = append(cmds, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate claimed command rows: %w", err)
	}
	return cmds, nil
}

// MarkCompleted records authoritative success. A late ACK may arrive after the
// lease budget marked a command failed, so failed -> completed is allowed while
// attempts and last_error remain as history.
func (s *PostgresCommandStore) MarkCompleted(ctx context.Context, commandID string, collectedCount int) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE command_log
		SET status = 'completed',
		    completed_at = NOW(),
		    accepted_at = COALESCE(accepted_at, NOW()),
		    collected_count = $2,
		    error = NULL,
		    lease_expires_at = NULL,
		    updated_at = NOW()
		WHERE command_id = $1 AND status IN ('dispatched', 'failed')
	`, commandID, collectedCount)
	if err != nil {
		return fmt.Errorf("mark completed: %w", err)
	}
	if tag.RowsAffected() == 0 {
		log.Warn().Str("command_id", commandID).Msg("no dispatched or failed command found to complete")
	}
	return nil
}

// RenewLease records accepted/running progress without consuming an attempt.
func (s *PostgresCommandStore) RenewLease(ctx context.Context, commandID string, lease time.Duration) error {
	if lease <= 0 {
		lease = CommandLeaseDuration
	}
	tag, err := s.db.Exec(ctx, `
		UPDATE command_log
		SET accepted_at = COALESCE(accepted_at, NOW()),
		    lease_expires_at = NOW() + ($2 * INTERVAL '1 millisecond'),
		    updated_at = NOW()
		WHERE command_id = $1 AND status = 'dispatched'
	`, commandID, lease.Milliseconds())
	if err != nil {
		return fmt.Errorf("renew command lease: %w", err)
	}
	if tag.RowsAffected() == 0 {
		log.Debug().Str("command_id", commandID).Msg("progress ack ignored for non-dispatched command")
	}
	return nil
}

// MarkAttemptFailed records a retryable attempt failure and releases its lease.
func (s *PostgresCommandStore) MarkAttemptFailed(ctx context.Context, commandID string, errMsg string) (CommandStatus, error) {
	var status CommandStatus
	err := s.db.QueryRow(ctx, `
		UPDATE command_log
		SET status = CASE WHEN attempts >= $3 THEN 'failed' ELSE 'pending' END,
		    error = $2,
		    last_error = $2,
		    lease_expires_at = NULL,
		    completed_at = CASE WHEN attempts >= $3 THEN NOW() ELSE NULL END,
		    updated_at = NOW()
		WHERE command_id = $1 AND status = 'dispatched'
		RETURNING status
	`, commandID, errMsg, CommandMaxAttempts).Scan(&status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrCommandNotDispatched
		}
		return "", fmt.Errorf("mark command attempt failed: %w", err)
	}
	return status, nil
}

// MarkTerminalFailed records a non-retryable command failure.
func (s *PostgresCommandStore) MarkTerminalFailed(ctx context.Context, commandID string, errMsg string) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE command_log
		SET status = 'failed',
		    error = $2,
		    last_error = $2,
		    lease_expires_at = NULL,
		    completed_at = NOW(),
		    updated_at = NOW()
		WHERE command_id = $1 AND status IN ('pending', 'dispatched')
	`, commandID, errMsg)
	if err != nil {
		return fmt.Errorf("mark command terminal failed: %w", err)
	}
	if tag.RowsAffected() == 0 {
		log.Warn().Str("command_id", commandID).Msg("no active command found to mark terminal failed")
	}
	return nil
}

// Compile-time check that PostgresCommandStore satisfies CommandStore.
var _ CommandStore = (*PostgresCommandStore)(nil)
