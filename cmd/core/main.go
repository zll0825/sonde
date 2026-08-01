// Command core runs the core gRPC server for plugin connections,
// wiring the full M0–M5 pipeline: registration → ingestion → detection → alert → research.
package main

import (
	"context"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc"

	"capital_observatory/internal/core/alert"
	"capital_observatory/internal/core/detector"
	"capital_observatory/internal/core/ontology"
	"capital_observatory/internal/core/pluginmgr"
	"capital_observatory/internal/core/research"
	"capital_observatory/internal/core/store"
	pb "capital_observatory/pkg/proto/plugin/v1"
)

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://capital:capital_dev@localhost:5432/capital_observatory?sslmode=disable"
	}

	ctx := signalContext()

	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to connect to database")
	}
	defer db.Close()

	if err := db.Ping(ctx); err != nil {
		log.Fatal().Err(err).Msg("database ping failed")
	}

	// ── Persistence layer (M0/M1) ──────────────────────────────────────────────
	repo := ontology.NewStore(db)

	// ── M3: detectors + alert engine + outbox ──────────────────────────────────
	detEngine := detector.NewEngine(detector.ThresholdDetector{})
	alertStore := store.NewPostgresAlertStore(db)
	alertEng := alert.NewEngine(alertStore)
	outboxStore := store.NewPostgresOutboxStore(db)
	outboxWorker := alert.NewOutboxWorker(outboxStore, 5*time.Second)

	// ── M4: research assembly ──────────────────────────────────────────────────
	researchStore := store.NewPostgresResearchStore(db)
	researchAsm := research.NewAssembler(researchStore)

	// ── Observation querier (shared by pipeline + outbox) ──────────────────────
	obsQuerier := store.NewPostgresResearchStore(db)

	// ── Manager (wires M1–M4) ──────────────────────────────────────────────────
	mgr := pluginmgr.NewManager(repo, db, detEngine, alertEng, researchAsm, obsQuerier)
	handler := pluginmgr.NewHandler(mgr)

	// ── gRPC server ────────────────────────────────────────────────────────────
	addr := os.Getenv("CORE_BIND")
	if addr == "" {
		addr = ":50051"
	}

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatal().Err(err).Str("addr", addr).Msg("listen failed")
	}

	srv := grpc.NewServer()
	pb.RegisterPluginHostServer(srv, handler)

	log.Info().Str("addr", addr).Msg("core gRPC server listening")

	go func() {
		if err := srv.Serve(lis); err != nil {
			log.Fatal().Err(err).Msg("grpc serve failed")
		}
	}()

	// ── Outbox worker: dispatch Tier-1 events ──────────────────────────────────
	go outboxWorker.Run(ctx, 10)

	<-ctx.Done()
	log.Info().Msg("shutting down...")
	srv.GracefulStop()
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
