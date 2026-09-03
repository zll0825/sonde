// Minimal rules management API: list, enable/disable (PATCH), restore (POST).
// Database access is inline SQL against *pgxpool.Pool — per P1 #7 we only need
// the enable/disable/restore/version surface, so a thin handler file is enough.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"

	"capital_observatory/pkg/model"
)

type rulesStore struct {
	db      rulesDB
	beginTx func(context.Context) (rulesTx, error)
}

type rulesDB interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

type rulesTx interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

func newRulesStore(db *pgxpool.Pool) *rulesStore {
	return &rulesStore{
		db: db,
		beginTx: func(ctx context.Context) (rulesTx, error) {
			return db.Begin(ctx)
		},
	}
}

// GET /api/rules/ returns one current row per logical rule. Historical rows are
// available through the audit/history endpoint and are never presented as
// mutable controls.
func (s *rulesStore) listHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET required"})
		return
	}

	rows, err := s.db.Query(r.Context(), `
		SELECT id, name, metric_id, detector_name, severity, config, description,
		       enabled, source, is_override, version, effective_from, effective_to
		FROM rules
		WHERE effective_to IS NULL
		ORDER BY id
	`)
	if err != nil {
		log.Error().Err(err).Msg("query rules failed")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer rows.Close()

	rules := []model.Rule{}
	for rows.Next() {
		var (
			rule        model.Rule
			configBytes []byte
			description *string
			effTo       *time.Time
		)
		if err := rows.Scan(
			&rule.ID, &rule.Name, &rule.MetricID, &rule.DetectorName, &rule.Severity,
			&configBytes, &description, &rule.Enabled, &rule.Source,
			&rule.IsOverride, &rule.Version, &rule.EffectiveFrom, &effTo,
		); err != nil {
			log.Error().Err(err).Msg("scan rule row failed")
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		if description != nil {
			rule.Description = *description
		}
		rule.EffectiveTo = effTo
		if len(configBytes) > 0 {
			rule.Config = configBytes
		}
		rules = append(rules, rule)
	}
	if err := rows.Err(); err != nil {
		log.Error().Err(err).Msg("iterate rule rows failed")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	if rules == nil {
		rules = []model.Rule{}
	}
	writeJSON(w, http.StatusOK, rules)
}

// PATCH /api/rules/{id}  body: {"enabled": true|false}  → toggle a rule on/off.
//
// Atomically closes the current version, inserts a successor, and appends a
// rule_audit_log entry. Rule content is never updated in place.
func (s *rulesStore) patchHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "PATCH required"})
		return
	}

	idStr := r.PathValue("id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "rule id must be a positive integer"})
		return
	}

	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if req.Enabled == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "enabled (bool) required"})
		return
	}

	tx, err := s.beginTx(r.Context())
	if err != nil {
		log.Error().Err(err).Int("rule_id", id).Msg("begin rule patch transaction failed")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer tx.Rollback(r.Context())

	var (
		name, metricID, detectorName, severity, source string
		configBytes                                    []byte
		description                                    *string
		curEnabled, isOverride                         bool
		curVersion                                     int
	)
	if err := tx.QueryRow(r.Context(), `
		SELECT name, metric_id, detector_name, severity, config, description,
		       enabled, source, is_override, version
		FROM rules WHERE id = $1 AND effective_to IS NULL
		FOR UPDATE
	`, id).Scan(&name, &metricID, &detectorName, &severity, &configBytes,
		&description, &curEnabled, &source, &isOverride, &curVersion); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "rule not found"})
			return
		}
		log.Error().Err(err).Int("rule_id", id).Msg("lookup rule enabled failed")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	if curEnabled == *req.Enabled {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"id": id, "version": curVersion, "enabled": curEnabled, "status": "unchanged",
		})
		return
	}

	oldJSON, _ := json.Marshal(curEnabled)
	newJSON, _ := json.Marshal(*req.Enabled)
	actor := bearerActorIdentifier(r)

	tag, err := tx.Exec(r.Context(), `
		UPDATE rules SET effective_to = NOW(), updated_at = NOW()
		WHERE id = $1 AND effective_to IS NULL
	`, id)
	if err != nil {
		log.Error().Err(err).Int("rule_id", id).Msg("update rule enabled failed")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	if tag.RowsAffected() == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "rule not found (historical or missing)"})
		return
	}

	var newID int
	if err := tx.QueryRow(r.Context(), `
		INSERT INTO rules (name, metric_id, detector_name, severity, config,
		                   description, enabled, source, is_override, version,
		                   effective_from, effective_to)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NOW(), NULL)
		RETURNING id
	`, name, metricID, detectorName, severity, configBytes, description,
		*req.Enabled, source, isOverride, curVersion+1).Scan(&newID); err != nil {
		log.Error().Err(err).Int("rule_id", id).Msg("insert rule enabled version failed")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}

	if _, err := tx.Exec(r.Context(), `
		INSERT INTO rule_audit_log (rule_id, scope, field, old_value, new_value, actor)
		VALUES ($1, 'global', 'enabled', $2::jsonb, $3::jsonb, $4)
	`, newID, oldJSON, newJSON, actor); err != nil {
		log.Error().Err(err).Int("rule_id", id).Msg("insert rule audit log failed")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		log.Error().Err(err).Int("rule_id", id).Msg("commit rule patch failed")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":      newID,
		"version": curVersion + 1,
		"enabled": *req.Enabled,
		"status":  "updated",
	})
}

// historyHandler returns the immutable rule_audit_log rows for a rule, newest
// first. Path: GET /api/rules/{id}/history
func (s *rulesStore) historyHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET required"})
		return
	}

	idStr := r.PathValue("id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "rule id must be a positive integer"})
		return
	}

	rows, err := s.db.Query(r.Context(), `
		SELECT audit.id, audit.rule_id, audit.scope, audit.field, audit.old_value,
		       audit.new_value, audit.actor, audit.reason, audit.created_at,
		       COALESCE(src.version, 0)
		FROM rule_audit_log AS audit
		LEFT JOIN rules AS src ON src.id = audit.rule_id
		WHERE audit.rule_id IN (
			SELECT logical.id
			FROM rules AS anchor
			JOIN rules AS logical
			  ON logical.name = anchor.name
			 AND logical.metric_id = anchor.metric_id
			 AND logical.detector_name = anchor.detector_name
			WHERE anchor.id = $1
		)
		ORDER BY audit.created_at DESC
	`, id)
	if err != nil {
		log.Error().Err(err).Int("rule_id", id).Msg("query rule audit history failed")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer rows.Close()

	type auditRow struct {
		ID        int64           `json:"id"`
		RuleID    int             `json:"rule_id"`
		Scope     string          `json:"scope"`
		Field     string          `json:"field"`
		OldValue  json.RawMessage `json:"old_value"`
		NewValue  json.RawMessage `json:"new_value"`
		Actor     string          `json:"actor"`
		Reason    *string         `json:"reason,omitempty"`
		CreatedAt time.Time       `json:"created_at"`
		Version   int             `json:"version"`
	}

	var entries []auditRow
	for rows.Next() {
		var e auditRow
		var reason *string
		if err := rows.Scan(
			&e.ID, &e.RuleID, &e.Scope, &e.Field, &e.OldValue, &e.NewValue,
			&e.Actor, &reason, &e.CreatedAt, &e.Version,
		); err != nil {
			log.Error().Err(err).Int("rule_id", id).Msg("scan rule audit log failed")
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		e.Reason = reason
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		log.Error().Err(err).Int("rule_id", id).Msg("iterate rule audit log failed")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	if entries == nil {
		entries = []auditRow{}
	}
	writeJSON(w, http.StatusOK, entries)
}

// POST /api/rules/{id}/restore/{version}  → soft-disable the current row and
//
//	insert a copy of the requested historical version as the new current row.
//
// Restore creates a new physical row at version N+1 using the selected
// historical version's content and state.
func (s *rulesStore) restoreHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST required"})
		return
	}

	idStr := r.PathValue("id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "rule id must be a positive integer"})
		return
	}

	versionStr := r.PathValue("version")
	ver, err := strconv.Atoi(versionStr)
	if err != nil || ver <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "version must be a positive integer"})
		return
	}

	tx, err := s.beginTx(r.Context())
	if err != nil {
		log.Error().Err(err).Msg("begin rule restore transaction failed")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer tx.Rollback(r.Context())

	// id identifies one physical row. Resolve its logical rule identity, then
	// find the requested version within that identity. This prevents a version
	// from another rule being restored accidentally.
	var (
		name, metricID, detectorName, severity, source, description string
		configBytes                                                 []byte
		isOverride, enabled                                         bool
	)
	err = tx.QueryRow(r.Context(), `
		SELECT historical.name, historical.metric_id, historical.detector_name,
		       historical.severity, historical.config,
		       COALESCE(historical.description, ''), historical.source,
		       historical.is_override, historical.enabled
		FROM rules AS anchor
		JOIN rules AS historical
		  ON historical.name = anchor.name
		 AND historical.metric_id = anchor.metric_id
		 AND historical.detector_name = anchor.detector_name
		WHERE anchor.id = $1 AND historical.version = $2
		FOR UPDATE OF anchor, historical
	`, id, ver).Scan(&name, &metricID, &detectorName, &severity, &configBytes,
		&description, &source, &isOverride, &enabled)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "historical rule version not found"})
			return
		}
		log.Error().Err(err).Int("rule_id", id).Int("version", ver).Msg("lookup historical rule failed")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}

	// Lock the latest logical version so concurrent restores serialize before
	// computing the next version number.
	var maxVer int
	if err := tx.QueryRow(r.Context(), `
		SELECT version FROM rules
		WHERE name = $1 AND metric_id = $2 AND detector_name = $3
		ORDER BY version DESC LIMIT 1
		FOR UPDATE
	`, name, metricID, detectorName).Scan(&maxVer); err != nil {
		log.Error().Err(err).Int("rule_id", id).Msg("query max rule version failed")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}

	if _, err := tx.Exec(r.Context(), `
		UPDATE rules SET effective_to = NOW(), updated_at = NOW()
		WHERE name = $1 AND metric_id = $2 AND detector_name = $3
		  AND effective_to IS NULL
	`, name, metricID, detectorName); err != nil {
		log.Error().Err(err).Int("rule_id", id).Msg("close current rule version failed")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}

	configArg := configBytes
	if len(configBytes) == 0 {
		configArg = []byte("{}")
	}

	var restoredID int
	if err := tx.QueryRow(r.Context(), `
		INSERT INTO rules (name, metric_id, detector_name, severity, config,
		                   description, enabled, source, is_override, version,
		                   effective_from, effective_to)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NOW(), NULL)
		RETURNING id
	`, name, metricID, detectorName, severity, configArg, description, enabled,
		source, isOverride, maxVer+1).Scan(&restoredID); err != nil {
		log.Error().Err(err).Int("rule_id", id).Msg("insert restored rule version failed")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	oldJSON, _ := json.Marshal(maxVer)
	newJSON, _ := json.Marshal(ver)
	if _, err := tx.Exec(r.Context(), `
		INSERT INTO rule_audit_log (rule_id, scope, field, old_value, new_value, actor)
		VALUES ($1, 'global', 'restore_version', $2::jsonb, $3::jsonb, $4)
	`, restoredID, oldJSON, newJSON, bearerActorIdentifier(r)); err != nil {
		log.Error().Err(err).Int("rule_id", id).Msg("insert rule restore audit failed")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		log.Error().Err(err).Int("rule_id", id).Msg("commit restored rule version failed")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":      restoredID,
		"version": maxVer + 1,
		"status":  "restored",
	})
}
