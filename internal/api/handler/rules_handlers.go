// Minimal rules management API: list, enable/disable (PATCH), restore (POST).
// Database access is inline SQL against *pgxpool.Pool — per P1 #7 we only need
// the enable/disable/restore/version surface, so a thin handler file is enough.
package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"

	"sonde/pkg/model"
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
		       COALESCE(display_name, ''),
		       enabled, source, is_override, version, effective_from, effective_to,
		       COALESCE(mode, 'live')
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
			&configBytes, &description, &rule.DisplayName, &rule.Enabled, &rule.Source,
			&rule.IsOverride, &rule.Version, &rule.EffectiveFrom, &effTo, &rule.Mode,
		); err != nil {
			log.Error().Err(err).Msg("scan rule row failed")
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		if description != nil {
			rule.Description = *description
		}
		rule.Mode = model.NormalizeRuleMode(rule.Mode)
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

// PATCH /api/rules/{id}  body: {"enabled": bool, "config": object, "severity": string, "description": string}
//
// Atomically closes the current version, inserts a successor, and appends a
// rule_audit_log entry for each modified field. Rule content is never updated in place.
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
		Enabled     *bool           `json:"enabled"`
		Config      json.RawMessage `json:"config"`
		Severity    *string         `json:"severity"`
		Description *string         `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if req.Enabled == nil && req.Config == nil && req.Severity == nil && req.Description == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "at least one of enabled, config, severity, description required",
		})
		return
	}

	if req.Severity != nil {
		sev := strings.ToLower(strings.TrimSpace(*req.Severity))
		if sev != "info" && sev != "warning" && sev != "critical" {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "severity must be one of: info, warning, critical",
			})
			return
		}
		req.Severity = &sev
	}

	if req.Config != nil {
		var cfgObj map[string]any
		if err := json.Unmarshal(req.Config, &cfgObj); err != nil || len(cfgObj) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "config must be a valid non-empty JSON object",
			})
			return
		}
	}

	tx, err := s.beginTx(r.Context())
	if err != nil {
		log.Error().Err(err).Int("rule_id", id).Msg("begin rule patch transaction failed")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer tx.Rollback(r.Context())

	var (
		name, metricID, detectorName, curSeverity, source, curDisplayName, curMode string
		curConfigBytes                                                             []byte
		curDescription                                                             *string
		curEnabled, isOverride                                                     bool
		curVersion                                                                 int
	)
	if err := tx.QueryRow(r.Context(), `
		SELECT name, metric_id, detector_name, severity, config, description,
		       COALESCE(display_name, ''),
		       enabled, source, is_override, version, COALESCE(mode, 'live')
		FROM rules WHERE id = $1 AND effective_to IS NULL
		FOR UPDATE
	`, id).Scan(&name, &metricID, &detectorName, &curSeverity, &curConfigBytes,
		&curDescription, &curDisplayName, &curEnabled, &source, &isOverride, &curVersion, &curMode); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "rule not found"})
			return
		}
		log.Error().Err(err).Int("rule_id", id).Msg("lookup rule failed")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}

	type changeEntry struct {
		field    string
		oldValue any
		newValue any
	}
	var changes []changeEntry

	newEnabled := curEnabled
	if req.Enabled != nil {
		newEnabled = *req.Enabled
		if curEnabled != *req.Enabled {
			changes = append(changes, changeEntry{
				field:    "enabled",
				oldValue: curEnabled,
				newValue: *req.Enabled,
			})
		}
	}

	newSeverity := curSeverity
	if req.Severity != nil {
		newSeverity = *req.Severity
		if curSeverity != *req.Severity {
			changes = append(changes, changeEntry{
				field:    "severity",
				oldValue: curSeverity,
				newValue: *req.Severity,
			})
		}
	}

	newDescription := curDescription
	if req.Description != nil {
		newDescription = req.Description
		oldDescStr := ""
		if curDescription != nil {
			oldDescStr = *curDescription
		}
		if oldDescStr != *req.Description {
			changes = append(changes, changeEntry{
				field:    "description",
				oldValue: oldDescStr,
				newValue: *req.Description,
			})
		}
	}

	newConfigBytes := curConfigBytes
	if req.Config != nil {
		var oldMap, newMap map[string]any
		_ = json.Unmarshal(curConfigBytes, &oldMap)
		_ = json.Unmarshal(req.Config, &newMap)
		oldNorm, _ := json.Marshal(oldMap)
		newNorm, _ := json.Marshal(newMap)
		if string(oldNorm) != string(newNorm) {
			newConfigBytes = req.Config
			changes = append(changes, changeEntry{
				field:    "config",
				oldValue: json.RawMessage(curConfigBytes),
				newValue: req.Config,
			})
		}
	}

	if len(changes) == 0 {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"id": id, "version": curVersion, "enabled": curEnabled, "severity": curSeverity, "status": "unchanged",
		})
		return
	}

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
		                   description, display_name, enabled, source, is_override, version,
		                   effective_from, effective_to, mode)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NOW(), NULL, $12)
		RETURNING id
	`, name, metricID, detectorName, newSeverity, newConfigBytes, newDescription,
		curDisplayName, newEnabled, source, isOverride, curVersion+1, curMode).Scan(&newID); err != nil {
		log.Error().Err(err).Int("rule_id", id).Msg("insert rule enabled version failed")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}

	actor := bearerActorIdentifier(r)
	for _, ch := range changes {
		oldJSON, _ := json.Marshal(ch.oldValue)
		newJSON, _ := json.Marshal(ch.newValue)
		if _, err := tx.Exec(r.Context(), fmt.Sprintf(`
			INSERT INTO rule_audit_log (rule_id, scope, field, old_value, new_value, actor)
			VALUES ($1, 'global', '%s', $2::jsonb, $3::jsonb, $4)
		`, ch.field), newID, oldJSON, newJSON, actor); err != nil {
			log.Error().Err(err).Int("rule_id", id).Str("field", ch.field).Msg("insert rule audit log failed")
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
	}

	if err := tx.Commit(r.Context()); err != nil {
		log.Error().Err(err).Int("rule_id", id).Msg("commit rule patch failed")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":       newID,
		"version":  curVersion + 1,
		"enabled":  newEnabled,
		"severity": newSeverity,
		"status":   "updated",
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
		WITH logical_rules AS (
			SELECT r.id, r.name, r.metric_id, r.detector_name, r.version, r.severity,
			       r.config, r.description, r.enabled, r.effective_from, r.effective_to, r.created_at
			FROM rules AS anchor
			JOIN rules AS r
			  ON r.name = anchor.name
			 AND r.metric_id = anchor.metric_id
			 AND r.detector_name = anchor.detector_name
			WHERE anchor.id = $1
		)
		SELECT
			COALESCE(audit.id, 0) AS id,
			lr.id AS rule_id,
			COALESCE(audit.scope, 'global') AS scope,
			COALESCE(audit.field, CASE WHEN lr.version = 1 THEN 'initial_version' ELSE 'version_created' END) AS field,
			audit.old_value,
			COALESCE(audit.new_value, lr.config) AS new_value,
			COALESCE(audit.actor, 'system') AS actor,
			audit.reason,
			COALESCE(audit.created_at, lr.effective_from) AS created_at,
			lr.version,
			lr.severity,
			lr.config,
			lr.enabled,
			lr.effective_from,
			lr.effective_to
		FROM logical_rules AS lr
		LEFT JOIN rule_audit_log AS audit ON audit.rule_id = lr.id
		ORDER BY lr.version DESC, COALESCE(audit.created_at, lr.effective_from) DESC
	`, id)
	if err != nil {
		log.Error().Err(err).Int("rule_id", id).Msg("query rule audit history failed")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer rows.Close()

	type auditRow struct {
		ID            int64           `json:"id"`
		RuleID        int             `json:"rule_id"`
		Scope         string          `json:"scope"`
		Field         string          `json:"field"`
		OldValue      json.RawMessage `json:"old_value,omitempty"`
		NewValue      json.RawMessage `json:"new_value,omitempty"`
		Actor         string          `json:"actor"`
		Reason        *string         `json:"reason,omitempty"`
		CreatedAt     time.Time       `json:"created_at"`
		Version       int             `json:"version"`
		Severity      string          `json:"severity,omitempty"`
		Config        json.RawMessage `json:"config,omitempty"`
		Enabled       bool            `json:"enabled"`
		EffectiveFrom time.Time       `json:"effective_from"`
		EffectiveTo   *time.Time      `json:"effective_to,omitempty"`
	}

	var entries []auditRow
	for rows.Next() {
		var (
			e           auditRow
			reason      *string
			effectiveTo *time.Time
		)
		if err := rows.Scan(
			&e.ID, &e.RuleID, &e.Scope, &e.Field, &e.OldValue, &e.NewValue,
			&e.Actor, &reason, &e.CreatedAt, &e.Version,
			&e.Severity, &e.Config, &e.Enabled, &e.EffectiveFrom, &effectiveTo,
		); err != nil {
			log.Error().Err(err).Int("rule_id", id).Msg("scan rule audit log failed")
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		e.Reason = reason
		e.EffectiveTo = effectiveTo
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
		name, metricID, detectorName, severity, source, description, displayName, mode string
		configBytes                                                                    []byte
		isOverride, enabled                                                            bool
	)
	err = tx.QueryRow(r.Context(), `
		SELECT historical.name, historical.metric_id, historical.detector_name,
		       historical.severity, historical.config,
		       COALESCE(historical.description, ''), COALESCE(historical.display_name, ''),
		       historical.source, historical.is_override, historical.enabled,
		       COALESCE(historical.mode, 'live')
		FROM rules AS anchor
		JOIN rules AS historical
		  ON historical.name = anchor.name
		 AND historical.metric_id = anchor.metric_id
		 AND historical.detector_name = anchor.detector_name
		WHERE anchor.id = $1 AND historical.version = $2
		FOR UPDATE OF anchor, historical
	`, id, ver).Scan(&name, &metricID, &detectorName, &severity, &configBytes,
		&description, &displayName, &source, &isOverride, &enabled, &mode)
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
		                   description, display_name, enabled, source, is_override, version,
		                   effective_from, effective_to, mode)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NOW(), NULL, $12)
		RETURNING id
	`, name, metricID, detectorName, severity, configArg, description, displayName, enabled,
		source, isOverride, maxVer+1, mode).Scan(&restoredID); err != nil {
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
