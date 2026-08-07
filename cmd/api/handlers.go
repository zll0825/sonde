package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

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

// alertsHandler returns the list of active alerts.
func alertsHandler(db statusQuerier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := db.Query(r.Context(), `
			SELECT id, title, summary, severity, metric_id, triggered_at, status,
			       source_provider, source_class, dedup_count, last_deduplicated_at
			FROM alerts WHERE status = 'active'
			ORDER BY triggered_at DESC LIMIT 100
		`)
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
			)
			if err := rows.Scan(&id, &title, &summary, &severity, &metricID, &triggeredAt, &status,
				&sourceProvider, &sourceClass, &dedupCount, &lastDeduplicatedAt); err != nil {
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

// syncHandler triggers a manual sync for a given plugin+metrics.
func syncHandler(db *pgxpool.Pool) http.HandlerFunc {
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
func backfillHandler(db *pgxpool.Pool) http.HandlerFunc {
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

		_, err := db.Exec(r.Context(), `
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
