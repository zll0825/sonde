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

	// Register a handler for Core messages (e.g., sync/backfill commands).
	runner.OnCoreMessage(func(msg *pb.CoreMessage) {
		switch p := msg.Payload.(type) {
		case *pb.CoreMessage_Sync:
			log.Info().Str("command_id", p.Sync.CommandId).Str("reason", p.Sync.Reason).Msg("received sync command")
		case *pb.CoreMessage_Backfill:
			log.Info().Str("command_id", p.Backfill.CommandId).Str("reason", p.Backfill.Reason).Msg("received backfill command")
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

	// Collector submits snapshots on every tick.
	collectorInstance := collector.Mock{}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				snapshots, cerr := collectorInstance.GetSnapshots(ctx)
				if cerr != nil {
					log.Error().Err(cerr).Msg("collector failed to get snapshots")
					continue
				}
				msgs := snapshotsToProto(snapshots, pluginVersion)
				runner.SubmitSnapshots(pluginID, msgs)
				log.Info().Int("count", len(msgs)).Msg("snapshots submitted")
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
			QualityGrade:        pb.QualityGrade_QUALITY_GRADE_ESTIMATED,
		})
	}
	return result
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
