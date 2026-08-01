package pluginmgr

import (
	"context"
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
	pb "capital_observatory/pkg/proto/plugin/v1"
)

// Manager holds the registry of active plugin sessions and routes
// registration / push events to the persistence layer and the M3/M4 pipeline.
type Manager struct {
	mu       sync.RWMutex
	sessions map[string]*StreamSession // pluginID → session
	store    *ontology.Store
	ingester *metric.Ingester
	pipeline *Pipeline

	// detectorEngine is here so dynamic registration of new Detector types
	// (e.g. percentile, trend) can be added after construction.
	detectorEngine *detector.Engine
	alertEngine    *alert.Engine
	researchAsm    *research.Assembler

	db metric.DB
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
) *Manager {
	m := &Manager{
		sessions:       make(map[string]*StreamSession),
		store:          store,
		db:             db,
		detectorEngine: detEngine,
		alertEngine:    alertEng,
		researchAsm:    researchAsmer,
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
	go session.StartWriter()
}

// UnregisterSession removes a plugin session from the registry.
func (m *Manager) UnregisterSession(pluginID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, pluginID)
}

// GetSession returns the active session for a plugin, or nil if not connected.
func (m *Manager) GetSession(pluginID string) *StreamSession {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessions[pluginID]
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
func (m *Manager) HandlePluginMessage(msg *pb.PluginMessage) error {
	switch msg.Payload.(type) {
	case *pb.PluginMessage_PushSnapshots:
		return m.handlePushSnapshots(msg.GetPushSnapshots())
	case *pb.PluginMessage_Heartbeat:
		log.Debug().Msg("stream heartbeat received")
		return nil
	case *pb.PluginMessage_CommandAck:
		log.Debug().Msg("command ack received")
		return nil
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
func (m *Manager) handlePushSnapshots(ps *pb.PushSnapshotsRequest) error {
	isHealthy := m.isPluginHealthy(ps.PluginId)
	result, err := m.ingester.Ingest(context.Background(), ps, isHealthy)
	if err != nil {
		log.Error().Err(err).Str("plugin_id", ps.PluginId).Msg("ingestion failed")
		return err
	}

	log.Info().
		Str("plugin_id", ps.PluginId).
		Int("inserted", result.Inserted).
		Int("rejected", result.Rejected).
		Int("pending", len(result.PendingSeen)).
		Msg("push ingested")

	// M3/M4: evaluate rules for every metric_id that produced new observations.
	// Dedup the metric_ids so we don't re-evaluate for duplicates.
	seen := make(map[string]struct{})
	for _, snap := range ps.Snapshots {
		if _, ok := seen[snap.MetricId]; ok {
			continue
		}
		seen[snap.MetricId] = struct{}{}

		// Fire-and-forget evaluation: evaluation failures must not break the
		// ingestion acknowledgement. Errors are logged inside the pipeline.
		go func(metricID, pluginID string) {
			evalCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_ = m.pipeline.EvaluateAndAlert(evalCtx, metricID, pluginID)
		}(snap.MetricId, ps.PluginId)
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

func (m *Manager) isPluginHealthy(_ string) bool {
	// M2/M3 placeholder: real plugin status comes from heartbeat polling.
	return true
}

// ErrPluginNotConnected is returned when a SyncCommand targets a disconnected plugin.
var ErrPluginNotConnected = status.Error(codes.FailedPrecondition, "plugin not connected")
