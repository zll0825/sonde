package handler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"

	"capital_observatory/pkg/model"
)

// controlDB is the query/exec surface sync and backfill need. Production
// passes *pgxpool.Pool; tests pass a fake.
type controlDB interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// statusHandler returns the aggregated pulse-dashboard state (design D1):
// plugin health, today's alert-budget consumption, newest data timestamp, and
// per-metric freshness + sparkline series — everything the frontend needs in
// one fetch. Read-only GET, so authMiddleware lets it through; the rate
// limiter covers it like the other read routes. Freshness is decided here
// server-side (freshness()) — the frontend only maps the verdict to a color.
//
// db is declared as statusQuerier (a Query/QueryRow subset of *pgxpool.Pool)
// so the handler is unit-testable with the handwritten fake in status_test.go;
// production passes the real pool unchanged.
func statusHandler(db statusQuerier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status, err := loadStatus(r.Context(), db, time.Now())
		if err != nil {
			log.Error().Err(err).Msg("load status failed")
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		writeJSON(w, http.StatusOK, status)
	}
}

// alertsHandler returns the list of alerts, optionally filtered by status.
// GET /api/alerts?status=active|acknowledged|resolved|silenced|all (defaults to active)
func alertsHandler(db statusQuerier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		statusParam := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")))
		var (
			rows pgx.Rows
			err  error
		)

		if statusParam == "all" {
			rows, err = db.Query(r.Context(), `
				SELECT id, title, summary, severity, metric_id, triggered_at, status,
				       source_provider, source_class, dedup_count, last_deduplicated_at,
				       resolved_at
				FROM alerts
				ORDER BY triggered_at DESC LIMIT 100
			`)
		} else {
			targetStatus := "active"
			switch statusParam {
			case "acknowledged", "resolved", "silenced":
				targetStatus = statusParam
			default:
				targetStatus = "active"
			}
			rows, err = db.Query(r.Context(), `
				SELECT id, title, summary, severity, metric_id, triggered_at, status,
				       source_provider, source_class, dedup_count, last_deduplicated_at,
				       resolved_at
				FROM alerts WHERE status = $1
				ORDER BY triggered_at DESC LIMIT 100
			`, targetStatus)
		}
		if err != nil {
			log.Error().Err(err).Msg("query alerts failed")
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		defer rows.Close()

		// Initialize non-nil so an empty result serializes as [], not null.
		alerts := []map[string]interface{}{}
		for rows.Next() {
			var (
				id, title, summary, severity, metricID, status string
				sourceProvider, sourceClass                    string
				triggeredAt                                    time.Time
				dedupCount                                     int
				lastDeduplicatedAt                             *time.Time
				resolvedAt                                     *time.Time
			)
			if err := rows.Scan(&id, &title, &summary, &severity, &metricID, &triggeredAt, &status,
				&sourceProvider, &sourceClass, &dedupCount, &lastDeduplicatedAt, &resolvedAt); err != nil {
				log.Error().Err(err).Msg("scan alert row failed")
				continue
			}
			alerts = append(alerts, map[string]interface{}{
				"id":                   id,
				"title":                title,
				"summary":              summary,
				"severity":             severity,
				"metric_id":            metricID,
				"triggered_at":         triggeredAt,
				"status":               status,
				"source_provider":      sourceProvider,
				"source_class":         sourceClass,
				"dedup_count":          dedupCount,
				"last_deduplicated_at": lastDeduplicatedAt,
				"resolved_at":          resolvedAt,
			})
		}
		if err := rows.Err(); err != nil {
			log.Error().Err(err).Msg("iterate alert rows failed")
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		writeJSON(w, http.StatusOK, alerts)
	}
}

// patchAlertHandler updates the lifecycle status of an alert.
// Route: PATCH /api/alerts/{id}
// Body: {"status": "active"|"acknowledged"|"resolved"|"silenced"}
func patchAlertHandler(db statusQuerier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "PATCH required"})
			return
		}

		id := r.PathValue("id")
		if id == "" {
			id = strings.TrimPrefix(r.URL.Path, "/api/alerts/")
		}
		id = strings.TrimSpace(id)
		if id == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "alert id required"})
			return
		}

		var req struct {
			Status string `json:"status"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
			return
		}

		req.Status = strings.ToLower(strings.TrimSpace(req.Status))
		switch req.Status {
		case "active", "acknowledged", "resolved", "silenced":
			// valid
		default:
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "status must be one of: active, acknowledged, resolved, silenced",
			})
			return
		}

		var (
			updatedID, status string
			updatedAt         time.Time
			resolvedAt        *time.Time
		)

		err := db.QueryRow(r.Context(), `
			UPDATE alerts
			SET status = $1,
			    resolved_at = CASE WHEN $1 = 'resolved' THEN NOW() ELSE NULL END,
			    updated_at = NOW()
			WHERE id = $2
			RETURNING id, status, updated_at, resolved_at
		`, req.Status, id).Scan(&updatedID, &status, &updatedAt, &resolvedAt)

		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "alert not found"})
				return
			}
			log.Error().Err(err).Str("alert_id", id).Msg("update alert status failed")
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"id":          updatedID,
			"status":      status,
			"updated_at":  updatedAt,
			"resolved_at": resolvedAt,
		})
	}
}

// researchHandler returns the research context for a given alert ID.
// Path: /api/research/{alert_id}
func researchHandler(db *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		alertID := r.URL.Path[len("/api/research/"):]
		if alertID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "alert_id required"})
			return
		}

		var ctxData []byte
		var frozenAt time.Time
		err := db.QueryRow(r.Context(), `
			SELECT context, ontology_frozen_at FROM research_snapshots WHERE alert_id = $1
		`, alertID).Scan(&ctxData, &frozenAt)
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "research context not found"})
			return
		}
		if err != nil {
			log.Error().Err(err).Str("alert_id", alertID).Msg("query research snapshot failed")
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}

		var result map[string]interface{}
		if err := json.Unmarshal(ctxData, &result); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "corrupt snapshot"})
			return
		}
		result["ontology_frozen_at"] = frozenAt
		writeJSON(w, http.StatusOK, result)
	}
}

// validFeedbackVerdicts is the set of verdicts the research_feedbacks table
// accepts (matches the CHECK constraint in migration 009).
var validFeedbackVerdicts = map[string]bool{
	"worth_researching": true,
	"irrelevant":        true,
	"duplicate":         true,
}

// researchFeedbackHandler serves
//   - POST /api/research/{id}/feedback  (requires Bearer token) and
//   - GET  /api/research/feedback?alert_id=xxx
//
// POST records a verdict {alert_id, rationale?, verdict} against the snapshot
// whose alert_id equals the path {id}. The body may optionally repeat it, but
// a mismatch is rejected. verdict must be one of
// worth_researching | irrelevant | duplicate. On success 201; a bad verdict
// yields 400. The actor (user_id) is derived from the Bearer token when present,
// otherwise 'anonymous'.
type researchFeedbackStore interface {
	SaveFeedback(ctx context.Context, alertID, clusterID, verdict, rationale, userID string) error
	ListFeedbacks(ctx context.Context, alertID string) ([]model.ResearchFeedback, error)
}

func researchFeedbackHandler(rs researchFeedbackStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			handleSaveFeedback(w, r, rs)
		case http.MethodGet:
			handleListFeedbacks(w, r, rs)
		default:
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET or POST required"})
		}
	}
}

// handleSaveFeedback handles POST /api/research/{id}/feedback.
func handleSaveFeedback(w http.ResponseWriter, r *http.Request, rs researchFeedbackStore) {
	alertID := r.PathValue("id")
	if alertID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "alert_id (path) required"})
		return
	}

	var req struct {
		AlertID   string `json:"alert_id"`
		ClusterID string `json:"cluster_id,omitempty"`
		Verdict   string `json:"verdict"`
		Rationale string `json:"rationale"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}

	if req.AlertID != "" && req.AlertID != alertID {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "body alert_id must match path"})
		return
	}
	if !validFeedbackVerdicts[req.Verdict] {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "verdict must be one of: worth_researching, irrelevant, duplicate",
		})
		return
	}

	userID := bearerActorIdentifier(r)
	if err := rs.SaveFeedback(r.Context(), alertID, req.ClusterID, req.Verdict, req.Rationale, userID); err != nil {
		log.Error().Err(err).Str("alert_id", alertID).Msg("save research feedback failed")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"alert_id": alertID,
		"verdict":  req.Verdict,
		"status":   "recorded",
	})
}

// handleListFeedbacks returns the feedback history for an alert.
// The alert_id is taken from the query string ?alert_id=.
func handleListFeedbacks(w http.ResponseWriter, r *http.Request, rs researchFeedbackStore) {
	alertID := r.URL.Query().Get("alert_id")
	if alertID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "alert_id query param required"})
		return
	}
	feedbacks, err := rs.ListFeedbacks(r.Context(), alertID)
	if err != nil {
		log.Error().Err(err).Str("alert_id", alertID).Msg("list research feedbacks failed")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	writeJSON(w, http.StatusOK, feedbacks)
}

// bearerActorIdentifier records authentication state without persisting any
// credential material. The static API token has no user claims to identify a
// person more specifically.
func bearerActorIdentifier(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if strings.HasPrefix(auth, prefix) && strings.TrimPrefix(auth, prefix) != "" {
		return "authenticated_api_client"
	}
	return "anonymous"
}

// syncHandler triggers a manual sync for a given plugin+metrics.
func syncHandler(db controlDB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST required"})
			return
		}

		var req struct {
			PluginID  string   `json:"plugin_id"`
			MetricIDs []string `json:"metric_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
			return
		}
		if req.PluginID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "plugin_id required"})
			return
		}

		// M5: insert into command_log; core will pick it up via outbox/polling.
		// No ON CONFLICT clause: command IDs are random, so a conflict would be a
		// real bug — swallowing it would ack a command that was never queued.
		_, err := db.Exec(r.Context(), `
			INSERT INTO command_log (command_id, command_type, target_plugin, requested_by, status, metric_ids)
			VALUES ($1, 'sync', $2, 'api', 'pending', $3)
		`, generateCommandID(), req.PluginID, metricIDsToBytes(req.MetricIDs))
		if err != nil {
			log.Error().Err(err).Msg("insert sync command failed")
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}

		writeJSON(w, http.StatusAccepted, map[string]string{
			"status":  "pending",
			"message": "sync command queued",
		})
	}
}

// backfillHandler triggers a manual backfill for a given plugin+window.
func backfillHandler(db controlDB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST required"})
			return
		}

		var req struct {
			PluginID    string   `json:"plugin_id"`
			MetricIDs   []string `json:"metric_ids"`
			WindowStart int64    `json:"window_start"`
			WindowEnd   int64    `json:"window_end"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
			return
		}
		if req.PluginID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "plugin_id required"})
			return
		}
		if req.WindowStart <= 0 || req.WindowEnd <= 0 || req.WindowStart >= req.WindowEnd {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "window_start and window_end must be positive unix seconds with window_start < window_end"})
			return
		}

		found, allowed, err := pluginDeclaresWindowedBackfill(r.Context(), db, req.PluginID)
		if err != nil {
			log.Error().Err(err).Str("plugin_id", req.PluginID).Msg("lookup backfill capability failed")
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		if !found {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "plugin not registered"})
			return
		}
		if !allowed {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "plugin does not declare windowedBackfill"})
			return
		}

		_, err = db.Exec(r.Context(), `
			INSERT INTO command_log
			(command_id, command_type, target_plugin, requested_by, status,
			 metric_ids, window_start, window_end)
			VALUES ($1, 'backfill', $2, 'api', 'pending', $3, to_timestamp($4), to_timestamp($5))
		`, generateCommandID(), req.PluginID, metricIDsToBytes(req.MetricIDs), req.WindowStart, req.WindowEnd)
		if err != nil {
			log.Error().Err(err).Msg("insert backfill command failed")
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}

		writeJSON(w, http.StatusAccepted, map[string]string{
			"status":  "pending",
			"message": "backfill command queued",
		})
	}
}

func pluginDeclaresWindowedBackfill(ctx context.Context, db controlDB, pluginID string) (found, allowed bool, err error) {
	var raw []byte
	err = db.QueryRow(ctx, `
		SELECT COALESCE(capabilities, '{}'::jsonb)
		FROM plugins
		WHERE id = $1 OR name = $1
		LIMIT 1
	`, pluginID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	caps := parseCapabilityJSON(raw)
	return true, caps.WindowedBackfill, nil
}

// generateCommandID creates a unique command identifier. command_id is the
// command_log primary key, so a timestamp alone is not enough — two commands
// in the same second must not collide. 6 random bytes ≈ 2^48 keyspace.
func generateCommandID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		// Extremely unlikely; nanosecond timestamp as last resort.
		return "cmd_" + time.Now().Format("20060102T150405.000000000")
	}
	return "cmd_" + hex.EncodeToString(b)
}

// metricIDsToBytes serializes a slice of metric IDs to JSON bytes.
func metricIDsToBytes(ids []string) []byte {
	b, _ := json.Marshal(ids)
	return b
}
