package pluginmgr

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"sonde/internal/core/alert"
	"sonde/internal/core/detector"
	"sonde/internal/core/metric"
	"sonde/internal/core/ontology"
	"sonde/internal/core/store"
	pb "sonde/pkg/proto/plugin/v1"
)

// Manager holds the registry of active plugin sessions and routes
// registration / push events to the persistence layer and the M3/M4 pipeline.
type Manager struct {
	mu       sync.RWMutex
	sessions map[string]*StreamSession // pluginID → session
	health   map[string]*PluginHealth  // pluginID → health state
	store    *ontology.Store
	ingester *metric.Ingester
	pipeline *Pipeline

	// commandStore is the control-plane store. The CommandDispatcher polls it
	// and dispatches pending Sync/Backfill commands to connected plugins.
	commandStore store.CommandStore

	// detectorEngine is here so dynamic registration of new Detector types
	// (e.g. percentile, trend) can be added after construction.
	detectorEngine *detector.Engine
	alertEngine    *alert.Engine

	db metric.DB

	healthTimeout time.Duration // max time since last heartbeat before "unhealthy"
}

// PluginHealth tracks the last-seen timestamp for a plugin.
// A plugin is healthy if it has an active session AND its last activity
// (stream heartbeat or data push) was within Manager.healthTimeout.
type PluginHealth struct {
	lastActivity      time.Time // last heartbeat or push
	collectionKnown   bool
	collectionHealthy bool
	mu                sync.Mutex
}

// markHealthActivity updates the last-seen timestamp for a plugin.
// The health map itself is guarded by m.mu (Register/Unregister add and
// remove keys concurrently); the per-plugin timestamp by its own mutex.
func (m *Manager) markHealthActivity(pluginID string) {
	m.mu.RLock()
	h, ok := m.health[pluginID]
	m.mu.RUnlock()
	if !ok {
		return
	}
	h.mu.Lock()
	h.lastActivity = time.Now()
	h.mu.Unlock()
}

// markCollectionHealth updates the in-memory reputation signal only when the
// heartbeat contains a recorded collection outcome. Legacy and runtime-only
// heartbeats preserve the prior outcome, matching durable persistence.
func (m *Manager) markCollectionHealth(pluginID string, status *pb.PluginStatus) {
	if !hasCollectionStatus(status) {
		return
	}
	m.mu.RLock()
	h, ok := m.health[pluginID]
	m.mu.RUnlock()
	if !ok {
		return
	}
	h.mu.Lock()
	h.collectionKnown = true
	h.collectionHealthy = status.GetLastCollectError() == ""
	h.mu.Unlock()
}

func hasCollectionStatus(status *pb.PluginStatus) bool {
	return status != nil && (status.GetLastCollectAt() != 0 ||
		status.GetLastCollectDurationMs() != 0 || status.GetLastCollectCount() != 0 ||
		status.GetLastCollectError() != "" || status.GetConsecutiveErrors() != 0)
}

// persistHeartbeat mirrors the in-memory health signal into plugins.healthy /
// last_heartbeat so read-only consumers (/api/status) can see liveness.
// Best-effort: a failed write is logged, never propagated — heartbeats must
// stay cheap and infallible from the plugin's point of view.
func (m *Manager) persistHeartbeat(ctx context.Context, pluginID string, status *pb.PluginStatus) {
	if m.store == nil {
		return
	}
	if err := m.store.RecordHeartbeat(ctx, pluginID, status); err != nil {
		log.Warn().Err(err).Str("plugin_id", pluginID).Msg("persist heartbeat failed")
	}
}

// NewManager creates a plugin manager backed by the given store and the
// full set of M3/M4 evaluation components. db is shared with the ingester,
// resolver, pending tracker, and observation querier.
func NewManager(
	store *ontology.Store,
	db metric.DB,
	detEngine *detector.Engine,
	alertEng *alert.Engine,
	obsQuerier ObservationQuerier,
	cmdStore store.CommandStore,
) *Manager {
	m := &Manager{
		sessions:       make(map[string]*StreamSession),
		health:         make(map[string]*PluginHealth),
		store:          store,
		commandStore:   cmdStore,
		db:             db,
		detectorEngine: detEngine,
		alertEngine:    alertEng,
		healthTimeout:  60 * time.Second,
	}
	m.ingester = metric.NewIngester(store, db)
	m.pipeline = NewPipeline(store, obsQuerier, detEngine, alertEng)
	return m
}

// RegisterSession holds a new stream session for a plugin.
// If the plugin already has an active session, the old one is closed (single-connection guarantee).
func (m *Manager) RegisterSession(session *StreamSession) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if old, ok := m.sessions[session.pluginID]; ok {
		log.Warn().Str("plugin_id", session.pluginID).Msg("replacing existing session")
		old.Close()
	}
	m.sessions[session.pluginID] = session
	m.health[session.pluginID] = &PluginHealth{lastActivity: time.Now()}
	go session.StartWriter()
}

// UnregisterSession removes a plugin session from the registry, but only if
// the registered session is still the given one. Without this guard, a stale
// stream handler returning after a reconnect would delete the replacement
// session that RegisterSession just installed.
func (m *Manager) UnregisterSession(pluginID string, session *StreamSession) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions[pluginID] == session {
		delete(m.sessions, pluginID)
		delete(m.health, pluginID)
	}
}

// GetSession returns the active session for a plugin, or nil if not connected.
func (m *Manager) GetSession(pluginID string) *StreamSession {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessions[pluginID]
}

// handleCommandAck updates command_log based on a plugin's CommandAck.
// Success completes authoritatively, progress renews the active lease, and
// failure releases the current attempt for bounded retry.
func (m *Manager) handleCommandAck(ctx context.Context, ack *pb.CommandAck) error {
	if ack == nil {
		return nil
	}
	log.Info().
		Str("command_id", ack.CommandId).
		Str("status", ack.Status).
		Int32("collected", ack.CollectedCount).
		Msg("command ack received")

	if ack.Error != "" {
		return m.handleCommandAttemptFailure(ctx, ack.CommandId, ack.Error)
	}

	switch ack.Status {
	case "", "success", "completed":
		return m.commandStore.MarkCompleted(ctx, ack.CommandId, int(ack.CollectedCount))
	case "accepted", "running":
		return m.commandStore.RenewLease(ctx, ack.CommandId, store.CommandLeaseDuration)
	case "failed":
		errMsg := ack.Message
		if errMsg == "" {
			errMsg = "plugin reported failed status"
		}
		return m.handleCommandAttemptFailure(ctx, ack.CommandId, errMsg)
	default:
		return m.handleCommandAttemptFailure(ctx, ack.CommandId, "plugin reported status "+ack.Status)
	}
}

func (m *Manager) handleCommandAttemptFailure(ctx context.Context, commandID, errMsg string) error {
	status, err := m.commandStore.MarkAttemptFailed(ctx, commandID, errMsg)
	if errors.Is(err, store.ErrCommandNotDispatched) {
		log.Debug().Str("command_id", commandID).Msg("duplicate command failure ack ignored")
		return nil
	}
	if err != nil {
		return err
	}
	logEvent := log.Warn()
	if status == store.CmdStatusFailed {
		logEvent = log.Error()
	}
	logEvent.
		Str("command_id", commandID).
		Str("status", string(status)).
		Str("error", errMsg).
		Msg("command attempt failed")
	return nil
}

// StartCommandDispatcher begins a background goroutine that polls the
// command_log for dispatchable commands and routes each claimed lease to its
// target plugin's stream via SendAsync.
//
// Loop:
//  1. Snapshot the currently connected plugin sessions.
//  2. Atomically claim a bounded batch only for those plugin IDs.
//  3. Enqueue the corresponding CoreMessage (SyncCommand / BackfillCommand).
//  4. Release enqueue failures immediately; otherwise wait for ACK or expiry.
//
// When the plugin responds with CommandAck, handleCommandAck transitions
// the row to 'completed' or 'failed' — closing the control-plane loop.
func (m *Manager) StartCommandDispatcher(ctx context.Context, tickInterval time.Duration) {
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info().Msg("CommandDispatcher shutting down")
			return
		case <-ticker.C:
			if err := m.dispatchOnce(ctx); err != nil {
				log.Error().Err(err).Msg("CommandDispatcher dispatch error")
			}
		}
	}
}

// dispatchOnce atomically claims one bounded round before enqueueing commands.
func (m *Manager) dispatchOnce(ctx context.Context) error {
	m.mu.RLock()
	sessions := make(map[string]*StreamSession, len(m.sessions))
	pluginIDs := make([]string, 0, len(m.sessions))
	for pluginID, session := range m.sessions {
		sessions[pluginID] = session
		pluginIDs = append(pluginIDs, pluginID)
	}
	m.mu.RUnlock()

	claimed, err := m.commandStore.ClaimDispatchable(
		ctx,
		pluginIDs,
		store.CommandDispatchBatch,
		store.CommandLeaseDuration,
	)
	if err != nil {
		return fmt.Errorf("claim dispatchable commands: %w", err)
	}

	var dispatchErr error
	for _, cmd := range claimed {
		session := sessions[cmd.TargetPlugin]
		if session == nil {
			if err := m.releaseClaim(ctx, cmd, "plugin session disappeared after claim"); err != nil {
				dispatchErr = errors.Join(dispatchErr, err)
			}
			continue
		}

		coreMsg := commandToCoreMessage(cmd)
		if coreMsg == nil {
			// Unknown command type can never dispatch — fail it instead of
			// retrying forever (and never hand a nil message to the stream).
			log.Error().
				Str("command_id", cmd.CommandID).
				Str("type", string(cmd.CommandType)).
				Msg("unknown command type, marking failed")
			if ferr := m.commandStore.MarkTerminalFailed(ctx, cmd.CommandID, "unknown command type: "+string(cmd.CommandType)); ferr != nil {
				log.Error().Err(ferr).Str("command_id", cmd.CommandID).Msg("failed to mark unknown command failed")
			}
			continue
		}
		if serr := session.SendAsync(coreMsg); serr != nil {
			if err := m.releaseClaim(ctx, cmd, "stream enqueue failed: "+serr.Error()); err != nil {
				dispatchErr = errors.Join(dispatchErr, err)
			}
			continue
		}

		logEvent := log.Info()
		if cmd.Attempts > 1 {
			logEvent = log.Warn()
		}
		logEvent.
			Str("command_id", cmd.CommandID).
			Str("type", string(cmd.CommandType)).
			Str("plugin_id", cmd.TargetPlugin).
			Int("attempts", cmd.Attempts).
			Str("endpoint", cmdToStreamEndpoint(coreMsg)).
			Msg("command dispatched to plugin")
	}

	return dispatchErr
}

func (m *Manager) releaseClaim(ctx context.Context, cmd store.Command, reason string) error {
	status, err := m.commandStore.MarkAttemptFailed(ctx, cmd.CommandID, reason)
	if errors.Is(err, store.ErrCommandNotDispatched) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("release command %s: %w", cmd.CommandID, err)
	}
	logEvent := log.Warn()
	if status == store.CmdStatusFailed {
		logEvent = log.Error()
	}
	logEvent.
		Str("command_id", cmd.CommandID).
		Str("plugin_id", cmd.TargetPlugin).
		Int("attempts", cmd.Attempts).
		Str("status", string(status)).
		Str("error", reason).
		Msg("command dispatch attempt released")
	return nil
}

// commandToCoreMessage converts a store.Command to a CoreMessage for the stream.
func commandToCoreMessage(cmd store.Command) *pb.CoreMessage {
	switch cmd.CommandType {
	case store.CmdSync:
		return &pb.CoreMessage{
			Payload: &pb.CoreMessage_Sync{
				Sync: &pb.SyncCommand{
					CommandId: cmd.CommandID,
					Reason:    cmd.Reason,
					MetricIds: cmd.MetricIDs,
				},
			},
		}
	case store.CmdBackfill:
		var ws, we int64
		if cmd.WindowStart != nil {
			ws = cmd.WindowStart.Unix()
		}
		if cmd.WindowEnd != nil {
			we = cmd.WindowEnd.Unix()
		}
		return &pb.CoreMessage{
			Payload: &pb.CoreMessage_Backfill{
				Backfill: &pb.BackfillCommand{
					CommandId:   cmd.CommandID,
					Reason:      cmd.Reason,
					MetricIds:   cmd.MetricIDs,
					WindowStart: ws,
					WindowEnd:   we,
				},
			},
		}
	default:
		return nil
	}
}

// cmdToStreamEndpoint is a tiny helper for structured logging.
func cmdToStreamEndpoint(msg *pb.CoreMessage) string {
	switch msg.Payload.(type) {
	case *pb.CoreMessage_Sync:
		return "Sync"
	case *pb.CoreMessage_Backfill:
		return "Backfill"
	default:
		return "(unknown)"
	}
}

// GetActiveSessions returns a snapshot of all connected plugin sessions.
func (m *Manager) GetActiveSessions() []*StreamSession {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*StreamSession, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, s)
	}
	return out
}

// HandleRegistration processes a RegisterPluginRequest and persists it.
// On success it returns the assigned plugin_id and registration_version.
func (m *Manager) HandleRegistration(ctx context.Context, req *pb.RegisterPluginRequest) (string, int, error) {
	return m.store.RegisterPlugin(ctx, req)
}

// HandlePluginMessage dispatches an inbound PluginMessage.
// Called by the gRPC server's MaintainSession handler when a message arrives.
// ctx should be the stream context so ingestion stops when the session dies.
func (m *Manager) HandlePluginMessage(ctx context.Context, msg *pb.PluginMessage) error {
	switch msg.Payload.(type) {
	case *pb.PluginMessage_PushSnapshots:
		return m.handlePushSnapshots(ctx, msg.GetPushSnapshots())
	case *pb.PluginMessage_Heartbeat:
		heartbeat := msg.GetHeartbeat()
		m.markHealthActivity(heartbeat.GetPluginId())
		m.markCollectionHealth(heartbeat.GetPluginId(), heartbeat.GetStatus())
		m.persistHeartbeat(ctx, heartbeat.GetPluginId(), heartbeat.GetStatus())
		log.Debug().Msg("stream heartbeat received")
		return nil
	case *pb.PluginMessage_CommandAck:
		return m.handleCommandAck(ctx, msg.GetCommandAck())
	default:
		return nil
	}
}

// handlePushSnapshots is the M2 → M3 → M4 entry point.
//
//  1. M2: ingest observations (quality score, source preference, dedup).
//  2. After ingestion, collect distinct metric_ids that were accepted
//     and run the M3/M4 evaluation pipeline against each of them.
//  3. Send PushAck back over the stream.
func (m *Manager) handlePushSnapshots(ctx context.Context, ps *pb.PushSnapshotsRequest) error {
	m.markHealthActivity(ps.PluginId)
	isHealthy := m.isPluginHealthy(ps.PluginId)
	result, err := m.ingester.Ingest(ctx, ps, isHealthy)
	if err != nil {
		log.Error().Err(err).Str("plugin_id", ps.PluginId).Msg("ingestion failed")
		return err
	}

	log.Info().
		Str("plugin_id", ps.PluginId).
		Int("inserted", result.Inserted).
		Int("dedup", result.Duplicates).
		Int("rejected", result.Rejected).
		Int("pending", len(result.PendingSeen)).
		Msg("push ingested")

	// Send PushAck over the stream if the plugin has an active session.
	if session := m.GetSession(ps.PluginId); session != nil {
		_ = session.SendAsync(&pb.CoreMessage{
			Payload: &pb.CoreMessage_PushAck{
				PushAck: &pb.PushAck{
					PluginId:     ps.PluginId,
					Inserted:     int32(result.Inserted),
					Deduplicated: int32(result.Duplicates),
					Rejected:     int32(result.Rejected),
				},
			},
		})
	}
	return nil
}

// EvaluateDetection runs one durable detection work item. The outbox worker
// owns retry and terminal-failure state; returning an error keeps the event
// pending until its retry budget is exhausted.
func (m *Manager) EvaluateDetection(ctx context.Context, metricID, pluginID string) error {
	if metricID == "" || pluginID == "" {
		return fmt.Errorf("detection request requires metric_id and plugin_id")
	}
	return m.pipeline.EvaluateAndAlert(ctx, metricID, pluginID)
}

// isPluginHealthy determines whether a plugin is currently healthy.
// Healthy = active session AND last activity (stream heartbeat or push)
// within Manager.healthTimeout. Missing or stale health entries are
// treated as unhealthy (fail-closed: quality scores penalized).
func (m *Manager) isPluginHealthy(pluginID string) bool {
	m.mu.RLock()
	session, hasSession := m.sessions[pluginID]
	health, hasHealth := m.health[pluginID]
	timeout := m.healthTimeout
	m.mu.RUnlock()

	if !hasSession || session == nil {
		return false
	}
	if !hasHealth {
		return false
	}

	health.mu.Lock()
	last := health.lastActivity
	collectionKnown := health.collectionKnown
	collectionHealthy := health.collectionHealthy
	health.mu.Unlock()
	return time.Since(last) <= timeout && (!collectionKnown || collectionHealthy)
}

// ErrPluginNotConnected is returned when a SyncCommand targets a disconnected plugin.
var ErrPluginNotConnected = status.Error(codes.FailedPrecondition, "plugin not connected")
