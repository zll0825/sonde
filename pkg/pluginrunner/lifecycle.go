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

	pb "sonde/pkg/proto/plugin/v1"
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

	// SetupCollector 在每次会话建立后调用，返回本插件的数据采集器。
	// 插件在这里决定使用 mock 还是真实数据源（PROVIDER 环境变量）。
	// Catalog windowedBackfill must match a WindowedProvider or the session
	// refuses to start. Lifecycle still type-asserts WindowedProvider when
	// executing Backfill commands.
	SetupCollector func(ctx context.Context) (Provider, error)
}

// Lifecycle owns the long-lived process: signal handling, reconnection loop,
// gRPC dialing, registration, message dispatch, and the collection ticker.
type Lifecycle struct {
	cfg      Config
	commands *commandLedger
	status   *collectionStatus
}

// NewLifecycle creates a Lifecycle from the given config.
func NewLifecycle(cfg Config) *Lifecycle {
	return &Lifecycle{
		cfg:      cfg,
		commands: newCommandLedger(defaultCommandLedgerCapacity, defaultCommandLedgerTTL),
		status:   &collectionStatus{},
	}
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

// runSession establishes one connection: validate collector → dial → register →
// collect → stream loop. Local startup validation must happen before any
// registration side effect; otherwise a missing credential creates a new
// registration version on every reconnect.
func (l *Lifecycle) runSession(ctx context.Context, coreAddr string, interval time.Duration) error {
	collector, err := l.cfg.SetupCollector(ctx)
	if err != nil {
		return fmt.Errorf("setup collector: %w", err)
	}

	reg := l.cfg.BuildRegistration()
	if err := ValidateCapabilities(reg, collector); err != nil {
		return err
	}

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

	pluginID, err := runner.Register(ctx, reg)
	if err != nil {
		return fmt.Errorf("registration failed: %w", err)
	}
	log.Info().Str("plugin_id", pluginID).Msg("plugin registered successfully")

	// executeCommand single-flights duplicate command IDs and replays a cached
	// successful snapshot set without calling the provider again.
	executeCommand := func(commandID string, collect func() ([]Snapshot, error)) {
		result := l.executeCollection(commandID, collect)
		// Publish the updated outcome immediately after this result. Snapshot and
		// ACK delivery retain priority if the bounded send queue is saturated.
		defer runner.SubmitHeartbeat(pluginID, l.heartbeatStatus(collector))
		if result.err != nil {
			errMessage := SanitizeCollectionError(result.err)
			log.Error().Str("error", errMessage).Str("command_id", commandID).Msg("collector command failed")
			submitted := true
			if len(result.snapshots) > 0 {
				submitted = submitSnapshots(runner, pluginID, commandID, result.snapshots)
			}
			if commandID != "" {
				collectedCount := int32(len(result.snapshots))
				if !submitted {
					errMessage = "snapshot enqueue failed after partial collection"
					collectedCount = 0
				}
				runner.SubmitCommandAck(commandID, "failed", errMessage, collectedCount, errMessage)
			}
			return
		}
		submitAndAck(runner, pluginID, commandID, result)
	}

	collectOnce := func(commandID string) {
		executeCommand(commandID, func() ([]Snapshot, error) {
			return collector.GetSnapshots(ctx)
		})
	}

	// 窗口回填能力在此统一判定：实现了 WindowedProvider 的采集器自动获得
	// Backfill 命令支持，未实现的在 collectWindowed 里退化为当前快照。
	wColl, _ := collector.(WindowedProvider)
	collectWindowed := func(commandID string, start, end time.Time) {
		executeCommand(commandID, func() ([]Snapshot, error) {
			if wColl == nil {
				log.Warn().Msg("collector does not implement WindowedProvider; falling back to current snapshot")
				return collector.GetSnapshots(ctx)
			}
			snaps, err := wColl.GetSnapshotsForWindow(ctx, start, end)
			log.Info().
				Str("command_id", commandID).
				Time("window_start", start).
				Time("window_end", end).
				Int("count", len(snaps)).
				Msg("backfill window collected")
			return snaps, err
		})
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

	// Stream heartbeat loop — keeps Core's health view alive between pushes.
	// 20s < Core's 60s healthTimeout; hourly collectors depend on this to stay
	// "online" (and un-penalized in quality scoring) between collections.
	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runner.SubmitHeartbeat(pluginID, l.heartbeatStatus(collector))
			}
		}
	}()

	return runner.Run(ctx, pluginID)
}

func (l *Lifecycle) executeCollection(commandID string, collect func() ([]Snapshot, error)) commandExecutionResult {
	return l.commands.execute(commandID, func() ([]Snapshot, error) {
		return l.status.execute(collect)
	})
}

// submitAndAck converts snapshots to proto, submits them, and acks the command
// (if any) with the number of collected snapshots.
func submitAndAck(runner *Runner, pluginID, commandID string, result commandExecutionResult) {
	if !submitSnapshots(runner, pluginID, commandID, result.snapshots) {
		if commandID != "" {
			const message = "snapshot enqueue failed after collection"
			runner.submitCommandAckAt(commandID, "failed", message, 0, message, result.ackTimestamp)
		}
		return
	}
	if commandID != "" {
		runner.submitCommandAckAt(commandID, "success",
			fmt.Sprintf("%d snapshots collected", len(result.snapshots)),
			int32(len(result.snapshots)), "", result.ackTimestamp)
	}
}

func submitSnapshots(runner *Runner, pluginID, commandID string, snapshots []Snapshot) bool {
	protoSnaps := SnapshotsToProto(snapshots, runner.version)
	if err := runner.trySubmitSnapshots(pluginID, protoSnaps); err != nil {
		log.Warn().Err(err).
			Str("command_id", commandID).
			Int("count", len(protoSnaps)).
			Msg("snapshot enqueue failed")
		return false
	}
	log.Info().
		Str("command_id", commandID).
		Int("count", len(protoSnaps)).
		Msg("snapshots submitted")
	return true
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

// CircuitStateProvider is optionally implemented by collectors that expose
// an HTTP circuit breaker state for operational health reporting.
type CircuitStateProvider interface {
	CircuitState() string
}

// heartbeatStatus returns a race-free collection outcome merged with optional
// collector runtime metadata.
func (l *Lifecycle) heartbeatStatus(collector Provider) *pb.PluginStatus {
	runtime := make(map[string]string)
	if csp, ok := collector.(CircuitStateProvider); ok {
		runtime["circuit_state"] = csp.CircuitState()
	}
	return l.status.snapshot(runtime)
}
