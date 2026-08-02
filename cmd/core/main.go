// Command core runs the core gRPC server for plugin connections,
// wiring the full M0–M5 pipeline: registration → ingestion → detection → alert → research.
package main

import (
	"context"
	"encoding/json"
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
	"capital_observatory/internal/core/noise"
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

	// ── M3: detectors + alert engine + outbox + noise budget ────────────────────
	detEngine := detector.NewEngine(
		detector.ThresholdDetector{},
		detector.PercentileDetector{},
		detector.TrendDetector{},
	)
	alertStore := store.NewPostgresAlertStore(db)
	alertEng := alert.NewEngine(alertStore)
	outboxStore := store.NewPostgresOutboxStore(db)
	outboxWorker := alert.NewOutboxWorker(outboxStore, 5*time.Second)

	// Noise budget tracker (PRD §十七: ≤10 alerts/day under default rules).
	// The tracker is in-process and resets on restart, which is fine — each
	// restart re-calibrates from the trailing 24h of emissions.
	budgetTracker := noise.NewInMemoryBudget(noise.DefaultBudgetPerDay)

	// Notification-hook stub: without a registered handler every alert.triggered
	// event would exhaust its retries and land in status='failed'. Phase 2
	// replaces this with a real notifier (webhook / email / slack). This
	// handler also records the emission against the noise budget so operators
	// can spot when the system is over its ≤10 alerts/day target.
	outboxWorker.RegisterHandler(alert.EventTypeAlertTriggered, func(_ context.Context, ev alert.OutboxEvent) error {
		// Outbox payload is JSON; unmarshal just the fields we need for the
		// log and the budget tracker. A malformed payload must not abort the
		// outbox loop, so we log the failure and fall through with zero fields.
		var parsed struct {
			AlertID  string `json:"alert_id"`
			Title    string `json:"title"`
			Severity string `json:"severity"`
			MetricID string `json:"metric_id"`
			RuleID   int    `json:"rule_id"`
		}
		if uerr := json.Unmarshal(ev.Payload, &parsed); uerr != nil {
			log.Warn().Err(uerr).
				Int("event_id", ev.ID).
				Msg("alert payload unmarshal failed; dispatching with empty fields")
		}

		log.Info().
			Str("alert_id", parsed.AlertID).
			Str("title", parsed.Title).
			Str("severity", parsed.Severity).
			Str("metric_id", parsed.MetricID).
			Int("rule_id", parsed.RuleID).
			Msg("alert event dispatched (notification stub)")

		// Record to noise budget tracker (non-fatal).
		budgetTracker.Record(context.Background(), parsed.RuleID, parsed.Title, time.Now())
		return nil
	})

	// Budget status reporter: log current noise-budget consumption every 5 min
	// so operators can see which rules dominate the ≤10 alerts/day target.
	go func() {
		t := time.NewTicker(5 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-t.C:
				st := budgetTracker.Status(context.Background(), now)
				if st.OverBudget {
					log.Warn().
						Int("projected_daily", int(st.ProjectedDaily+0.5)).
						Int("budget", st.BudgetPerDay).
						Interface("noisy_rules", st.Rules).
						Msg("noise budget EXCEEDED")
				} else {
					log.Info().
						Int("projected_daily", int(st.ProjectedDaily+0.5)).
						Int("budget", st.BudgetPerDay).
						Msg("noise budget ok")
				}
			}
		}
	}()

	// ── M4: research assembly ──────────────────────────────────────────────────
	researchStore := store.NewPostgresResearchStore(db)
	researchAsm := research.NewAssembler(researchStore)

	// ── C7: command_log control-plane ──────────────────────────────────────────
	cmdStore := store.NewPostgresCommandStore(db)

	// ── Manager (wires M1–M5; researchStore doubles as the observation querier) ─
	mgr := pluginmgr.NewManager(repo, db, detEngine, alertEng, researchAsm, researchStore, cmdStore)
	handler := pluginmgr.NewHandler(mgr)

	// ── CommandDispatcher: poll command_log → dispatch → wait for CommandAck ────
	go mgr.StartCommandDispatcher(ctx, 2*time.Second)

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
