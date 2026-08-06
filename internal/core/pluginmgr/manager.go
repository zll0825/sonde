package pluginmgr

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"capital_observatory/internal/core/alert"
	"capital_observatory/internal/core/detector"
	"capital_observatory/internal/core/metric"
	"capital_observatory/internal/core/ontology"
	"capital_observatory/internal/core/research"
	"capital_observatory/internal/core/store"
	pb "capital_observatory/pkg/proto/plugin/v1"
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
	researchAsm    *research.Assembler

	db metric.DB

	healthTimeout time.Duration // max time since last heartbeat before "unhealthy"
}

// PluginHealth tracks the last-seen timestamp for a plugin.
// A plugin is healthy if it has an active session AND its last activity
// (stream heartbeat or data push) was within Manager.healthTimeout.
type PluginHealth struct {
	lastActivity time.Time // last heartbeat or push
	mu           sync.Mutex
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

// persistHeartbeat mirrors the in-memory health signal into plugins.healthy /
// last_heartbeat so read-only consumers (/api/status) can see liveness.
// Best-effort: a failed write is logged, never propagated — heartbeats must
// stay cheap and infallible from the plugin's point of view.
func (m *Manager) persistHeartbeat(ctx context.Context, pluginID string) {
	if m.store == nil {
		return
	}
	if err := m.store.TouchHeartbeat(ctx, pluginID); err != nil {
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
	researchAsmer *research.Assembler,
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
		researchAsm:    researchAsmer,
		healthTimeout:  60 * time.Second,
	}
	m.ingester = metric.NewIngester(store, db)
	m.pipeline = NewPipeline(store, obsQuerier, researchAsmer, detEngine, alertEng)
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
// Success → MarkCompleted, failure → MarkFailed. Used by HandlePluginMessage
// to close the control-plane loop started by CommandDispatcher.dispatch.
func (m *Manager) handleCommandAck(ctx context.Context, ack *pb.CommandAck) error {
	if ack == nil {
		return nil
	}
	log.Info().
		Str("command_id", ack.CommandId).
		Str("status", ack.Status).
		Int32("collected", ack.CollectedCount).
		Msg("command ack received")

	// Success requires no error AND a non-failure status. Judging by
	// `status=="success" || error==""` would mark a failed ack with an empty
	// error message as completed.
	isSuccess := ack.Error == "" && (ack.Status == "" || ack.Status == "success")
	if isSuccess {
		return m.commandStore.MarkCompleted(ctx, ack.CommandId, int(ack.CollectedCount))
	}
	errMsg := ack.Error
	if errMsg == "" {
		errMsg = "plugin reported status " + ack.Status
	}
	return m.commandStore.MarkFailed(ctx, ack.CommandId, errMsg)
}

// StartCommandDispatcher begins a background goroutine that polls the
// command_log for pending commands and routes each to the target plugin's
// stream via SendAsync.
//
// Loop:
//  1. Poll commandStore.GetPendingCommands (blocked by 1s tick).
//  2. For each command, find the active session for target_plugin.
//  3. Send the corresponding CoreMessage (SyncCommand / BackfillCommand).
//  4. Mark the row 'dispatched' so the next poll won't re-emit it.
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

// dispatchOnce sends one round of pending commands to their target plugins.
func (m *Manager) dispatchOnce(ctx context.Context) error {
	pending, err := m.commandStore.GetPendingCommands(ctx)
	if err != nil {
		return fmt.Errorf("get pending commands: %w", err)
	}

	for _, cmd := range pending {
		m.mu.RLock()
		session := m.sessions[cmd.TargetPlugin]
		m.mu.RUnlock()

		if session == nil {
			// Debug, not warn: an offline target plugin is a normal transient
			// state and this fires on every poll tick until it reconnects.
			log.Debug().
				Str("command_id", cmd.CommandID).
				Str("plugin", cmd.TargetPlugin).
				Msg("no connected session for pending command — will retry on next tick")
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
			if ferr := m.commandStore.MarkFailed(ctx, cmd.CommandID, "unknown command type: "+string(cmd.CommandType)); ferr != nil {
				log.Error().Err(ferr).Str("command_id", cmd.CommandID).Msg("failed to mark unknown command failed")
			}
			continue
		}
		if serr := session.SendAsync(coreMsg); serr != nil {
			log.Error().Err(serr).
				Str("command_id", cmd.CommandID).
				Str("plugin", cmd.TargetPlugin).
				Msg("failed to send command to plugin")
			continue
		}

		// Mark dispatched so the next poll skips this command.
		if derr := m.commandStore.MarkDispatched(ctx, cmd.CommandID); derr != nil {
			log.Error().Err(derr).
				Str("command_id", cmd.CommandID).
				Msg("failed to mark command dispatched")
		}

		log.Info().
			Str("command_id", cmd.CommandID).
			Str("type", string(cmd.CommandType)).
			Str("plugin", cmd.TargetPlugin).
			Str("endpoint", cmdToStreamEndpoint(coreMsg)).
			Msg("command dispatched to plugin")
	}

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
		m.markHealthActivity(msg.GetHeartbeat().GetPluginId())
		m.persistHeartbeat(ctx, msg.GetHeartbeat().GetPluginId())
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

	// Persist collect stats so the dashboard's status bar reflects reality.
	// Non-fatal: a failed bookkeeping write must not reject the push.
	if m.store != nil {
		if err := m.store.RecordCollect(ctx, ps.PluginId, result.Inserted); err != nil {
			log.Warn().Err(err).Str("plugin_id", ps.PluginId).Msg("record collect failed")
		}
	}

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
	health.mu.Unlock()
	return time.Since(last) <= timeout
}

// ErrPluginNotConnected is returned when a SyncCommand targets a disconnected plugin.
var ErrPluginNotConnected = status.Error(codes.FailedPrecondition, "plugin not connected")
