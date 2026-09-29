// Package e2e 把 core 的真实装配（gRPC 服务端、摄入、检测、告警、研究）
// 搬进测试进程，让插件模块用自己的真实采集器 + 录制的上游响应跑完整链路：
//
//	采集器 → pluginrunner.Runner → gRPC → 摄入 → detection outbox
//	       → 规则评估 → alerts → research / alert.triggered outbox
//
// 与 cmd/core 的差别只在边缘：outbox 由测试逐批 Tick（确定性，不靠轮询
// 定时器），alert.triggered 只记录不外发，聚类与噪音预算不装。
//
// 需要 TEST_DATABASE_URL 指向已跑完迁移的测试库；每个用例开始时清空全部
// 业务表，库里原有数据会丢——只能指向测试库。
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	"sonde/internal/core/alert"
	"sonde/internal/core/detector"
	coreevent "sonde/internal/core/event"
	"sonde/internal/core/notifier"
	"sonde/internal/core/ontology"
	"sonde/internal/core/pluginmgr"
	"sonde/internal/core/research"
	"sonde/internal/core/store"
	"sonde/pkg/pluginrunner"
	pb "sonde/pkg/proto/plugin/v1"
)

// Core is an in-process Core wired like cmd/core, listening on a loopback port.
type Core struct {
	DB   *pgxpool.Pool
	Addr string

	outbox *alert.OutboxWorker

	mu            sync.Mutex
	notifications []notifier.AlertInfo
}

// RequireDB opens TEST_DATABASE_URL and empties every table except the
// migration ledger, before and after the test. Skips when the variable is unset.
func RequireDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run end-to-end tests")
	}
	db, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect test database: %v", err)
	}
	t.Cleanup(db.Close)
	truncateAll(t, db)
	t.Cleanup(func() { truncateAll(t, db) })
	return db
}

func truncateAll(t *testing.T, db *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	_, err := db.Exec(ctx, `
		DO $$
		DECLARE stmt TEXT;
		BEGIN
			SELECT 'TRUNCATE TABLE ' || string_agg(format('%I', tablename), ', ') || ' RESTART IDENTITY CASCADE'
			  INTO stmt
			  FROM pg_tables
			 WHERE schemaname = 'public' AND tablename <> 'schema_migrations';
			IF stmt IS NOT NULL THEN
				EXECUTE stmt;
			END IF;
		END $$`)
	if err != nil {
		t.Fatalf("truncate test database: %v", err)
	}
}

// StartCore wires the store, detectors, alert engine, research assembler and
// plugin manager exactly as cmd/core does and serves PluginHost on loopback.
func StartCore(t *testing.T, db *pgxpool.Pool) *Core {
	t.Helper()

	repo := ontology.NewStore(db)
	alertStore := store.NewPostgresAlertStore(db)
	alertEng := alert.NewEngine(alertStore)
	researchStore := store.NewPostgresResearchStore(db)
	assembler := research.NewAssembler(researchStore)
	mgr := pluginmgr.NewManager(repo, db, detector.NewDefaultEngine(), alertEng, researchStore, store.NewPostgresCommandStore(db))

	c := &Core{DB: db, outbox: alert.NewOutboxWorker(store.NewPostgresOutboxStore(db), time.Hour)}

	c.outbox.RegisterHandler(coreevent.TypeDetectionRequested, func(ctx context.Context, ev alert.OutboxEvent) error {
		var req coreevent.DetectionRequest
		if err := json.Unmarshal(ev.Payload, &req); err != nil {
			return fmt.Errorf("unmarshal detection request: %w", err)
		}
		return mgr.EvaluateDetection(ctx, req.MetricID, req.PluginID)
	})
	c.outbox.RegisterHandler(coreevent.TypeResearchRequested, func(ctx context.Context, ev alert.OutboxEvent) error {
		var req coreevent.ResearchRequest
		if err := json.Unmarshal(ev.Payload, &req); err != nil {
			return fmt.Errorf("unmarshal research request: %w", err)
		}
		if err := req.Validate(); err != nil {
			return err
		}
		a, err := alertStore.GetAlertByID(ctx, req.AlertID)
		if err != nil {
			return fmt.Errorf("load alert %s: %w", req.AlertID, err)
		}
		if a == nil {
			return fmt.Errorf("load alert %s: not found", req.AlertID)
		}
		rc, err := assembler.Assemble(ctx, *a)
		if err != nil {
			return fmt.Errorf("assemble research for %s: %w", req.AlertID, err)
		}
		return assembler.SaveSnapshot(ctx, rc)
	})
	c.outbox.RegisterHandler(alert.EventTypeAlertTriggered, func(ctx context.Context, ev alert.OutboxEvent) error {
		var info notifier.AlertInfo
		if err := json.Unmarshal(ev.Payload, &info); err != nil {
			return fmt.Errorf("unmarshal alert payload: %w", err)
		}
		c.mu.Lock()
		c.notifications = append(c.notifications, info)
		c.mu.Unlock()
		return nil
	})

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	pb.RegisterPluginHostServer(srv, pluginmgr.NewHandler(mgr))
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	c.Addr = lis.Addr().String()
	return c
}

// DrainOutbox dispatches pending outbox events batch by batch until none are
// left. Detection events enqueue research and notification events, so one
// pass is not enough. Fails the test if any event ends up failed.
func (c *Core) DrainOutbox(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(30 * time.Second)
	for {
		dispatched, failed := c.outbox.Tick(ctx, 100)
		if failed > 0 {
			var lastErr *string
			_ = c.DB.QueryRow(ctx, `SELECT last_error FROM event_outbox WHERE last_error IS NOT NULL ORDER BY id DESC LIMIT 1`).Scan(&lastErr)
			msg := "<none>"
			if lastErr != nil {
				msg = *lastErr
			}
			t.Fatalf("outbox: %d event(s) failed; last error: %s", failed, msg)
		}
		if dispatched == 0 {
			var pending int
			if err := c.DB.QueryRow(ctx, `SELECT COUNT(*) FROM event_outbox WHERE status = 'pending'`).Scan(&pending); err != nil {
				t.Fatalf("count pending outbox events: %v", err)
			}
			if pending == 0 {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("outbox did not drain within 30s")
		}
	}
}

// Notifications returns the alert.triggered payloads dispatched so far.
func (c *Core) Notifications() []notifier.AlertInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]notifier.AlertInfo(nil), c.notifications...)
}

// Plugin is a registered plugin with an open stream session to Core.
type Plugin struct {
	ID      string
	version string
	stream  pb.PluginHost_MaintainSessionClient
}

// Connect registers the plugin's catalog and opens its session stream, the
// same two calls pluginrunner makes on every (re)connect. The stream is driven
// directly rather than through pluginrunner.Runner so each push can wait for
// its own PushAck.
func (c *Core) Connect(t *testing.T, reg *pb.RegisterPluginRequest) *Plugin {
	t.Helper()
	conn, err := grpc.NewClient(c.Addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial core: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := pb.NewPluginHostClient(conn)

	name, version := reg.GetInfo().GetName(), reg.GetInfo().GetVersion()
	regCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pluginID, err := pluginrunner.New(name, version, client).Register(regCtx, reg)
	if err != nil {
		t.Fatalf("register %s: %v", name, err)
	}

	sessionCtx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	sessionCtx = metadata.AppendToOutgoingContext(sessionCtx, "x-plugin-id", pluginID)
	stream, err := client.MaintainSession(sessionCtx)
	if err != nil {
		t.Fatalf("open session for %s: %v", name, err)
	}
	return &Plugin{ID: pluginID, version: version, stream: stream}
}

// Push sends one PushSnapshots batch and waits for Core's PushAck. Core only
// acks after ingestion commits, so observations and detection work items are
// in the database when Push returns. Core sends no ack when ingestion fails;
// the timeout turns that into a test failure instead of a hang.
func (p *Plugin) Push(t *testing.T, snaps []pluginrunner.Snapshot) *pb.PushAck {
	t.Helper()
	err := p.stream.Send(&pb.PluginMessage{Payload: &pb.PluginMessage_PushSnapshots{
		PushSnapshots: &pb.PushSnapshotsRequest{
			PluginId:  p.ID,
			Snapshots: pluginrunner.SnapshotsToProto(snaps, p.version),
		},
	}})
	if err != nil {
		t.Fatalf("send snapshots: %v", err)
	}

	type result struct {
		ack *pb.PushAck
		err error
	}
	got := make(chan result, 1)
	go func() {
		for {
			msg, err := p.stream.Recv()
			if err != nil {
				got <- result{err: err}
				return
			}
			if ack := msg.GetPushAck(); ack != nil {
				got <- result{ack: ack}
				return
			}
		}
	}()
	select {
	case r := <-got:
		if r.err != nil {
			t.Fatalf("session closed before PushAck: %v", r.err)
		}
		return r.ack
	case <-time.After(30 * time.Second):
		t.Fatal("no PushAck within 30s (ingestion failed?)")
		return nil
	}
}
