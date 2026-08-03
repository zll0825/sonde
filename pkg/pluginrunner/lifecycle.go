package pluginrunner

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

	pb "capital_observatory/pkg/proto/plugin/v1"
)

// Config captures everything plugin-specific that the lifecycle needs to run.
// The plugin's main.go constructs one of these and calls Lifecycle.Run.
type Config struct {
	// PluginName is the human readable name (e.g. "etf", "crypto", "macro").
	PluginName string

	// Version is the plugin's semver string (e.g. "0.1.0").
	Version string

	// DefaultInterval is the ticker cadence when neither --interval nor
	// COLLECTION_INTERVAL is set.
	DefaultInterval time.Duration

	// BuildRegistration returns the full RegisterPluginRequest for this plugin.
	// Called once per session, allowing the plugin to customize its registration.
	BuildRegistration func() *pb.RegisterPluginRequest

	// SetupCollector is called with the ctx + runSession environment and returns
	// the Provider (and whether it supports WindowedProvider). This is where the
	// plugin chooses mock vs real data source.
	SetupCollector func(ctx context.Context) (Provider, bool, error)
}

// Lifecycle owns the long-lived process: signal handling, reconnection loop,
// gRPC dialing, registration, message dispatch, and the collection ticker.
type Lifecycle struct {
	cfg Config
}

// NewLifecycle creates a Lifecycle from the given config.
func NewLifecycle(cfg Config) *Lifecycle {
	return &Lifecycle{cfg: cfg}
}

// Run blocks until the process is terminated (SIGINT/SIGTERM). It reconnects
// forever on session errors with exponential backoff capped at 60s.
func (l *Lifecycle) Run() {
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
		interval = envDurationOrDefault("COLLECTION_INTERVAL", l.cfg.DefaultInterval)
	}

	log.Info().
		Str("addr", coreAddr).
		Dur("interval", interval).
		Str("version", l.cfg.Version).
		Msgf("%s plugin starting", l.cfg.PluginName)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	var backoff time.Duration
	for {
		if err := l.runSession(ctx, coreAddr, interval); err != nil {
			log.Error().Err(err).Msg("session ended, reconnecting")
		}
		if ctx.Err() != nil {
			log.Info().Msgf("context canceled, %s plugin shutting down", l.cfg.PluginName)
			return
		}
		// Exponential backoff with cap (max 60s).
		backoff = min(backoff*2+time.Second, 60*time.Second)
		log.Info().Dur("backoff", backoff).Msg("waiting before reconnect")
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			log.Info().Msgf("context canceled during backoff, %s plugin shutting down", l.cfg.PluginName)
			return
		}
	}
}

// runSession establishes one connection: dial → register → collect → stream loop.
func (l *Lifecycle) runSession(ctx context.Context, coreAddr string, interval time.Duration) error {
	conn, err := grpc.NewClient(coreAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("failed to dial core at %s: %w", coreAddr, err)
	}
	defer func() {
		if cerr := conn.Close(); cerr != nil {
			log.Warn().Err(cerr).Msg("failed to close gRPC connection")
		}
	}()

	client := pb.NewPluginHostClient(conn)
	runner := New(l.cfg.PluginName, l.cfg.Version, client)

	pluginID, err := runner.Register(ctx, l.cfg.BuildRegistration())
	if err != nil {
		return fmt.Errorf("registration failed: %w", err)
	}
	log.Info().Str("plugin_id", pluginID).Msg("plugin registered successfully")

	collector, windowed, err := l.cfg.SetupCollector(ctx)
	if err != nil {
		return fmt.Errorf("setup collector: %w", err)
	}

	// Build the collect functions (current snapshot vs windowed).
	collectOnce := func(commandID string) {
		snaps, cerr := collector.GetSnapshots(ctx)
		if cerr != nil {
			log.Error().Err(cerr).Msg("collector failed to get snapshots")
			if commandID != "" {
				runner.SubmitCommandAck(commandID, "failed", cerr.Error(), 0, cerr.Error())
			}
			return
		}
		submitAndAck(runner, pluginID, commandID, snaps)
	}

	var wColl WindowedProvider
	if windowed {
		wp, ok := collector.(WindowedProvider)
		if !ok {
			return fmt.Errorf("SetupCollector returned windowed=true but %T does not implement WindowedProvider", collector)
		}
		wColl = wp
	}
	collectWindowed := func(commandID string, start, end time.Time) {
		if wColl == nil {
			log.Warn().Msg("collector does not implement WindowedProvider; falling back to current snapshot")
			collectOnce(commandID)
			return
		}
		snaps, cerr := wColl.GetSnapshotsForWindow(ctx, start, end)
		if cerr != nil {
			log.Error().Err(cerr).Msg("collector failed to get windowed snapshots")
			runner.SubmitCommandAck(commandID, "failed", cerr.Error(), 0, cerr.Error())
			return
		}
		log.Info().
			Str("command_id", commandID).
			Time("window_start", start).
			Time("window_end", end).
			Int("count", len(snaps)).
			Msg("backfill window collected")
		submitAndAck(runner, pluginID, commandID, snaps)
	}

	// Register core message handlers.
	runner.OnCoreMessage(func(msg *pb.CoreMessage) {
		switch p := msg.Payload.(type) {
		case *pb.CoreMessage_Sync:
			log.Info().Str("command_id", p.Sync.CommandId).Str("reason", p.Sync.Reason).Msg("received sync command")
			collectOnce(p.Sync.CommandId)
		case *pb.CoreMessage_Backfill:
			log.Info().
				Str("command_id", p.Backfill.CommandId).
				Str("reason", p.Backfill.Reason).
				Time("window_start", time.Unix(p.Backfill.WindowStart, 0)).
				Time("window_end", time.Unix(p.Backfill.WindowEnd, 0)).
				Msg("received backfill command")
			collectWindowed(p.Backfill.CommandId,
				time.Unix(p.Backfill.WindowStart, 0),
				time.Unix(p.Backfill.WindowEnd, 0))
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

	// Periodic collection loop. Collect immediately on session start — with
	// hourly/daily intervals a fresh stack would otherwise sit empty until the
	// first tick (24h for the ETF plugin). Reconnect re-collection is safe:
	// the observations idempotency index dedups identical samples.
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		collectOnce("")
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				collectOnce("")
			}
		}
	}()

	return runner.Run(ctx, pluginID)
}

// submitAndAck converts snapshots to proto, submits them, and acks the command
// (if any) with the number of collected snapshots.
func submitAndAck(runner *Runner, pluginID, commandID string, snaps []Snapshot) {
	protoSnaps := SnapshotsToProto(snaps, runner.version)
	runner.SubmitSnapshots(pluginID, protoSnaps)
	log.Info().
		Str("command_id", commandID).
		Int("count", len(protoSnaps)).
		Msg("snapshots submitted")
	if commandID != "" {
		runner.SubmitCommandAck(commandID, "success",
			fmt.Sprintf("%d snapshots collected", len(protoSnaps)),
			int32(len(protoSnaps)), "")
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
