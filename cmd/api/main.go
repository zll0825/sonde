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

	// Get research context for an alert.
	mux.HandleFunc("/api/research/", researchHandler(db))

	// Manual Sync / Backfill (control endpoint).
	mux.HandleFunc("/api/control/sync", syncHandler(db))
	mux.HandleFunc("/api/control/backfill", backfillHandler(db))

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
