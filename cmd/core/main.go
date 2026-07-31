// Command core runs the core gRPC server for plugin connections.
package main

import (
	"context"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc"

	"capital_observatory/internal/core/ontology"
	"capital_observatory/internal/core/pluginmgr"
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

	store := ontology.NewStore(db)
	mgr := pluginmgr.NewManager(store)
	handler := pluginmgr.NewHandler(mgr)

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
