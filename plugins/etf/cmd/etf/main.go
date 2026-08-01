package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"capital_observatory/pkg/pluginrunner"
	pb "capital_observatory/pkg/proto/plugin/v1"
	"capital_observatory/plugins/etf/internal/collector"
)

const (
	pluginName    = "etf"
	pluginVersion = "0.1.0"
)

func main() {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})

	var coreAddr string
	var interval time.Duration
	flag.StringVar(&coreAddr, "addr", "", "Core gRPC address (overrides CORE_ADDR env)")
	flag.DurationVar(&interval, "interval", 0, "Collection interval (overrides COLLECTION_INTERVAL env)")
	flag.Parse()

	if coreAddr == "" {
		coreAddr = envOrDefault("CORE_ADDR", ":50051")
	}
	if interval == 0 {
		interval = envDurationOrDefault("COLLECTION_INTERVAL", 10*time.Second)
	}

	log.Info().
		Str("addr", coreAddr).
		Dur("interval", interval).
		Str("version", pluginVersion).
		Msg("ETF plugin starting")

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Main reconnect loop — registration and stream are retried forever.
	var backoff time.Duration
	for {
		if err := runSession(ctx, coreAddr, interval); err != nil {
			log.Error().Err(err).Msg("session ended, reconnecting")
		}
		if ctx.Err() != nil {
			log.Info().Msg("context canceled, ETF plugin shutting down")
			return
		}
		// Exponential backoff with cap (max 60s).
		backoff = min(backoff*2+time.Second, 60*time.Second)
		log.Info().Dur("backoff", backoff).Msg("waiting before reconnect")
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			log.Info().Msg("context canceled during backoff, ETF plugin shutting down")
			return
		}
	}
}

func runSession(ctx context.Context, coreAddr string, interval time.Duration) error {
	// Dial Core gRPC (insecure for local development).
	conn, err := grpc.NewClient(coreAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return fmt.Errorf("failed to dial core at %s: %w", coreAddr, err)
	}
	defer func() {
		if cerr := conn.Close(); cerr != nil {
			log.Warn().Err(cerr).Msg("failed to close gRPC connection")
		}
	}()

	client := pb.NewPluginHostClient(conn)
	runner := pluginrunner.New(pluginName, client)

	// Register the plugin.
	pluginID, err := runner.Register(ctx, buildRegistration())
	if err != nil {
		return fmt.Errorf("registration failed: %w", err)
	}
	log.Info().Str("plugin_id", pluginID).Msg("plugin registered successfully")

	// Collector: default to Yahoo Finance (real data); set PROVIDER=mock for synthetic.
	// Declared before collectOnce, which closes over it.
	var collectorInstance collector.Provider = collector.NewYahooCollector()
	if envOrDefault("PROVIDER", "yahoo") == "mock" {
		collectorInstance = collector.Mock{}
	}

	// collectOnce runs one collection cycle: fetch snapshots from the collector,
	// push them to Core, and report collected_count in a CommandAck. Shared by
	// the periodic tick loop AND the OnCoreMessage command handler so that
	// Sync/Backfill commands cause an immediate real collection.
	//
	// The commandID may be "" for the periodic tick loop (no ack needed).
	// For command-triggered runs, a CommandAck is sent back to Core once the
	// snapshots are successfully submitted — Core's dispatching loop is now
	// complete: API → command_log → dispatch → plugin collect → CommandAck.
	collectOnce := func(commandID string) {
		snaps, cerr := collectorInstance.GetSnapshots(ctx)
		if cerr != nil {
			log.Error().Err(cerr).Msg("collector failed to get snapshots")
			if commandID != "" {
				runner.SubmitCommandAck(commandID, "failed", cerr.Error(), 0, cerr.Error())
			}
			return
		}
		msgs := snapshotsToProto(snaps, pluginVersion)
		runner.SubmitSnapshots(pluginID, msgs)
		log.Info().
			Str("command_id", commandID).
			Int("count", len(msgs)).
			Msg("snapshots submitted")
		if commandID != "" {
			runner.SubmitCommandAck(commandID, "success", fmt.Sprintf("%d snapshots collected", len(msgs)), int32(len(msgs)), "")
		}
	}

	// Register a handler for Core messages (e.g., sync/backfill commands).
	runner.OnCoreMessage(func(msg *pb.CoreMessage) {
		switch p := msg.Payload.(type) {
		case *pb.CoreMessage_Sync:
			log.Info().Str("command_id", p.Sync.CommandId).Str("reason", p.Sync.Reason).Msg("received sync command")
			collectOnce(p.Sync.CommandId)
		case *pb.CoreMessage_Backfill:
			log.Info().Str("command_id", p.Backfill.CommandId).Str("reason", p.Backfill.Reason).Msg("received backfill command")
			collectOnce(p.Backfill.CommandId)
		case *pb.CoreMessage_PushAck:
			log.Info().
				Int32("inserted", p.PushAck.Inserted).
				Int32("deduplicated", p.PushAck.Deduplicated).
				Int32("rejected", p.PushAck.Rejected).
				Str("message", p.PushAck.Message).
				Msg("received push ack")
		default:
			log.Warn().Str("type", fmt.Sprintf("%T", p)).Msg("unknown core message type")
		}
	})

	// Periodic collection loop.
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				collectOnce("")
			}
		}
	}()

	// Block on the stream.
	return runner.Run(ctx, pluginID)
}

func buildRegistration() *pb.RegisterPluginRequest {
	return &pb.RegisterPluginRequest{
		Info: &pb.PluginInfo{
			Name:        pluginName,
			Version:     pluginVersion,
			Description: "Gold/Ethereum ETF flow tracker",
		},
		Entities: []*pb.EntityDeclaration{
			{
				Id:         "GLD",
				Name:       "GLD",
				EntityType: pb.EntityType_ENTITY_TYPE_ASSET,
				Tags:       []string{"gold", "etf"},
			},
			{
				Id:         "ETH-P",
				Name:       "Ethereum Prime",
				EntityType: pb.EntityType_ENTITY_TYPE_ASSET,
				Tags:       []string{"ethereum", "etf"},
			},
		},
		Metrics: []*pb.MetricDeclaration{
			{
				Id:          "gld_daily_flow",
				Name:        "GLD Daily Flow (USD)",
				Description: "Daily inflow/outflow for GLD in USD",
				Unit:        "USD",
				Frequency:   "daily",
				EntityId:    "GLD",
			},
			{
				Id:          "eth_daily_flow",
				Name:        "ETH-P Daily Flow (USD)",
				Description: "Daily inflow/outflow for ETH-P in USD",
				Unit:        "USD",
				Frequency:   "daily",
				EntityId:    "ETH-P",
			},
			{
				Id:          "gld_price",
				Name:        "GLD Price (USD)",
				Description: "Current price of GLD share in USD",
				Unit:        "USD",
				Frequency:   "daily",
				EntityId:    "GLD",
			},
		},
		Relations: []*pb.RelationSuggestion{
			{
				SourceId:     "GLD",
				TargetId:     "ETH-P",
				RelationType: "tracks",
				Direction:    pb.Direction_DIRECTION_FORWARD,
				Description:  "GLD tracks ETH-P",
			},
		},
		Rules: []*pb.RuleSuggestion{
			{
				Name:         "gld_flow_spike",
				MetricId:     "gld_daily_flow",
				DetectorName: "threshold",
				Severity:     pb.Severity_SEVERITY_WARNING,
				Config:       []byte(`{"operator":"gt","value":500000000,"consecutive":2}`),
				Description:  "GLD daily flow exceeds 500M USD threshold",
			},
		},
		ChangeLog: "Initial ETF plugin registration",
	}
}

func snapshotsToProto(snapshots []collector.Snapshot, version string) []*pb.MetricSnapshot {
	result := make([]*pb.MetricSnapshot, 0, len(snapshots))
	for _, s := range snapshots {
		result = append(result, &pb.MetricSnapshot{
			MetricId:            s.MetricID,
			Value:               s.Value,
			Timestamp:           s.Timestamp.Unix(),
			SourcePluginVersion: version,
			SourceProvider:      s.Provider,
			SourceFetchedAt:     s.Timestamp.Unix(),
			QualityGrade:        gradeToProto(s.Grade),
		})
	}
	return result
}

// gradeToProto converts a collector grade string to its proto enum value.
// Falls back to ESTIMATED for unknown/unset grades.
func gradeToProto(grade string) pb.QualityGrade {
	switch grade {
	case "realtime":
		return pb.QualityGrade_QUALITY_GRADE_REALTIME
	case "delayed":
		return pb.QualityGrade_QUALITY_GRADE_DELAYED
	case "estimated":
		return pb.QualityGrade_QUALITY_GRADE_ESTIMATED
	case "preliminary":
		return pb.QualityGrade_QUALITY_GRADE_PRELIMINARY
	case "revised":
		return pb.QualityGrade_QUALITY_GRADE_REVISED
	default:
		return pb.QualityGrade_QUALITY_GRADE_ESTIMATED
	}
}

func envOrDefault(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func envDurationOrDefault(key string, def time.Duration) time.Duration {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
		log.Warn().Str("key", key).Str("value", v).Msg("invalid duration env var, using default")
	}
	return def
}
