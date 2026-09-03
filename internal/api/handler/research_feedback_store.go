package handler

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"capital_observatory/pkg/model"
)

type postgresResearchFeedbackStore struct {
	db *pgxpool.Pool
}

func newResearchFeedbackStore(db *pgxpool.Pool) *postgresResearchFeedbackStore {
	return &postgresResearchFeedbackStore{db: db}
}

func (s *postgresResearchFeedbackStore) SaveFeedback(
	ctx context.Context, alertID, clusterID, verdict, rationale, userID string,
) error {
	var cID, rat, uid *string
	if clusterID != "" {
		cID = &clusterID
	}
	if rationale != "" {
		rat = &rationale
	}
	if userID != "" {
		uid = &userID
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO research_feedbacks (alert_id, cluster_id, verdict, rationale, user_id)
		VALUES ($1, $2, $3, $4, $5)
	`, alertID, cID, verdict, rat, uid)
	if err != nil {
		return fmt.Errorf("save research feedback: %w", err)
	}
	return nil
}

func (s *postgresResearchFeedbackStore) ListFeedbacks(
	ctx context.Context, alertID string,
) ([]model.ResearchFeedback, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, alert_id, cluster_id, verdict, rationale, user_id, created_at
		FROM research_feedbacks
		WHERE alert_id = $1
		ORDER BY created_at DESC
	`, alertID)
	if err != nil {
		return nil, fmt.Errorf("query research feedbacks: %w", err)
	}
	defer rows.Close()

	feedbacks := []model.ResearchFeedback{}
	for rows.Next() {
		var feedback model.ResearchFeedback
		if err := rows.Scan(
			&feedback.ID, &feedback.AlertID, &feedback.ClusterID, &feedback.Verdict,
			&feedback.Rationale, &feedback.UserID, &feedback.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan research feedback: %w", err)
		}
		feedbacks = append(feedbacks, feedback)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate research feedback rows: %w", err)
	}
	return feedbacks, nil
}
