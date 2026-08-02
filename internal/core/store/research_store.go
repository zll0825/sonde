package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"capital_observatory/pkg/model"
)

// defaultObservationsLimit bounds unbounded GetObservations queries when the
// caller doesn't specify a limit. 500 rows covers ~1.5 years of daily or ~7
// years of weekly data — enough context for any detector while protecting the
// database from pathological scans.
const defaultObservationsLimit = 500

// PostgresResearchStore implements research.ResearchStore against a PostgreSQL pool.
type PostgresResearchStore struct {
	db *pgxpool.Pool
}

// NewPostgresResearchStore creates a new PostgresResearchStore.
func NewPostgresResearchStore(db *pgxpool.Pool) *PostgresResearchStore {
	return &PostgresResearchStore{db: db}
}

// GetObservations returns time-sorted (ascending) observations for a metric
// UID in the [since, until] window, bounded to `limit` rows. The caller passes
// a UID (metric_uid column), not a metric_id — the table's
// idx_obs_metric_uid_time index serves this filter. Pass limit <= 0 to use the
// default bound (defaultObservationsLimit).
//
// The query orders DESC so the LIMIT keeps the NEWEST rows — detectors walk
// backwards from the newest observation, so truncating the tail (ASC + LIMIT)
// would freeze evaluation on stale data the moment a window holds more rows
// than the limit. Rows are reversed in memory to honor the ascending contract.
func (s *PostgresResearchStore) GetObservations(ctx context.Context, metricUID string, since, until time.Time, limit int) ([]model.Observation, error) {
	if limit <= 0 {
		limit = defaultObservationsLimit
	}
	rows, err := s.db.Query(ctx, `
		SELECT time, metric_id, metric_uid, value, labels, labels_hash,
		       source_plugin, source_plugin_version, source_provider, source_fetched_at,
		       quality_grade, quality_confidence, system_quality_score
		FROM observations
		WHERE metric_uid = $1 AND time >= $2 AND time <= $3
		ORDER BY time DESC
		LIMIT $4
	`, metricUID, since, until, limit)
	if err != nil {
		return nil, fmt.Errorf("query observations: %w", err)
	}
	defer rows.Close()

	var observations []model.Observation
	for rows.Next() {
		var obs model.Observation
		if err := rows.Scan(
			&obs.Time, &obs.MetricID, &obs.MetricUID, &obs.Value, &obs.Labels, &obs.LabelsHash,
			&obs.SourcePlugin, &obs.SourcePluginVersion, &obs.SourceProvider, &obs.SourceFetchedAt,
			&obs.QualityGrade, &obs.QualityConfidence, &obs.SystemQualityScore,
		); err != nil {
			return nil, fmt.Errorf("scan observation: %w", err)
		}
		observations = append(observations, obs)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate observation rows: %w", err)
	}
	// Rows arrived newest-first (DESC + LIMIT); restore the ascending contract.
	for i, j := 0, len(observations)-1; i < j; i, j = i+1, j-1 {
		observations[i], observations[j] = observations[j], observations[i]
	}
	return observations, nil
}

// GetEntityByID returns the current version (effective_to IS NULL) of an entity
// by its id, or nil if not found.
func (s *PostgresResearchStore) GetEntityByID(ctx context.Context, entityID string) (*model.Entity, error) {
	row := s.db.QueryRow(ctx, `
		SELECT id, name, namespace, entity_type, plugin_id, tags, metadata,
		       version, effective_from, effective_to, supersedes, change_log
		FROM entities_v2
		WHERE id = $1 AND effective_to IS NULL
	`, entityID)

	var (
		ent                      model.Entity
		tagsBytes, metadataBytes []byte
		supersedesPtr            *int
	)
	if err := row.Scan(
		&ent.ID, &ent.Name, &ent.Namespace, &ent.EntityType, &ent.PluginID,
		&tagsBytes, &metadataBytes,
		&ent.Version, &ent.EffectiveFrom, &ent.EffectiveTo, &supersedesPtr, &ent.ChangeLog,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan entity: %w", err)
	}

	if tagsBytes != nil {
		if err := json.Unmarshal(tagsBytes, &ent.Tags); err != nil {
			return nil, fmt.Errorf("unmarshal entity tags: %w", err)
		}
	}
	if metadataBytes != nil {
		if err := json.Unmarshal(metadataBytes, &ent.Metadata); err != nil {
			return nil, fmt.Errorf("unmarshal entity metadata: %w", err)
		}
	}
	if supersedesPtr != nil {
		ent.Supersedes = supersedesPtr
	}
	return &ent, nil
}

// GetRelatedEntities returns all currently-effective relations (effective_to IS
// NULL) where entityID appears as either source or target — a single hop in the
// entity-relation graph.
func (s *PostgresResearchStore) GetRelatedEntities(ctx context.Context, entityID string) ([]model.Relation, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, source_id, target_id, relation_type, layer, direction,
		       confidence, typical_lag, description, source, version,
		       effective_from, effective_to
		FROM relations_v2
		WHERE effective_to IS NULL AND (source_id = $1 OR target_id = $1)
	`, entityID)
	if err != nil {
		return nil, fmt.Errorf("query related entities: %w", err)
	}
	defer rows.Close()

	var relations []model.Relation
	for rows.Next() {
		var rel model.Relation
		if err := rows.Scan(
			&rel.ID, &rel.SourceID, &rel.TargetID, &rel.RelationType, &rel.Layer, &rel.Direction,
			&rel.Confidence, &rel.TypicalLag, &rel.Description, &rel.Source, &rel.Version,
			&rel.EffectiveFrom, &rel.EffectiveTo,
		); err != nil {
			return nil, fmt.Errorf("scan relation: %w", err)
		}
		relations = append(relations, rel)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate relation rows: %w", err)
	}
	return relations, nil
}

// SaveSnapshot writes the research context JSON to research_snapshots. Uses
// upsert semantics so re-running research for the same alert overwrites prior
// snapshots.
func (s *PostgresResearchStore) SaveSnapshot(ctx context.Context, snapshot model.ResearchSnapshot) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO research_snapshots (alert_id, context, ontology_frozen_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (alert_id) DO UPDATE SET
			context = EXCLUDED.context,
			ontology_frozen_at = EXCLUDED.ontology_frozen_at,
			created_at = NOW()
	`, snapshot.AlertID, snapshot.Context, snapshot.OntologyFrozenAt)
	if err != nil {
		return fmt.Errorf("save research snapshot: %w", err)
	}
	return nil
}
