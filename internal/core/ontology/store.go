// Package ontology provides database persistence for registration data
// (plugins, entities, metrics, relations, rules).
package ontology

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"capital_observatory/pkg/model"
	pb "capital_observatory/pkg/proto/plugin/v1"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DB is the minimal database interface the store needs.
// Implemented by *pgxpool.Pool (github.com/jackc/pgx/v5/pgxpool) and pgx.Tx.
type DB interface {
	Query(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error)
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Compile-time verification that *pgxpool.Pool satisfies DB.
var _ DB = (*pgxpool.Pool)(nil)

// Store provides persistence for registration data.
type Store struct {
	db DB
}

// NewStore creates a new Store wrapping the given DB handle.
func NewStore(db DB) *Store {
	return &Store{db: db}
}

// RegisterPlugin persists a plugin registration in a single transaction:
//  1. Upsert plugins table (ON CONFLICT UPDATE, increment registration_version)
//  2. Insert entities into entities_v2 (composite PK: id + version)
//  3. Insert metrics into metric_definitions_v2 (with generated uid)
//  4. Insert relation_suggestions
//  5. Insert rule_suggestions
//
// Returns the assigned plugin_id and registration_version.
func (s *Store) RegisterPlugin(ctx context.Context, req *pb.RegisterPluginRequest) (pluginID string, version int, err error) {
	info := req.GetInfo()
	if info == nil {
		return "", 0, fmt.Errorf("plugin info is required")
	}

	pluginID = "plg_" + strings.ToLower(info.GetName())

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// 1. Upsert plugins atomically; RETURNING gives the new registration_version.
	var regVersion int
	err = tx.QueryRow(ctx, `
		INSERT INTO plugins (id, name, version, description, registration_version, updated_at)
		VALUES ($1, $2, $3, $4, 1, NOW())
		ON CONFLICT (id) DO UPDATE SET
			name = EXCLUDED.name,
			version = EXCLUDED.version,
			description = EXCLUDED.description,
			registration_version = plugins.registration_version + 1,
			updated_at = NOW()
		RETURNING registration_version
	`, pluginID, info.GetName(), info.GetVersion(), info.GetDescription()).Scan(&regVersion)
	if err != nil {
		return "", 0, fmt.Errorf("upsert plugin: %w", err)
	}

	// 2. Insert entities.
	for _, ent := range req.GetEntities() {
		if err := upsertEntity(ctx, tx, pluginID, ent, req.GetChangeLog()); err != nil {
			return "", 0, fmt.Errorf("entity %q: %w", ent.GetId(), err)
		}
	}

	// 3. Insert metrics.
	for _, met := range req.GetMetrics() {
		if err := upsertMetric(ctx, tx, pluginID, met, req.GetChangeLog()); err != nil {
			return "", 0, fmt.Errorf("metric %q: %w", met.GetId(), err)
		}
	}

	// 4. Insert relation suggestions.
	for _, rel := range req.GetRelations() {
		if err := insertRelationSuggestion(ctx, tx, pluginID, rel); err != nil {
			return "", 0, fmt.Errorf("relation %q->%q: %w", rel.GetSourceId(), rel.GetTargetId(), err)
		}
	}

	// 5. Insert rule suggestions.
	for _, rule := range req.GetRules() {
		if err := insertRuleSuggestion(ctx, tx, pluginID, rule); err != nil {
			return "", 0, fmt.Errorf("rule %q/%q: %w", rule.GetName(), rule.GetMetricId(), err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return "", 0, fmt.Errorf("commit transaction: %w", err)
	}

	return pluginID, regVersion, nil
}

// GetCurrentEntities returns all currently-effective entities (effective_to IS NULL)
// for a plugin.
func (s *Store) GetCurrentEntities(ctx context.Context, pluginID string) ([]model.Entity, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, name, namespace, entity_type, plugin_id, tags, metadata,
		       version, effective_from, effective_to, supersedes, change_log
		FROM entities_v2
		WHERE plugin_id = $1 AND effective_to IS NULL
	`, pluginID)
	if err != nil {
		return nil, fmt.Errorf("query entities: %w", err)
	}
	defer rows.Close()

	var entities []model.Entity
	for rows.Next() {
		var (
			ent                      model.Entity
			tagsBytes, metadataBytes []byte
			supersedesPtr            *int
		)
		if err := rows.Scan(
			&ent.ID, &ent.Name, &ent.Namespace, &ent.EntityType, &ent.PluginID,
			&tagsBytes, &metadataBytes,
			&ent.Version, &ent.EffectiveFrom, &ent.EffectiveTo, &supersedesPtr, &ent.ChangeLog,
		); err != nil {
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
		entities = append(entities, ent)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate entity rows: %w", err)
	}
	return entities, nil
}

// GetCurrentMetrics returns all currently-effective metric definitions
// (effective_to IS NULL) for a plugin.
func (s *Store) GetCurrentMetrics(ctx context.Context, pluginID string) ([]model.MetricDefinition, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, uid, name, description, unit, frequency, entity_id, plugin_id,
		       tags, active, version, effective_from, effective_to, supersedes, change_log
		FROM metric_definitions_v2
		WHERE plugin_id = $1 AND effective_to IS NULL
	`, pluginID)
	if err != nil {
		return nil, fmt.Errorf("query metrics: %w", err)
	}
	defer rows.Close()

	var metrics []model.MetricDefinition
	for rows.Next() {
		var (
			m         model.MetricDefinition
			tagsBytes []byte
		)
		if err := rows.Scan(
			&m.ID, &m.UID, &m.Name, &m.Description, &m.Unit, &m.Frequency,
			&m.EntityID, &m.PluginID, &tagsBytes, &m.Active,
			&m.Version, &m.EffectiveFrom, &m.EffectiveTo, &m.Supersedes, &m.ChangeLog,
		); err != nil {
			return nil, fmt.Errorf("scan metric: %w", err)
		}
		if tagsBytes != nil {
			if err := json.Unmarshal(tagsBytes, &m.Tags); err != nil {
				return nil, fmt.Errorf("unmarshal metric tags: %w", err)
			}
		}
		metrics = append(metrics, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate metric rows: %w", err)
	}
	return metrics, nil
}

// CreateMetricUID generates a unique metric UID with the format "mtr_" + 12 hex
// chars drawn from crypto/rand entropy. Falls back to a UUID-derived value if
// the random source fails.
func CreateMetricUID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		// Deterministic fallback: derive 12 hex chars from a random UUID.
		u := uuid.New()
		return "mtr_" + hex.EncodeToString(u[:])[:12]
	}
	return "mtr_" + hex.EncodeToString(b)
}

// ---------------------------------------------------------------------------
// unexported helpers
// ---------------------------------------------------------------------------

// upsertEntity inserts a new versioned row into entities_v2, superseding any
// prior version (effective_to IS NULL) of the same entity id.
func upsertEntity(ctx context.Context, tx pgx.Tx, pluginID string, ent *pb.EntityDeclaration, changeLog string) error {
	// Determine the current max version for this entity id.
	var maxVersion int
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(version), 0) FROM entities_v2 WHERE id = $1
	`, ent.GetId()).Scan(&maxVersion); err != nil {
		return fmt.Errorf("query max entity version: %w", err)
	}

	newVersion := maxVersion + 1
	supersedes := maxVersion > 0

	// Close the prior effective version.
	if supersedes {
		if _, err := tx.Exec(ctx, `
			UPDATE entities_v2 SET effective_to = NOW()
			WHERE id = $1 AND effective_to IS NULL
		`, ent.GetId()); err != nil {
			return fmt.Errorf("close prior entity version: %w", err)
		}
	}

	// Marshal JSONB columns.
	tags := ent.GetTags()
	if tags == nil {
		tags = []string{}
	}
	tagsJSON, err := json.Marshal(tags)
	if err != nil {
		return fmt.Errorf("marshal entity tags: %w", err)
	}

	metadata := ent.GetMetadata()
	if metadata == nil {
		metadata = make(map[string]string)
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("marshal entity metadata: %w", err)
	}

	var supersedesVal *int
	if supersedes {
		supersedesVal = &maxVersion
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO entities_v2 (
			id, version, name, namespace, entity_type, plugin_id,
			tags, metadata, effective_from, effective_to, supersedes, change_log
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW(), NULL, $9, $10)
	`, ent.GetId(), newVersion, ent.GetName(), ent.GetNamespace(),
		entityTypeToString(ent.GetEntityType()), pluginID,
		tagsJSON, metadataJSON, supersedesVal, changeLog)

	if err != nil {
		return fmt.Errorf("insert entity: %w", err)
	}
	return nil
}

// upsertMetric inserts a new versioned row into metric_definitions_v2,
// superseding any prior version. The uid is the metric's stable system-wide
// identity: generated once at version 1 and inherited by every later version,
// so observations keyed by metric_uid stay joinable across redefinitions.
func upsertMetric(ctx context.Context, tx pgx.Tx, pluginID string, met *pb.MetricDeclaration, changeLog string) error {
	var maxVersion int
	var priorUID *string
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(version), 0),
		       (SELECT uid FROM metric_definitions_v2 WHERE id = $1 AND effective_to IS NULL LIMIT 1)
		FROM metric_definitions_v2 WHERE id = $1
	`, met.GetId()).Scan(&maxVersion, &priorUID); err != nil {
		return fmt.Errorf("query max metric version: %w", err)
	}

	newVersion := maxVersion + 1
	supersedes := maxVersion > 0

	if supersedes {
		if _, err := tx.Exec(ctx, `
			UPDATE metric_definitions_v2 SET effective_to = NOW()
			WHERE id = $1 AND effective_to IS NULL
		`, met.GetId()); err != nil {
			return fmt.Errorf("close prior metric version: %w", err)
		}
	}

	tags := met.GetTags()
	if tags == nil {
		tags = make(map[string]string)
	}
	tagsJSON, err := json.Marshal(tags)
	if err != nil {
		return fmt.Errorf("marshal metric tags: %w", err)
	}

	uid := CreateMetricUID()
	if priorUID != nil && *priorUID != "" {
		uid = *priorUID
	}

	var supersedesVal *int
	if supersedes {
		supersedesVal = &maxVersion
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO metric_definitions_v2 (
			id, uid, version, name, description, unit, frequency,
			entity_id, plugin_id, tags, active,
			effective_from, effective_to, supersedes, change_log
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, TRUE,
		          NOW(), NULL, $11, $12)
	`, met.GetId(), uid, newVersion, met.GetName(), met.GetDescription(),
		met.GetUnit(), met.GetFrequency(), met.GetEntityId(), pluginID,
		tagsJSON, supersedesVal, changeLog)

	if err != nil {
		return fmt.Errorf("insert metric: %w", err)
	}
	return nil
}

// insertRelationSuggestion writes a relation_suggestions row (idempotent via ON CONFLICT).
func insertRelationSuggestion(ctx context.Context, tx pgx.Tx, pluginID string, rel *pb.RelationSuggestion) error {
	var typicalLag *string
	if secs := rel.GetTypicalLagSecs(); secs > 0 {
		s := fmt.Sprintf("%ds", secs)
		typicalLag = &s
	}

	_, err := tx.Exec(ctx, `
		INSERT INTO relation_suggestions (
			source_id, target_id, relation_type, direction, confidence,
			typical_lag, description, evidence, plugin_id, status
		) VALUES ($1, $2, $3, $4, $5, $6::interval, $7, $8, $9, 'pending')
		ON CONFLICT (source_id, target_id, relation_type, plugin_id) DO NOTHING
	`, rel.GetSourceId(), rel.GetTargetId(), rel.GetRelationType(),
		directionToString(rel.GetDirection()), rel.GetConfidence(),
		typicalLag, rel.GetDescription(), rel.GetEvidence(), pluginID)

	if err != nil {
		return fmt.Errorf("insert relation suggestion: %w", err)
	}
	return nil
}

// insertRuleSuggestion writes a rule_suggestions row (idempotent via ON CONFLICT).
func insertRuleSuggestion(ctx context.Context, tx pgx.Tx, pluginID string, rule *pb.RuleSuggestion) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO rule_suggestions (
			name, metric_id, detector_name, severity, config, description, plugin_id, status
		) VALUES ($1, $2, $3, $4, $5, $6, $7, 'pending')
		ON CONFLICT (name, metric_id, detector_name, plugin_id) DO NOTHING
	`, rule.GetName(), rule.GetMetricId(), rule.GetDetectorName(),
		severityToString(rule.GetSeverity()), rule.GetConfig(),
		rule.GetDescription(), pluginID)

	if err != nil {
		return fmt.Errorf("insert rule suggestion: %w", err)
	}
	return nil
}

// InsertObservation writes a scored, source-resolved observation into the observations table.
// metricUID is the registered uid from metric_definitions_v2 (resolved by the caller);
// it is what research-time joins use, so it must never be re-derived here.
// The unique index idx_obs_idempotency enforces dedup: same (metric_uid, time, source, labels_hash)
// returns a PG 23505 unique violation which callers should interpret as "duplicate".
//
// Quality values are passed directly (no dependency on metric.QualityResult avoids an import cycle).
func (s *Store) InsertObservation(ctx context.Context, snap *pb.MetricSnapshot, metricUID, grade string, confidence float64, systemScore float64, labelsHash, pluginID string) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO observations (
			time, metric_id, metric_uid, value, labels, labels_hash,
			source_plugin, source_plugin_version, source_provider, source_fetched_at,
			quality_grade, quality_confidence, system_quality_score, ingested_at
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10,
			$11, $12, $13, NOW()
		)
	`, time.Unix(snap.Timestamp, 0), snap.MetricId, metricUID, snap.Value,
		labelsToBytes(snap.Labels), labelsHash,
		pluginID, snap.SourcePluginVersion, snap.SourceProvider,
		time.Unix(snap.SourceFetchedAt, 0),
		grade, confidence, systemScore)

	if err != nil {
		return fmt.Errorf("insert observation: %w", err)
	}
	return nil
}

// labelsToBytes serializes a labels map for the JSONB column.
func labelsToBytes(labels map[string]string) []byte {
	if len(labels) == 0 {
		return []byte("{}")
	}
	b, err := json.Marshal(labels)
	if err != nil {
		return []byte("{}")
	}
	return b
}

// entityTypeToString converts a proto EntityType enum to its lowercase DB label.
func entityTypeToString(et pb.EntityType) string {
	switch et {
	case pb.EntityType_ENTITY_TYPE_ASSET:
		return "asset"
	case pb.EntityType_ENTITY_TYPE_INSTRUMENT:
		return "instrument"
	case pb.EntityType_ENTITY_TYPE_FLOW:
		return "flow"
	case pb.EntityType_ENTITY_TYPE_INSTITUTION:
		return "institution"
	case pb.EntityType_ENTITY_TYPE_INDICATOR:
		return "indicator"
	case pb.EntityType_ENTITY_TYPE_INDEX:
		return "index"
	case pb.EntityType_ENTITY_TYPE_MARKET:
		return "market"
	default:
		return "asset"
	}
}

// directionToString converts a proto Direction enum to its lowercase DB label.
func directionToString(d pb.Direction) string {
	switch d {
	case pb.Direction_DIRECTION_FORWARD:
		return "forward"
	case pb.Direction_DIRECTION_BACKWARD:
		return "backward"
	case pb.Direction_DIRECTION_BIDIRECTIONAL:
		return "bidirectional"
	default:
		return "forward"
	}
}

// severityToString converts a proto Severity enum to its lowercase DB label.
func severityToString(s pb.Severity) string {
	switch s {
	case pb.Severity_SEVERITY_CRITICAL:
		return "critical"
	case pb.Severity_SEVERITY_WARNING:
		return "warning"
	case pb.Severity_SEVERITY_INFO:
		return "info"
	default:
		return "warning"
	}
}
