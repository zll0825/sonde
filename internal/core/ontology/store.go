// Package ontology 持久化插件注册的本体数据（插件、实体、指标定义、关系、
// 规则），行级版本管理（version + effective_from/to），并为检测引擎提供
// 规则与指标频率查询。
package ontology

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	coreevent "capital_observatory/internal/core/event"
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
	QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row
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

// RegisterPlugin persists a plugin registration in a single transaction.
//
// Pipeline (M1 registration with review integration):
//  1. Ensure the plugins row exists (no version bump yet — entities/metrics
//     and suggestions carry FKs to plugins.id).
//  2. Diff-skip: compute once which entities/metrics differ from their current
//     effective rows; insert new versions only for those.
//  3. reviewAndAcceptRelations → relations (accepted) / relation_suggestions
//     (pending or rejected-for-audit). Identical re-registrations write nothing.
//  4. reviewAndAcceptRules → rules (accepted) / rule_suggestions (conflict).
//  5. Bump registration_version only when something was actually written
//     (or on first registration), so reconnect spam never inflates versions.
//
// Returns the assigned plugin_id and the new (or unchanged) registration_version.
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

	// 1. Ensure the plugins row (registration_version starts at 0 and is bumped
	// below once we know whether this registration changed anything).
	var regVersion int
	err = tx.QueryRow(ctx, `
		INSERT INTO plugins (id, name, version, description, registration_version, updated_at)
		VALUES ($1, $2, $3, $4, 0, NOW())
		ON CONFLICT (id) DO UPDATE SET
			name = EXCLUDED.name,
			version = EXCLUDED.version,
			description = EXCLUDED.description,
			updated_at = NOW()
		RETURNING registration_version
	`, pluginID, info.GetName(), info.GetVersion(), info.GetDescription()).Scan(&regVersion)
	if err != nil {
		return "", 0, fmt.Errorf("upsert plugin: %w", err)
	}
	isFirstRegistration := regVersion == 0

	// 2. Diff once, then insert only changed entities/metrics.
	entitiesChanged := 0
	for _, ent := range req.GetEntities() {
		if !s.entityChanged(ctx, tx, pluginID, ent) {
			continue
		}
		if err := upsertEntity(ctx, tx, pluginID, ent, req.GetChangeLog()); err != nil {
			return "", 0, fmt.Errorf("entity %q: %w", ent.GetId(), err)
		}
		entitiesChanged++
	}

	metricsChanged := 0
	for _, met := range req.GetMetrics() {
		if !s.metricChanged(ctx, tx, pluginID, met) {
			continue
		}
		if err := upsertMetric(ctx, tx, pluginID, met, req.GetChangeLog()); err != nil {
			return "", 0, fmt.Errorf("metric %q: %w", met.GetId(), err)
		}
		metricsChanged++
	}

	// 3. Review + accept relations (accepted counts real writes only).
	relationsAccepted, _, _, rerr := reviewAndAcceptRelations(ctx, tx, pluginID, req.GetRelations())
	if rerr != nil {
		return "", 0, fmt.Errorf("review relations: %w", rerr)
	}

	// 4. Review + accept rules.
	rulesAccepted, _, _, rerr := reviewAndAcceptRules(ctx, tx, pluginID, req.GetRules())
	if rerr != nil {
		return "", 0, fmt.Errorf("review rules: %w", rerr)
	}

	// 5. Bump only when the registration materialized a change.
	changed := entitiesChanged > 0 || metricsChanged > 0 ||
		relationsAccepted > 0 || rulesAccepted > 0 || isFirstRegistration
	if changed {
		if err := tx.QueryRow(ctx, `
			UPDATE plugins SET registration_version = registration_version + 1, updated_at = NOW()
			WHERE id = $1
			RETURNING registration_version
		`, pluginID).Scan(&regVersion); err != nil {
			return "", 0, fmt.Errorf("bump registration version: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return "", 0, fmt.Errorf("commit transaction: %w", err)
	}

	return pluginID, regVersion, nil
}

// entityChanged returns true if the entity's content differs from the current
// effective row (effective_to IS NULL) for the same (id, plugin_id).
func (s *Store) entityChanged(ctx context.Context, tx pgx.Tx, pluginID string, ent *pb.EntityDeclaration) bool {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM entities WHERE id = $1 AND plugin_id = $2 AND effective_to IS NULL
	)`, ent.GetId(), pluginID).Scan(&exists); err != nil || !exists {
		return true // not present → changed
	}
	// Compare content fields (name, namespace, type) for a lightweight diff.
	var curName, curNS, curType string
	if err := tx.QueryRow(ctx, `
		SELECT name, namespace, entity_type FROM entities
		WHERE id = $1 AND plugin_id = $2 AND effective_to IS NULL LIMIT 1
	`, ent.GetId(), pluginID).Scan(&curName, &curNS, &curType); err != nil {
		return true
	}
	return curName != ent.GetName() || curNS != ent.GetNamespace() ||
		curType != entityTypeToString(ent.GetEntityType())
}

// metricChanged returns true if the metric's content differs from the current row.
func (s *Store) metricChanged(ctx context.Context, tx pgx.Tx, pluginID string, met *pb.MetricDeclaration) bool {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM metric_definitions WHERE id = $1 AND plugin_id = $2 AND effective_to IS NULL
	)`, met.GetId(), pluginID).Scan(&exists); err != nil || !exists {
		return true
	}
	var curName, curUnit, curFreq, curEntity string
	if err := tx.QueryRow(ctx, `
		SELECT name, unit, frequency, entity_id FROM metric_definitions
		WHERE id = $1 AND plugin_id = $2 AND effective_to IS NULL LIMIT 1
	`, met.GetId(), pluginID).Scan(&curName, &curUnit, &curFreq, &curEntity); err != nil {
		return true
	}
	return curName != met.GetName() || curUnit != met.GetUnit() ||
		curFreq != met.GetFrequency() || curEntity != met.GetEntityId()
}

// GetCurrentEntities returns all currently-effective entities (effective_to IS NULL)
// for a plugin.
func (s *Store) GetCurrentEntities(ctx context.Context, pluginID string) ([]model.Entity, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, name, namespace, entity_type, plugin_id, tags, metadata,
		       version, effective_from, effective_to, supersedes, change_log
		FROM entities
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
		FROM metric_definitions
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

// MetricUIDForEntity returns the representative metric UID for an entity.
//
// Resolution path:
//  1. entity_representative_metric (entity_id, purpose='default') → metric_id → uid
//     (manual wins: operators can override the default metric for research context).
//  2. Fallback: the legacy heuristic — first row by id from metric_definitions.
//
// The fallback preserves backward compatibility for entities that have no
// explicit representative metric registered. Returns found=false when no metric
// is registered.
func (s *Store) MetricUIDForEntity(ctx context.Context, entityID string) (string, bool, error) {
	// 1. Try the explicit representative metric registry first.
	var metricID string
	err := s.db.QueryRow(ctx, `
		SELECT metric_id FROM entity_representative_metric
		WHERE entity_id = $1 AND purpose = 'default'
	`, entityID).Scan(&metricID)
	if err == nil && metricID != "" {
		var uid string
		err := s.db.QueryRow(ctx, `
			SELECT uid FROM metric_definitions
			WHERE id = $1 AND effective_to IS NULL
			LIMIT 1
		`, metricID).Scan(&uid)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, nil
		}
		if err != nil {
			return "", false, fmt.Errorf("resolve representative metric uid: %w", err)
		}
		if uid == "" {
			return "", false, nil
		}
		return uid, true, nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", false, fmt.Errorf("query representative metric: %w", err)
	}

	// 2. Legacy fallback: first metric by id (deterministic but possibly misleading).
	var uid string
	err = s.db.QueryRow(ctx, `
		SELECT uid FROM metric_definitions
		WHERE entity_id = $1 AND effective_to IS NULL
		ORDER BY id
		LIMIT 1
	`, entityID).Scan(&uid)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if uid == "" {
		return "", false, nil
	}
	return uid, true, nil
}

// MetricToEntity resolves a registered current metric to its canonical entity.
// It satisfies classification.EntityResolver for production clustering.
func (s *Store) MetricToEntity(ctx context.Context, metricID string) (string, bool, error) {
	var entityID string
	err := s.db.QueryRow(ctx, `
		SELECT entity_id FROM metric_definitions
		WHERE id = $1 AND effective_to IS NULL
		LIMIT 1
	`, metricID).Scan(&entityID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("resolve entity for metric %s: %w", metricID, err)
	}
	return entityID, entityID != "", nil
}

// HasAcceptedRelation reports whether two entities share an accepted
// structural or semantic relation. Statistical and causal relations are
// intentionally excluded from automatic event clustering, even when manually
// confirmed; they remain Research context only.
func (s *Store) HasAcceptedRelation(ctx context.Context, sourceEntity, targetEntity string) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM relations
			WHERE effective_to IS NULL
			  AND layer IN ('structural', 'semantic')
			  AND ((source_id = $1 AND target_id = $2)
			    OR (source_id = $2 AND target_id = $1))
			UNION ALL
			SELECT 1 FROM manual_relations
			WHERE active = TRUE
			  AND relation_type IN (
				'tracks', 'component_of', 'issued_by', 'belongs_to',
				'hedges', 'competes', 'signals'
			  )
			  AND ((source_id = $1 AND target_id = $2)
			    OR (source_id = $2 AND target_id = $1))
		)
	`, sourceEntity, targetEntity).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("query accepted relation %s <-> %s: %w", sourceEntity, targetEntity, err)
	}
	return exists, nil
}

// FetchObservations returns ObservationPoints for the given metric UID in
// [since, until], up to limit rows, time-ascending. Used by CandidateFinder
// as the observation data source.
func (s *Store) FetchObservations(ctx context.Context, metricUID string, since, until time.Time, limit int) ([]ObservationPoint, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := s.db.Query(ctx, `
		SELECT time, value FROM observations
		WHERE metric_uid = $1 AND time >= $2 AND time <= $3
		ORDER BY time ASC
		LIMIT $4
	`, metricUID, since, until, limit)
	if err != nil {
		return nil, fmt.Errorf("query observations: %w", err)
	}
	defer rows.Close()
	out := make([]ObservationPoint, 0, 64)
	for rows.Next() {
		var p ObservationPoint
		if err := rows.Scan(&p.Time, &p.Value); err != nil {
			return nil, fmt.Errorf("scan observation: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate observation rows: %w", err)
	}
	return out, nil
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

// upsertEntity inserts a new versioned row into entities, superseding any
// prior version (effective_to IS NULL) of the same entity id.
func upsertEntity(ctx context.Context, tx pgx.Tx, pluginID string, ent *pb.EntityDeclaration, changeLog string) error {
	// Determine the current max version for this entity id.
	var maxVersion int
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(version), 0) FROM entities WHERE id = $1
	`, ent.GetId()).Scan(&maxVersion); err != nil {
		return fmt.Errorf("query max entity version: %w", err)
	}

	newVersion := maxVersion + 1
	supersedes := maxVersion > 0

	// Close the prior effective version.
	if supersedes {
		if _, err := tx.Exec(ctx, `
			UPDATE entities SET effective_to = NOW()
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
		INSERT INTO entities (
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

// upsertMetric inserts a new versioned row into metric_definitions,
// superseding any prior version. The uid is the metric's stable system-wide
// identity: generated once at version 1 and inherited by every later version,
// so observations keyed by metric_uid stay joinable across redefinitions.
func upsertMetric(ctx context.Context, tx pgx.Tx, pluginID string, met *pb.MetricDeclaration, changeLog string) error {
	var maxVersion int
	var priorUID *string
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(version), 0),
		       (SELECT uid FROM metric_definitions WHERE id = $1 AND effective_to IS NULL LIMIT 1)
		FROM metric_definitions WHERE id = $1
	`, met.GetId()).Scan(&maxVersion, &priorUID); err != nil {
		return fmt.Errorf("query max metric version: %w", err)
	}

	newVersion := maxVersion + 1
	supersedes := maxVersion > 0

	if supersedes {
		if _, err := tx.Exec(ctx, `
			UPDATE metric_definitions SET effective_to = NOW()
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
		INSERT INTO metric_definitions (
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

// ObserveAction reports what happened to an observation in the DB.
type ObserveAction string

const (
	ActionInserted       ObserveAction = "inserted"        // new observation written
	ActionUpdatedRevised ObserveAction = "updated_revised" // revised/correction data overwrote existing
	ActionNoopDedup      ObserveAction = "noop_dedup"      // existing data is same-or-better grade; skipped
)

// gradeRank maps a quality_grade string to a numeric rank.
// Higher = more authoritative. Used by the coverage matrix in InsertObservation.
// Priority: realtime > revised > delayed > estimated > preliminary
func gradeRank(grade string) int {
	switch grade {
	case "realtime":
		return 5
	case "revised":
		return 4
	case "delayed":
		return 3
	case "estimated":
		return 2
	case "preliminary":
		return 1
	default:
		return 0
	}
}

// InsertObservation writes a scored observation and its durable detection work
// into observations/event_outbox in one transaction.
// metricUID is the registered uid from metric_definitions (resolved by the caller);
// it is what research-time joins use, so it must never be re-derived here.
//
// Coverage matrix (PRD §三.4, architecture §三):
//   - Same (metric_uid, time, source, labels_hash) → compare grade rank:
//   - Incoming rank > existing rank → UPDATE with new value/grade (the "revised" correction path)
//   - Incoming rank ≤ existing rank → NOOP (idempotent dedup)
//   - No existing row → INSERT new observation
//
// Quality values are passed directly (no dependency on metric.QualityResult avoids an import cycle).
func (s *Store) InsertObservation(ctx context.Context, snap *pb.MetricSnapshot, metricUID, grade string, confidence float64, systemScore float64, labelsHash, pluginID string) (ObserveAction, error) {
	detection := coreevent.NewDetectionRequest(
		snap.MetricId, metricUID, pluginID, snap.SourceProvider, labelsHash, grade, snap.Timestamp,
	)
	payload, err := json.Marshal(detection)
	if err != nil {
		return "", fmt.Errorf("marshal detection request: %w", err)
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin observation transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// RETURNING (xmax = 0) distinguishes a fresh INSERT (xmax=0) from an
	// ON CONFLICT UPDATE (xmax≠0) — that's what makes the revised-correction
	// path observable. ErrNoRows means the conflict WHERE clause filtered the
	// update out: incoming grade did not outrank the existing row (dedup).
	var freshInsert bool
	err = tx.QueryRow(ctx, `
		INSERT INTO observations (
			time, metric_id, metric_uid, value, labels, labels_hash,
			 source_plugin, source_plugin_version, source_provider, source_fetched_at,
			source_class,
			quality_grade, quality_confidence, system_quality_score, ingested_at
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10, $11,
			$12, $13, $14, NOW()
		)
		ON CONFLICT (metric_uid, time, source_plugin, source_provider, labels_hash) DO UPDATE SET
			value = EXCLUDED.value,
			quality_grade = EXCLUDED.quality_grade,
			quality_confidence = EXCLUDED.quality_confidence,
				source_fetched_at = EXCLUDED.source_fetched_at,
			source_class = EXCLUDED.source_class,
			system_quality_score = EXCLUDED.system_quality_score,
			ingested_at = NOW()
		WHERE
			CASE EXCLUDED.quality_grade
				WHEN 'realtime' THEN 5
				WHEN 'revised' THEN 4
				WHEN 'delayed' THEN 3
				WHEN 'estimated' THEN 2
				WHEN 'preliminary' THEN 1
				ELSE 0
			END
			>
			CASE observations.quality_grade
				WHEN 'realtime' THEN 5
				WHEN 'revised' THEN 4
				WHEN 'delayed' THEN 3
				WHEN 'estimated' THEN 2
				WHEN 'preliminary' THEN 1
				ELSE 0
			END
		RETURNING (xmax = 0)
	`, time.Unix(snap.Timestamp, 0), snap.MetricId, metricUID, snap.Value,
		labelsToBytes(snap.Labels), labelsHash,
		pluginID, snap.SourcePluginVersion, snap.SourceProvider,
		time.Unix(snap.SourceFetchedAt, 0),
		sourceClassFromProto(snap.SourceClass), grade, confidence, systemScore).Scan(&freshInsert)

	if errors.Is(err, pgx.ErrNoRows) {
		// ON CONFLICT match but WHERE excluded rank ≤ existing rank → dedup skip.
		return ActionNoopDedup, nil
	}
	if err != nil {
		return "", fmt.Errorf("insert observation: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO event_outbox (event_type, payload, dedup_key)
		VALUES ($1, $2, $3)
		ON CONFLICT (event_type, dedup_key) WHERE dedup_key IS NOT NULL DO NOTHING
	`, coreevent.TypeDetectionRequested, payload, detection.DetectionKey); err != nil {
		return "", fmt.Errorf("insert detection outbox event: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit observation transaction: %w", err)
	}
	if freshInsert {
		return ActionInserted, nil
	}
	return ActionUpdatedRevised, nil
}

func sourceClassFromProto(class pb.SourceClass) model.SourceClass {
	switch class {
	case pb.SourceClass_SOURCE_CLASS_REAL:
		return model.SourceClassReal
	case pb.SourceClass_SOURCE_CLASS_MOCK:
		return model.SourceClassMock
	case pb.SourceClass_SOURCE_CLASS_TEST:
		return model.SourceClassTest
	default:
		return model.SourceClassUnknown
	}
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
