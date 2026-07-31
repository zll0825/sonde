package pluginmgr

import (
	"context"

	"github.com/rs/zerolog/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	pb "capital_observatory/pkg/proto/plugin/v1"
)

const (
	// pluginIDKey is the gRPC metadata key for plugin identification on the stream.
	pluginIDKey = "x-plugin-id"
)

// Handler implements the PluginHost gRPC service (Core side).
type Handler struct {
	pb.UnimplementedPluginHostServer
	manager *Manager
}

// NewHandler creates a gRPC handler backed by the given plugin Manager.
func NewHandler(manager *Manager) *Handler {
	return &Handler{manager: manager}
}

// RegisterPlugin handles unary registration requests.
// On success returns the assigned plugin_id and registration_version.
func (h *Handler) RegisterPlugin(ctx context.Context, req *pb.RegisterPluginRequest) (*pb.RegisterPluginResponse, error) {
	if req.Info == nil {
		return nil, status.Error(codes.InvalidArgument, "plugin info required")
	}

	pluginID, version, err := h.manager.HandleRegistration(ctx, req)
	if err != nil {
		log.Error().Err(err).Str("plugin", req.Info.GetName()).Msg("registration failed")
		return nil, status.Error(codes.Internal, "registration failed")
	}

	log.Info().
		Str("plugin_id", pluginID).
		Int("version", version).
		Str("plugin", req.Info.GetName()).
		Msg("plugin registered")

	return &pb.RegisterPluginResponse{
		PluginId:            pluginID,
		RegistrationVersion: int32(version),
		Success:             true,
		Message:             "registered successfully",
	}, nil
}

// MaintainSession handles the bi-directional stream.
// The plugin is identified via x-plugin-id metadata set after registration.
func (h *Handler) MaintainSession(stream pb.PluginHost_MaintainSessionServer) error {
	// Extract plugin ID from metadata.
	md, ok := metadata.FromIncomingContext(stream.Context())
	if !ok {
		return status.Error(codes.Unauthenticated, "missing metadata")
	}
	ids := md.Get(pluginIDKey)
	if len(ids) == 0 {
		return status.Error(codes.Unauthenticated, "missing plugin ID metadata")
	}
	pluginID := ids[0]

	session := NewStreamSession(pluginID, 0, stream)
	h.manager.RegisterSession(session)
	defer h.manager.UnregisterSession(pluginID)

	log.Info().Str("plugin_id", pluginID).Msg("session established")

	// Run read loop — dispatches incoming messages until stream closes.
	session.ReadLoop(func(msg *pb.PluginMessage) {
		if err := h.manager.HandlePluginMessage(msg); err != nil {
			log.Error().Err(err).Str("plugin_id", pluginID).Msg("handle message failed")
		}
	})

	log.Info().Str("plugin_id", pluginID).Msg("session closed")
	return nil
}

// Heartbeat handles unary heartbeat requests from plugins.
func (h *Handler) Heartbeat(ctx context.Context, req *pb.HeartbeatRequest) (*pb.HeartbeatResponse, error) {
	// M1: accept all heartbeats; plugin status tracking comes in M2.
	log.Debug().
		Str("plugin_id", req.PluginId).
		Str("state", req.Status.GetState()).
		Msg("heartbeat received")

	return &pb.HeartbeatResponse{
		Ok:                true,
		HasPendingCommand: false,
	}, nil
}

// _ ensures the handler implements the full server interface.
var _ pb.PluginHostServer = (*Handler)(nil)
var _ = (*grpc.Server)(nil)
