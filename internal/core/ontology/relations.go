// Package ontology 扩展：手动关系管理与统计候选。
package ontology

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"capital_observatory/internal/core/relationmgr"
	"capital_observatory/pkg/model"
	pb "capital_observatory/pkg/proto/plugin/v1"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"
)

// RelationInput 用户手动创建关系的输入。
type RelationInput struct {
	SourceID     string  `json:"source_id"`
	TargetID     string  `json:"target_id"`
	RelationType string  `json:"relation_type"`
	Direction    string  `json:"direction"`
	Description  string  `json:"description,omitempty"`
	Weight       float64 `json:"weight,omitempty"` // 0..1, 用户自定义强度
	UserID       string  `json:"user_id"`
}

// ManualRelation 手动创建的关系（用户确认）。
type ManualRelation struct {
	RelationID    string    `json:"relation_id"`
	SourceID      string    `json:"source_id"`
	TargetID      string    `json:"target_id"`
	RelationType  string    `json:"relation_type"`
	Direction     string    `json:"direction"`
	Description   string    `json:"description,omitempty"`
	Weight        float64   `json:"weight"`
	UserID        string    `json:"user_id"`
	CreatedAt     time.Time `json:"created_at"`
	Active        bool      `json:"active"`
	Evidence      string    `json:"evidence,omitempty"`
	Extension     string    `json:"extension,omitempty"`
	LastUpdatedAt time.Time `json:"last_updated_at,omitempty"`
}

// RelationManager 手动关系管理。
type RelationManager struct {
	store *Store
}

// NewRelationManager creates a manager.
func NewRelationManager(store *Store) *RelationManager {
	return &RelationManager{store: store}
}

// Create 创建手动关系。
func (rm *RelationManager) Create(ctx context.Context, input RelationInput) (*ManualRelation, error) {
	if input.SourceID == input.TargetID {
		return nil, fmt.Errorf("source and target entities must differ")
	}
	if _, ok := relationmgr.LayerOf(input.RelationType); !ok {
		return nil, fmt.Errorf("unknown relation type %q", input.RelationType)
	}
	if input.Direction != "forward" && input.Direction != "undirected" {
		return nil, fmt.Errorf("direction must be forward or undirected")
	}
	// 验证实体存在
	if err := rm.validateEntities(ctx, input.SourceID, input.TargetID); err != nil {
		return nil, err
	}

	relation := &ManualRelation{
		RelationID:   generateRelationID(),
		SourceID:     input.SourceID,
		TargetID:     input.TargetID,
		RelationType: input.RelationType,
		Direction:    input.Direction,
		Description:  input.Description,
		Weight:       clampWeight(input.Weight),
		UserID:       input.UserID,
		CreatedAt:    time.Now(),
		Active:       true,
	}

	if err := insertManualRelation(ctx, rm.store.db, relation); err != nil {
		return nil, fmt.Errorf("insert manual relation: %w", err)
	}
	return relation, nil
}

// Update 更新关系。
func (rm *RelationManager) Update(ctx context.Context, relationID string, updates map[string]interface{}, userID string) (*ManualRelation, error) {
	existing, err := rm.GetByID(ctx, relationID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, fmt.Errorf("relation %s not found", relationID)
	}

	// 应用更新
	if v, ok := updates["weight"].(float64); ok {
		existing.Weight = clampWeight(v)
	}
	if v, ok := updates["description"].(string); ok {
		existing.Description = v
	}
	if v, ok := updates["active"].(bool); ok {
		existing.Active = v
	}

	if err := rm.updateManualRelation(ctx, existing); err != nil {
		return nil, fmt.Errorf("update relation: %w", err)
	}
	return existing, nil
}

// Delete 软删除关系。userID 会写入 evidence 列供审计使用。
func (rm *RelationManager) Delete(ctx context.Context, relationID, userID string) error {
	if err := rm.auditDeactivate(ctx, relationID, userID); err != nil {
		return fmt.Errorf("deactivate manual relation: %w", err)
	}
	return nil
}

// GetByID 按 ID 查询。明确区分 "未找到"(nil, nil) 和 DB 错误(nil, err)。
func (rm *RelationManager) GetByID(ctx context.Context, relationID string) (*ManualRelation, error) {
	row := rm.store.db.QueryRow(ctx, `
		SELECT relation_id, source_id, target_id, relation_type, direction,
		       description, weight, user_id, created_at, active, evidence, last_updated_at
		FROM manual_relations
		WHERE relation_id = $1
	`, relationID)

	var r ManualRelation
	err := row.Scan(&r.RelationID, &r.SourceID, &r.TargetID, &r.RelationType,
		&r.Direction, &r.Description, &r.Weight, &r.UserID, &r.CreatedAt, &r.Active, &r.Evidence, &r.LastUpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("query manual relation %s: %w", relationID, err)
	}
	return &r, nil
}

// List 列出实体的手动关系。
func (rm *RelationManager) List(ctx context.Context, entityID string, activeOnly bool) ([]ManualRelation, error) {
	query := `
		SELECT relation_id, source_id, target_id, relation_type, direction,
		       description, weight, user_id, created_at, active, evidence, last_updated_at
		FROM manual_relations
		WHERE (source_id = $1 OR target_id = $2)
	`
	if activeOnly {
		query += " AND active = true"
	}
	query += " ORDER BY created_at DESC"

	rows, err := rm.store.db.Query(ctx, query, entityID, entityID)
	if err != nil {
		return nil, fmt.Errorf("list manual relations: %w", err)
	}
	defer rows.Close()

	var result []ManualRelation
	for rows.Next() {
		var r ManualRelation
		if err := rows.Scan(&r.RelationID, &r.SourceID, &r.TargetID, &r.RelationType,
			&r.Direction, &r.Description, &r.Weight, &r.UserID, &r.CreatedAt, &r.Active, &r.Evidence, &r.LastUpdatedAt); err != nil {
			return nil, fmt.Errorf("scan manual relation: %w", err)
		}
		result = append(result, r)
	}
	return result, nil
}

// AcceptCandidate promotes a vetted candidate to a real ManualRelation.
// It validates entities then inserts the row via the store DB.
func (rm *RelationManager) AcceptCandidate(ctx context.Context, candidate Candidate, userID string) (*ManualRelation, error) {
	input := RelationInput{
		SourceID:     candidate.SourceID,
		TargetID:     candidate.TargetID,
		RelationType: candidate.RelationType,
		Direction:    candidate.Direction,
		Weight:       candidate.Confidence,
		UserID:       userID,
	}
	return rm.Create(ctx, input)
}

// txAcceptCandidate is the transactional variant of AcceptCandidate. It
// inserts the manual_relation row using the supplied pgx.Tx and skips entity
// validation (the candidate was already validated upstream when the
// PendingSuggestion was created). Used by AcceptSuggestionByCandidate which
// writes the manual_relation and the suggestion review row atomically.
func (rm *RelationManager) txAcceptCandidate(ctx context.Context, tx pgx.Tx, candidate Candidate, userID string) (*ManualRelation, error) {
	extension := map[string]any{
		"p_value":       candidate.PVale,
		"sample_size":   candidate.Observations,
		"lookback_days": candidate.LookbackDays,
		"lag_days":      candidate.LagDays,
		"evidence_refs": candidate.EvidenceRefs,
	}
	extJSON, err := json.Marshal(extension)
	if err != nil {
		extJSON = []byte("{}")
	}

	rel := &ManualRelation{
		RelationID:    generateRelationID(),
		SourceID:      candidate.SourceID,
		TargetID:      candidate.TargetID,
		RelationType:  candidate.RelationType,
		Direction:     candidate.Direction,
		Weight:        clampWeight(candidate.Confidence),
		UserID:        userID,
		CreatedAt:     time.Now(),
		Active:        true,
		Extension:     string(extJSON),
		LastUpdatedAt: time.Now(),
	}
	if err := insertManualRelation(ctx, tx, rel); err != nil {
		return nil, fmt.Errorf("insert manual relation in tx: %w", err)
	}
	return rel, nil
}

// [P2#15] RejectCandidate has been removed. It depended on the non-existent
// candidate_rejections table and silently lost rejection decisions. The API's
// accept-reject path in cmd/api/ontology_handlers.go uses
// relation_suggestions.status + review_reason (SuggestionReviewStatus) instead.
// Rebuilding candidate_rejections is tracked separately if a richer rejection
// audit is needed.

func (rm *RelationManager) validateEntities(ctx context.Context, sourceID, targetID string) error {
	var exists bool
	err := rm.store.db.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM entities WHERE id = $1 AND effective_to IS NULL)
	`, sourceID).Scan(&exists)
	if err != nil || !exists {
		return fmt.Errorf("source entity %s not found", sourceID)
	}
	err = rm.store.db.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM entities WHERE id = $1 AND effective_to IS NULL)
	`, targetID).Scan(&exists)
	if err != nil || !exists {
		return fmt.Errorf("target entity %s not found", targetID)
	}
	return nil
}

// insertManualRelation writes a manual_relations row via the supplied
// DB-compatible executor. Both *pgxpool.Pool and pgx.Tx satisfy DB, so this
// helper works inside and outside of transactions.
func insertManualRelation(ctx context.Context, db DB, r *ManualRelation) error {
	_, err := db.Exec(ctx, `
		INSERT INTO manual_relations (
			relation_id, source_id, target_id, relation_type, direction,
			description, weight, user_id, created_at, active, evidence, extension,
			last_updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
	`, r.RelationID, r.SourceID, r.TargetID, r.RelationType, r.Direction,
		r.Description, r.Weight, r.UserID, r.CreatedAt, r.Active, r.Evidence, r.Extension,
		r.LastUpdatedAt)
	return err
}

func (rm *RelationManager) updateManualRelation(ctx context.Context, r *ManualRelation) error {
	r.LastUpdatedAt = time.Now()
	_, err := rm.store.db.Exec(ctx, `
		UPDATE manual_relations SET
			weight = $2, description = $3, active = $4, last_updated_at = $5
		WHERE relation_id = $1
	`, r.RelationID, r.Weight, r.Description, r.Active, r.LastUpdatedAt)
	return err
}

// auditDeactivate 软删除关系，同时把 deleted_by / deleted_at 写入 evidence
// 供审计；last_updated_at 刷新当前时间。
func (rm *RelationManager) auditDeactivate(ctx context.Context, relationID, userID string) error {
	// 先把旧 evidence 读出，再与审计信息合并写回
	var existingEvidence string
	err := rm.store.db.QueryRow(ctx, `
		SELECT COALESCE(evidence, '') FROM manual_relations WHERE relation_id = $1
	`, relationID).Scan(&existingEvidence)
	if err != nil {
		return fmt.Errorf("load existing evidence for %s: %w", relationID, err)
	}

	audit := map[string]interface{}{
		"deleted_by": userID,
		"deleted_at": time.Now().Format(time.RFC3339),
	}
	merged := mergeEvidence(existingEvidence, audit)

	now := time.Now()
	_, err = rm.store.db.Exec(ctx, `
		UPDATE manual_relations SET active = false, evidence = $2, last_updated_at = $3
		WHERE relation_id = $1
	`, relationID, merged, now)
	if err != nil {
		return fmt.Errorf("deactivate relation %s: %w", relationID, err)
	}

	log.Info().
		Str("relation_id", relationID).
		Str("deleted_by", userID).
		Time("at", now).
		Msg("manual relation deactivated")
	return nil
}

// mergeEvidence 把额外 JSON 字段合并到已有 evidence 字符串（JSON 对象形式）。
// 入参 existing 可能为空字符串、"{}" 或已包含字段的 JSON 文本。
func mergeEvidence(existing string, extra map[string]interface{}) string {
	base := map[string]interface{}{}
	if existing != "" {
		_ = json.Unmarshal([]byte(existing), &base)
	}
	for k, v := range extra {
		base[k] = v
	}
	out, err := json.Marshal(base)
	if err != nil {
		// 绝对不该发生的回退
		return "{}"
	}
	return string(out)
}

func generateRelationID() string {
	uid := CreateMetricUID()
	if len(uid) > 4 {
		return "rel_" + uid[4:]
	}
	return "rel_unknown"
}

func clampWeight(w float64) float64 {
	if w < 0 {
		return 0
	}
	if w > 1 {
		return 1
	}
	return w
}

var _ = pb.RelationSuggestion{}
var _ = model.Relation{}
