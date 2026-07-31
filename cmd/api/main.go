// Command api runs the HTTP API server for the capital-observatory frontend.
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://capital:capital_dev@localhost:5432/capital_observatory?sslmode=disable"
	}

	ctx := signalContext()

	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatal().Err(err).Msg("database connection failed")
	}
	defer db.Close()

	if err := db.Ping(ctx); err != nil {
		log.Fatal().Err(err).Msg("database ping failed")
	}

	addr := os.Getenv("API_BIND")
	if addr == "" {
		addr = ":8080"
	}

	mux := http.NewServeMux()

	// Health check.
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// List active alerts (Capital Radar homepage data).
	mux.HandleFunc("/api/alerts", alertsHandler(db))

	// Get research context for an alert.
	mux.HandleFunc("/api/research/", researchHandler(db))

	// Manual Sync / Backfill (control endpoint).
	mux.HandleFunc("/api/control/sync", syncHandler(db))
	mux.HandleFunc("/api/control/backfill", backfillHandler(db))

	srv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	go func() {
		log.Info().Str("addr", addr).Msg("API server listening")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("api serve failed")
		}
	}()

	<-ctx.Done()
	log.Info().Msg("API shutting down...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

// signalContext returns a context that cancels on SIGINT or SIGTERM.
func signalContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()
	return ctx
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// alertsHandler returns the list of active alerts.
func alertsHandler(db *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := db.Query(r.Context(), `
			SELECT id, title, summary, severity, metric_id, triggered_at, status
			FROM alerts WHERE status = 'active'
			ORDER BY triggered_at DESC LIMIT 100
		`)
		if err != nil {
			log.Error().Err(err).Msg("query alerts failed")
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		defer rows.Close()

		var alerts []map[string]interface{}
		for rows.Next() {
			var (
				id, title, summary, severity, metricID, status string
				triggeredAt                                    time.Time
			)
			if err := rows.Scan(&id, &title, &summary, &severity, &metricID, &triggeredAt, &status); err != nil {
				continue
			}
			alerts = append(alerts, map[string]interface{}{
				"id":           id,
				"title":        title,
				"summary":      summary,
				"severity":     severity,
				"metric_id":    metricID,
				"triggered_at": triggeredAt,
				"status":       status,
			})
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
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "research context not found"})
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

		// M5: insert into command_log; core will pick it up via outbox/polling.
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

// generateCommandID creates a short unique command identifier.
func generateCommandID() string {
	return "cmd_" + time.Now().Format("20060102T150405")
}

// metricIDsToBytes serializes a slice of metric IDs to JSON bytes.
func metricIDsToBytes(ids []string) []byte {
	b, _ := json.Marshal(ids)
	return b
}
