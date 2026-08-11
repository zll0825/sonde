// core 进程入口：装配存储、本体、检测、告警、研究与通知组件，串起完整
// 链路 注册 → 摄入 → 检测 → 告警 → 研究，并启动 gRPC 插件服务端、outbox
// 工作器、命令派发器与噪音预算巡检定时器。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc"

	"capital_observatory/internal/core/alert"
	"capital_observatory/internal/core/classification"
	"capital_observatory/internal/core/detector"
	coreevent "capital_observatory/internal/core/event"
	"capital_observatory/internal/core/noise"
	"capital_observatory/internal/core/notifier"
	"capital_observatory/internal/core/ontology"
	"capital_observatory/internal/core/pluginmgr"
	"capital_observatory/internal/core/research"
	"capital_observatory/internal/core/store"
	"capital_observatory/pkg/model"
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

	// ── Notification channel (Phase 2) ──────────────────────────────────────────
	// Resolve picks the first configured channel; without credentials it degrades
	// to a NopNotifier and emits a startup warning. Ownership of retry semantics
	// belongs to the outbox worker — if Notify fails we return the error and let
	// MarkFailed/redelivery take over.
	n := notifier.Resolve()

	// ── A: classification event clustering ─────────────────────────────────────
	// Ring buffer holds the most recent N EventCluster snapshots for the API
	// endpoint /api/clusters/. The clusterer itself tracks open clusters in
	// memory; the ring buffer only records completed/recent snapshots.
	clusterer := classification.NewClusterer(classification.DefaultClusteringConfig())
	clusterRing := coreevent.NewClusterRing(100)
	var clusterMu sync.Mutex

	outboxWorker.RegisterHandler(alert.EventTypeAlertTriggered, func(ctx context.Context, ev alert.OutboxEvent) error {
		var parsed notifier.AlertInfo
		if uerr := json.Unmarshal(ev.Payload, &parsed); uerr != nil {
			// A malformed payload must not abort the outbox loop, so we log the
			// failure and dispatch with zero-valued AlertInfo.
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
			Msg("alert dispatched")

		// 记入噪音预算（仅统计，不影响派发结果）。
		budgetTracker.Record(ctx, parsed.RuleID, parsed.Title, time.Now())

		// Non-blocking classification — clustering must never delay the
		// critical notification path. We snapshot the needed fields because
		// parsed will go out of scope when the handler returns.
		go func(alertID, metricID, sev string, ruleID int, triggeredAt time.Time) {
			ca := model.Alert{
				ID:          alertID,
				MetricID:    metricID,
				RuleID:      ruleID,
				Severity:    model.Severity(sev),
				TriggeredAt: triggeredAt,
			}
			cluster := clusterer.Receive(context.Background(), ca)
			if cluster == nil {
				return
			}
			clusterMu.Lock()
			defer clusterMu.Unlock()
			clusterRing.Push(coreevent.ClusterSnapshot{
				ClusterID:     cluster.ID,
				PrimaryEntity: cluster.PrimaryEntity,
				MemberCount:   len(cluster.Alerts),
				Severity:      cluster.MaxSeverity,
				LastTriggered: cluster.LastTriggered,
				Coalesced:     cluster.Coalesced,
			})
			log.Debug().
				Str("cluster_id", cluster.ID).
				Str("entity", cluster.PrimaryEntity).
				Int("size", len(cluster.Alerts)).
				Bool("coalesced", cluster.Coalesced).
				Msg("alert clustered")
		}(parsed.AlertID, parsed.MetricID, parsed.Severity, parsed.RuleID, parsed.TriggeredAt)

		// Send via the configured notifier. Errors propagate to the outbox worker,
		// which owns redelivery (retry-after backoff, terminal-failure threshold).
		if err := n.Notify(ctx, parsed); err != nil {
			log.Error().Err(err).
				Int("event_id", ev.ID).
				Str("alert_id", parsed.AlertID).
				Msg("notification send failed — outbox will retry")
			return err
		}
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
				st := budgetTracker.Status(ctx, now)
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
	outboxWorker.RegisterHandler(coreevent.TypeResearchRequested, func(ctx context.Context, ev alert.OutboxEvent) error {
		researchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if err := handleResearchRequest(researchCtx, ev, alertStore, researchAsm); err != nil {
			return err
		}
		return nil
	})

	// ── C7: command_log control-plane ──────────────────────────────────────────
	cmdStore := store.NewPostgresCommandStore(db)

	// ── Manager (wires M1–M5; researchStore doubles as the observation querier) ─
	mgr := pluginmgr.NewManager(repo, db, detEngine, alertEng, researchStore, cmdStore)
	handler := pluginmgr.NewHandler(mgr)

	// Durable observation -> detection consumer. The observation and this work
	// item are committed together; a Core crash leaves the event pending.
	outboxWorker.RegisterHandler(coreevent.TypeDetectionRequested, func(ctx context.Context, ev alert.OutboxEvent) error {
		var req coreevent.DetectionRequest
		if err := json.Unmarshal(ev.Payload, &req); err != nil {
			return fmt.Errorf("unmarshal detection request: %w", err)
		}
		evalCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if err := mgr.EvaluateDetection(evalCtx, req.MetricID, req.PluginID); err != nil {
			return fmt.Errorf("evaluate detection %s: %w", req.DetectionKey, err)
		}
		return nil
	})

	if count, err := outboxStore.ReconcileDetectionRequests(ctx); err != nil {
		log.Error().Err(err).Msg("detection request reconciliation failed")
	} else if count > 0 {
		log.Info().Int64("count", count).Msg("reconciled detection requests")
	}

	const researchReconcileBatch = 100
	var reconciledResearch int64
	for {
		count, err := outboxStore.ReconcileResearchRequests(ctx, researchReconcileBatch)
		if err != nil {
			log.Error().Err(err).Msg("research request reconciliation failed")
			break
		}
		reconciledResearch += count
		if count < researchReconcileBatch {
			break
		}
	}
	if reconciledResearch > 0 {
		log.Info().Int64("count", reconciledResearch).Msg("reconciled research requests")
	}
	if audit, err := outboxStore.AuditResearchLinks(ctx); err != nil {
		log.Error().Err(err).Msg("research link audit failed")
	} else {
		log.Info().
			Int64("missing", audit.MissingSnapshots).
			Int64("duplicates", audit.DuplicateSnapshots).
			Int64("orphans", audit.OrphanSnapshots).
			Int64("failed", audit.FailedRequests).
			Msg("research link audit")
	}

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

type alertByIDStore interface {
	GetAlertByID(ctx context.Context, alertID string) (*model.Alert, error)
}

type researchRequestAssembler interface {
	Assemble(ctx context.Context, alert model.Alert) (*research.ResearchContext, error)
	SaveSnapshot(ctx context.Context, rc *research.ResearchContext) error
}

// handleResearchRequest consumes only research work. Keeping this separate
// from alert.triggered ensures an assembly retry never resends a notification.
func handleResearchRequest(
	ctx context.Context,
	ev alert.OutboxEvent,
	alerts alertByIDStore,
	assembler researchRequestAssembler,
) error {
	var req coreevent.ResearchRequest
	if err := json.Unmarshal(ev.Payload, &req); err != nil {
		return fmt.Errorf("unmarshal research request: %w", err)
	}
	if err := req.Validate(); err != nil {
		return err
	}
	persistedAlert, err := alerts.GetAlertByID(ctx, req.AlertID)
	if err != nil {
		return fmt.Errorf("load alert %s: %w", req.AlertID, err)
	}
	if persistedAlert == nil {
		return fmt.Errorf("load alert %s: not found", req.AlertID)
	}
	researchContext, err := assembler.Assemble(ctx, *persistedAlert)
	if err != nil {
		return fmt.Errorf("assemble research for %s: %w", req.AlertID, err)
	}
	if err := assembler.SaveSnapshot(ctx, researchContext); err != nil {
		return fmt.Errorf("save research snapshot for %s: %w", req.AlertID, err)
	}
	log.Info().Str("alert_id", req.AlertID).Msg("research snapshot assembled")
	return nil
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
