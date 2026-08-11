// Package ontology — Phase 2 candidate / suggestion management helpers.
//
// These store- and manager-level helpers back the ontology management API:
// listing pending decisions, accepting them into manual_relations, or
// rejecting them with a reason.
package ontology

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// PendingSuggestion is a relation_suggestions row awaiting human review. It
// carries a stable integer ID (the table's BIGSERIAL primary key) so the API
// can reference individual candidates.
type PendingSuggestion struct {
	ID           int64     `json:"id"`
	SourceID     string    `json:"source_id"`
	TargetID     string    `json:"target_id"`
	RelationType string    `json:"relation_type"`
	Direction    string    `json:"direction"`
	Confidence   float64   `json:"confidence"`
	Description  string    `json:"description,omitempty"`
	Evidence     string    `json:"evidence,omitempty"`
	TypicalLag   string    `json:"typical_lag,omitempty"`
	PluginID     string    `json:"plugin_id"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
}

// ErrSuggestionNotPending is returned by AcceptSuggestion / RejectSuggestion
// when the row has already been resolved (no longer 'pending').
var ErrSuggestionNotPending = errors.New("suggestion is not pending review")

// ErrAlreadyReviewed is returned by AcceptSuggestionByCandidate when the
// relation_suggestions row was reviewed by someone else first (concurrent
// accept — the UPDATE affected zero rows).
var ErrAlreadyReviewed = errors.New("suggestion already reviewed")

// ListPendingSuggestions returns all relation_suggestions rows still awaiting
// review (status = 'pending'). Ordered by id ASC for stable paging.
func (s *Store) ListPendingSuggestions(ctx context.Context) ([]PendingSuggestion, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, source_id, target_id, relation_type, direction,
		       confidence, description, evidence, typical_lag, plugin_id,
		       status, created_at
		FROM relation_suggestions
		WHERE status = 'pending'
		ORDER BY id ASC
		LIMIT 200
	`)
	if err != nil {
		return nil, fmt.Errorf("list pending suggestions: %w", err)
	}
	defer rows.Close()

	var out []PendingSuggestion
	for rows.Next() {
		var p PendingSuggestion
		var lag *string
		if err := rows.Scan(
			&p.ID, &p.SourceID, &p.TargetID, &p.RelationType, &p.Direction,
			&p.Confidence, &p.Description, &p.Evidence, &lag, &p.PluginID,
			&p.Status, &p.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan pending suggestion: %w", err)
		}
		if lag != nil {
			p.TypicalLag = *lag
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending suggestions: %w", err)
	}
	return out, nil
}

// GetPendingSuggestionByID returns a single pending suggestion by its
// BIGSERIAL primary key. Returns (nil, nil) when the row is not pending or
// does not exist; (nil, err) for DB-level errors.
func (s *Store) GetPendingSuggestionByID(ctx context.Context, id int64) (*PendingSuggestion, error) {
	row := s.db.QueryRow(ctx, `
		SELECT id, source_id, target_id, relation_type, direction,
		       confidence, description, evidence, typical_lag, plugin_id,
		       status, created_at
		FROM relation_suggestions
		WHERE id = $1 AND status = 'pending'
	`, id)
	return scanPendingSuggestion(row)
}

func scanPendingSuggestion(row pgx.Row) (*PendingSuggestion, error) {
	var p PendingSuggestion
	var lag *string
	if err := row.Scan(
		&p.ID, &p.SourceID, &p.TargetID, &p.RelationType, &p.Direction,
		&p.Confidence, &p.Description, &p.Evidence, &lag, &p.PluginID,
		&p.Status, &p.CreatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan pending suggestion: %w", err)
	}
	if lag != nil {
		p.TypicalLag = *lag
	}
	return &p, nil
}

// AcceptSuggestionByCandidate promotes a pending suggestion into a
// user-confirmed ManualRelation. Both the manual_relation insert and the
// relation_suggestions status flip run in a single transaction so a crash can
// never leave a row accepted without a relation or vice-versa.
func (rm *RelationManager) AcceptSuggestionByCandidate(ctx context.Context, suggestion PendingSuggestion, userID string) (*ManualRelation, error) {
	tx, err := rm.store.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	candidate := candidateFromSuggestion(suggestion)
	rel, err := rm.txAcceptCandidate(ctx, tx, candidate, userID)
	if err != nil {
		return nil, err
	}

	tag, err := tx.Exec(ctx, `
		UPDATE relation_suggestions
		SET reviewed_at = NOW(), reviewed_by = $1, decision = 'accepted'
		WHERE id = $2 AND decision = 'pending'
	`, userID, suggestion.ID)
	if err != nil {
		return nil, fmt.Errorf("mark accepted: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrAlreadyReviewed
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return rel, nil
}

// candidateFromSuggestion converts a PendingSuggestion into the Candidate
// shape RelationManager expects. The suggestion's Direction is preserved as-is
// (typically "forward" because the candidate discovery pipeline always infers
// forward directionality).
func candidateFromSuggestion(suggestion PendingSuggestion) Candidate {
	return Candidate{
		SourceID:     suggestion.SourceID,
		TargetID:     suggestion.TargetID,
		RelationType: suggestion.RelationType,
		Direction:    suggestion.Direction,
		Confidence:   suggestion.Confidence,
	}
}

// AcceptSuggestion marks a pending suggestion as accepted directly in the
// relation_suggestions table. Use AcceptSuggestionByCandidate when you also
// want to promote it to a ManualRelation. This helper is the low-level status
// flip; it returns ErrSuggestionNotPending when the row is no longer pending.
func (s *Store) AcceptSuggestion(ctx context.Context, id int64, userID string) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE relation_suggestions
		SET status = 'accepted', review_reason = COALESCE(NULLIF(review_reason, ''), $2)
		WHERE id = $1 AND status = 'pending'
	`, id, "accepted by "+userID)
	if err != nil {
		return fmt.Errorf("accept suggestion %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrSuggestionNotPending
	}
	return nil
}

// RejectSuggestion marks a pending suggestion as rejected with a reason.
// Returns ErrSuggestionNotPending when the row is no longer pending.
func (s *Store) RejectSuggestion(ctx context.Context, id int64, reason, userID string) error {
	if reason == "" {
		reason = "no reason given"
	}
	tag, err := s.db.Exec(ctx, `
		UPDATE relation_suggestions
		SET status = 'rejected', review_reason = $2
		WHERE id = $1 AND status = 'pending'
	`, id, "rejected by "+userID+": "+reason)
	if err != nil {
		return fmt.Errorf("reject suggestion %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrSuggestionNotPending
	}
	return nil
}

// ListAllManualRelations lists active manual_relations. When entityID is non-
// empty, only relations touching that entity are returned.
func (rm *RelationManager) ListAllManualRelations(ctx context.Context, entityID string) ([]ManualRelation, error) {
	if entityID != "" {
		return rm.List(ctx, entityID, true)
	}
	rows, err := rm.store.db.Query(ctx, `
		SELECT relation_id, source_id, target_id, relation_type, direction,
		       description, weight, user_id, created_at, active, evidence, last_updated_at
		FROM manual_relations
		WHERE active = true
		ORDER BY created_at DESC
		LIMIT 200
	`)
	if err != nil {
		return nil, fmt.Errorf("list all manual relations: %w", err)
	}
	defer rows.Close()

	var result []ManualRelation
	for rows.Next() {
		var r ManualRelation
		if err := rows.Scan(&r.RelationID, &r.SourceID, &r.TargetID, &r.RelationType,
			&r.Direction, &r.Description, &r.Weight, &r.UserID, &r.CreatedAt,
			&r.Active, &r.Evidence, &r.LastUpdatedAt); err != nil {
			return nil, fmt.Errorf("scan manual relation: %w", err)
		}
		result = append(result, r)
	}
	return result, nil
}
