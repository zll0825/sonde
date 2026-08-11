// api 进程入口：REST 端点（告警查询、研究上下文、Sync/Backfill 控制命令）
// 与 Capital Radar 前端静态托管；含鉴权中间件（$API_TOKEN 非空时启用
// Bearer 校验）与按 IP 限流。
package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"

	coreevent "capital_observatory/internal/core/event"
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

	// List active alerts.
	mux.HandleFunc("/api/alerts", alertsHandler(db))

	// Pulse dashboard aggregate (read-only): plugin health, alert budget,
	// metric freshness + sparklines — one fetch for the whole status view.
	mux.HandleFunc("/api/status", statusHandler(db))

	// Get research context for an alert.
	mux.HandleFunc("/api/research/", researchHandler(db))

	// Manual Sync / Backfill (control endpoint).
	mux.HandleFunc("/api/control/sync", syncHandler(db))
	mux.HandleFunc("/api/control/backfill", backfillHandler(db))

	// B: signal quality timeline (GET, read-only, public for dashboards).
	mux.HandleFunc("GET /api/signal/quality/{metric_uid}", signalQualityHandler(db))

	// C: ontology Phase 2 — manual relations CRUD (Go 1.22 method+path patterns).
	ont := newOntologyStore(db)
	mux.HandleFunc("GET /api/ontology/relations/", ont.ontologyRelationsHandler)
	mux.HandleFunc("POST /api/ontology/relations/", ont.ontologyRelationsHandler)
	mux.HandleFunc("GET /api/ontology/relations/{id}", ont.ontologyRelationByIDHandler)
	mux.HandleFunc("PUT /api/ontology/relations/{id}", ont.ontologyRelationByIDHandler)
	mux.HandleFunc("DELETE /api/ontology/relations/{id}", ont.ontologyRelationByIDHandler)

	// C: ontology Phase 2 — relation-suggestion candidate accept / reject.
	mux.HandleFunc("GET /api/ontology/candidates/", ont.ontologyCandidatesHandler)
	mux.HandleFunc("GET /api/ontology/candidates/{id}", ont.ontologyCandidatesHandler)
	mux.HandleFunc("POST /api/ontology/candidates/{id}/accept", ont.ontologyCandidateActionHandler)
	mux.HandleFunc("POST /api/ontology/candidates/{id}/reject", ont.ontologyCandidateActionHandler)

	// A: event cluster snapshots ring buffer (read-only GET). The API reads
	// from the same JSONL snapshot file that Core writes to (CLUSTER_SNAPSHOTS_FILE).
	// This shares the clustering history across both processes on the same
	// machine without an RPC layer.
	clusterSnapStore := initClusterSnapStore()
	if clusterSnapStore != nil {
		defer clusterSnapStore.Close()
		mux.HandleFunc("GET /api/clusters/", clusterHandlerWithStore(clusterSnapStore))
	} else {
		// Fallback: empty ring for backward compatibility when no store is
		// configured (returns empty list).
		clusterRing := coreevent.NewClusterRing(200)
		mux.HandleFunc("GET /api/clusters/", clusterHandler(clusterRing))
	}

	// Static frontend: mounts web/ at "/" so the frontend can be served by the
	// API server (fixes the file:// → fetch failure — M5 acceptance #5).
	// WEB_DIR env var allows overriding; defaults to ./web (project root when
	// CWD is repo root) or ../../web (when binary lives in cmd/api/).
	webDir := os.Getenv("WEB_DIR")
	if webDir == "" {
		webDir = "./web"
	}
	mux.Handle("/", http.FileServer(http.Dir(webDir)))

	// Wrap with middleware: rate-limit → auth → handler.
	var handler http.Handler = mux
	handler = authMiddleware(handler)
	handler = rateLimitMiddleware(handler)

	srv := &http.Server{
		Addr:         addr,
		Handler:      handler,
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

// initClusterSnapshotStore creates a ClusterSnapshotStore from the
// CLUSTER_SNAPSHOTS_FILE env var. It returns nil (with a startup log) when the
// env var is missing — the API simply serves an empty cluster list in that
// case.
func initClusterSnapStore() *coreevent.ClusterSnapshotStore {
	snapPath := os.Getenv("CLUSTER_SNAPSHOTS_FILE")
	if snapPath == "" {
		return nil
	}
	store, err := coreevent.NewClusterSnapshotStore(snapPath)
	if err != nil {
		log.Warn().Err(err).Str("path", snapPath).Msg("cluster snapshot store init failed")
		return nil
	}
	return store
}
