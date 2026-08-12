// Minimal rules management API: list, enable/disable (PATCH), restore (POST).
// Database access is inline SQL against *pgxpool.Pool — per P1 #7 we only need
// the enable/disable/restore/version surface, so a thin handler file is enough.
package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"

	"capital_observatory/pkg/model"
)

type rulesStore struct {
	db *pgxpool.Pool
}

func newRulesStore(db *pgxpool.Pool) *rulesStore {
	return &rulesStore{db: db}
}

// GET /api/rules/  → list all rule rows (all versions, all states).
func (s *rulesStore) listHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET required"})
		return
	}

	rows, err := s.db.Query(r.Context(), `
		SELECT id, name, metric_id, detector_name, severity, config, description,
		       enabled, source, is_override, version, effective_from, effective_to
		FROM rules
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
			effTo       *time.Time
		)
		if err := rows.Scan(
			&rule.ID, &rule.Name, &rule.MetricID, &rule.DetectorName, &rule.Severity,
			&configBytes, &rule.Description, &rule.Enabled, &rule.Source,
			&rule.IsOverride, &rule.Version, &rule.EffectiveFrom, &effTo,
		); err != nil {
			log.Warn().Err(err).Msg("scan rule row failed")
			continue
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

	tag, err := s.db.Exec(r.Context(), `
		UPDATE rules SET enabled = $1, updated_at = NOW()
		WHERE id = $2
	`, *req.Enabled, id)
	if err != nil {
		log.Error().Err(err).Int("rule_id", id).Msg("update rule enabled failed")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	if tag.RowsAffected() == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "rule not found"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":      id,
		"enabled": *req.Enabled,
		"status":  "updated",
	})
}

// POST /api/rules/{id}/restore/{version}  → soft-disable the current row and
//   insert a copy of the requested historical version as the new current row.
// Minimal restore: bumps a new row at version N+1 targeting the historical
// content (id, name, metric_id, detector_name, severity, config, source).
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

	// Look up the historical source row (any version with matching id).
	var (
		name, metricID, detectorName, severity, source, description string
		configBytes                                                 []byte
		isOverride                                                  bool
	)
	err = s.db.QueryRow(r.Context(), `
		SELECT name, metric_id, detector_name, severity, config, description, source, is_override
		FROM rules
		WHERE id = $1 AND version = $2
		LIMIT 1
	`, id, ver).Scan(&name, &metricID, &detectorName, &severity, &configBytes, &description, &source, &isOverride)
	if err != nil {
		log.Error().Err(err).Int("rule_id", id).Int("version", ver).Msg("lookup historical rule failed")
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "historical rule version not found"})
		return
	}

	// Next version number for this rule id.
	var maxVer int
	if err := s.db.QueryRow(r.Context(), `
		SELECT COALESCE(MAX(version), 0) FROM rules WHERE id = $1
	`, id).Scan(&maxVer); err != nil {
		log.Error().Err(err).Int("rule_id", id).Msg("query max rule version failed")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}

	// In a real implementation this would run in a transaction. The minimal MVP
	// path soft-disables any currently-effective row and inserts the restored
	// version (effective_to = NULL so it becomes current).
	if _, err := s.db.Exec(r.Context(), `
		UPDATE rules SET effective_to = NOW(), updated_at = NOW()
		WHERE id = $1 AND effective_to IS NULL
	`, id); err != nil {
		log.Error().Err(err).Int("rule_id", id).Msg("close current rule version failed")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}

	configArg := configBytes
	if len(configBytes) == 0 {
		configArg = []byte("{}")
	}

	if _, err := s.db.Exec(r.Context(), `
		INSERT INTO rules (id, name, metric_id, detector_name, severity, config,
		                   description, enabled, source, is_override, version,
		                   effective_from, effective_to)
		VALUES ($1, $2, $3, $4, $5, $6, $7, TRUE, $8, $9, $10, NOW(), NULL)
	`, id, name, metricID, detectorName, severity, configArg, description,
		source, isOverride, maxVer+1); err != nil {
		log.Error().Err(err).Int("rule_id", id).Msg("insert restored rule version failed")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":      id,
		"version": maxVer + 1,
		"status":  "restored",
	})
}
