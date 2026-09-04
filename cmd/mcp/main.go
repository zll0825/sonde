// Command mcp is a read-only Model Context Protocol (MCP) server for Capital Observatory.
// It exposes active alerts, research snapshots, and ontology data to AI research assistants.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"capital_observatory/internal/mcp"
)

const usageText = `capital-observatory mcp — read-only research assistant MCP server

Usage:
  mcp [flags]

Flags:
  -http addr   Serve MCP over HTTP/SSE instead of stdio (e.g. 127.0.0.1:8081)
  -h, --help   Show this help

Environment variables:
  DATABASE_URL   PostgreSQL connection string (defaults to local compose DSN)
  API_TOKEN      Optional bearer token required when serving over HTTP
`

func main() {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usageText) }
	httpAddr := fs.String("http", "", "Serve MCP over HTTP/SSE on the given address (e.g. 127.0.0.1:8081)")

	if err := fs.Parse(os.Args[1:]); err != nil {
		if err == flag.ErrHelp {
			os.Exit(0)
		}
		os.Exit(2)
	}

	// Route logs to stderr so stdout remains a clean JSON-RPC channel
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://capital:capital_dev@localhost:5432/capital_observatory?sslmode=disable"
	}

	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Error().Err(err).Msg("failed to connect to database")
		os.Exit(1)
	}
	defer db.Close()

	registry := mcp.NewToolRegistry(db)
	server := mcp.NewServer(registry)

	if *httpAddr != "" {
		token := os.Getenv("API_TOKEN")
		if err := server.ServeHTTP(ctx, *httpAddr, token); err != nil {
			log.Error().Err(err).Msg("HTTP server exited")
			os.Exit(1)
		}
		return
	}

	log.Info().Msg("starting Capital Observatory MCP server over stdio")
	if err := server.ServeStdio(ctx, os.Stdin, os.Stdout); err != nil {
		log.Error().Err(err).Msg("stdio server exited")
		os.Exit(1)
	}
}
