// api 进程入口：REST 端点与 Capital Radar 静态托管。
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

	"capital_observatory/internal/api/handler"
	"capital_observatory/internal/api/middleware"
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
	clusterStore := initClusterSnapStore()
	if clusterStore != nil {
		defer clusterStore.Close()
	}
	handler.Register(mux, handler.Deps{DB: db, ClusterStore: clusterStore})

	webDir := os.Getenv("WEB_DIR")
	if webDir == "" {
		webDir = "./web"
	}
	mux.Handle("/", http.FileServer(http.Dir(webDir)))

	var h http.Handler = mux
	h = middleware.Auth(h)
	h = middleware.RateLimit(h)

	srv := &http.Server{
		Addr:         addr,
		Handler:      h,
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
