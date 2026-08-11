// Package ontology 扩展：手动关系管理与统计候选。
package ontology

import (
	"context"
	"fmt"
	"time"

	"capital_observatory/pkg/model"
	pb "capital_observatory/pkg/proto/plugin/v1"
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
	RelationID   string    `json:"relation_id"`
	SourceID     string    `json:"source_id"`
	TargetID     string    `json:"target_id"`
	RelationType string    `json:"relation_type"`
	Direction    string    `json:"direction"`
	Description  string    `json:"description,omitempty"`
	Weight       float64   `json:"weight"`
	UserID       string    `json:"user_id"`
	CreatedAt    time.Time `json:"created_at"`
	Active       bool      `json:"active"`
	Evidence     string    `json:"evidence,omitempty"`
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

	if err := rm.insertManualRelation(ctx, relation); err != nil {
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

// Delete 软删除关系。
func (rm *RelationManager) Delete(ctx context.Context, relationID, userID string) error {
	return rm.deactivateManualRelation(ctx, relationID)
}

// GetByID 按 ID 查询。
func (rm *RelationManager) GetByID(ctx context.Context, relationID string) (*ManualRelation, error) {
	row := rm.store.db.QueryRow(ctx, `
		SELECT relation_id, source_id, target_id, relation_type, direction,
		       description, weight, user_id, created_at, active, evidence
		FROM manual_relations
		WHERE relation_id = $1
	`, relationID)

	var r ManualRelation
	err := row.Scan(&r.RelationID, &r.SourceID, &r.TargetID, &r.RelationType,
		&r.Direction, &r.Description, &r.Weight, &r.UserID, &r.CreatedAt, &r.Active, &r.Evidence)
	if err != nil {
		return nil, nil
	}
	return &r, nil
}

// List 列出实体的手动关系。
func (rm *RelationManager) List(ctx context.Context, entityID string, activeOnly bool) ([]ManualRelation, error) {
	query := `
		SELECT relation_id, source_id, target_id, relation_type, direction,
		       description, weight, user_id, created_at, active, evidence
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
			&r.Direction, &r.Description, &r.Weight, &r.UserID, &r.CreatedAt, &r.Active, &r.Evidence); err != nil {
			return nil, fmt.Errorf("scan manual relation: %w", err)
		}
		result = append(result, r)
	}
	return result, nil
}

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

func (rm *RelationManager) insertManualRelation(ctx context.Context, r *ManualRelation) error {
	_, err := rm.store.db.Exec(ctx, `
		INSERT INTO manual_relations (
			relation_id, source_id, target_id, relation_type, direction,
			description, weight, user_id, created_at, active, evidence
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`, r.RelationID, r.SourceID, r.TargetID, r.RelationType, r.Direction,
		r.Description, r.Weight, r.UserID, r.CreatedAt, r.Active, r.Evidence)
	return err
}

func (rm *RelationManager) updateManualRelation(ctx context.Context, r *ManualRelation) error {
	_, err := rm.store.db.Exec(ctx, `
		UPDATE manual_relations SET
			weight = $2, description = $3, active = $4
		WHERE relation_id = $1
	`, r.RelationID, r.Weight, r.Description, r.Active)
	return err
}

func (rm *RelationManager) deactivateManualRelation(ctx context.Context, relationID string) error {
	_, err := rm.store.db.Exec(ctx, `
		UPDATE manual_relations SET active = false WHERE relation_id = $1
	`, relationID)
	return err
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
