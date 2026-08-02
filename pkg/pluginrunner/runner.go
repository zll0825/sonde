// Package pluginrunner provides the plugin-side stream session template.
//
// High-level entry: a plugin's main.go constructs a Config and calls
// NewLifecycle(cfg).Run(); that drives the full reconnect→register→collect→
// stream cycle.  Lower-level Runner / Register / Run are exposed for tests and
// for the rare plugin that needs to manage the session manually.
//
// Usage:
//
//	pluginrunner.NewLifecycle(pluginrunner.Config{
//	    PluginName: "crypto", Version: "0.1.0", DefaultInterval: 10 * time.Second,
//	    BuildRegistration: buildRegistration, SetupCollector: setupCollector,
//	}).Run()
package pluginrunner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/rs/zerolog/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/metadata"

	pb "capital_observatory/pkg/proto/plugin/v1"
)

// Runner manages a plugin's bidirectional stream session with Core.
//
// Concurrency model:
//   - Exactly ONE goroutine writes to stream.Send(): the internal run loop.
//   - PushSnapshots batches are submitted via SubmitSnapshots() which enqueues to sendCh.
//   - Core messages are dispatched via the onCoreMessage callback.
type Runner struct {
	pluginName string
	version    string
	client     pb.PluginHostClient

	stream      pb.PluginHost_MaintainSessionClient
	sendCh      chan *pb.PluginMessage
	ctx         context.Context
	cancel      context.CancelFunc
	onCoreFuncs []func(*pb.CoreMessage)
}

// New creates a new Runner that will use the given gRPC client to connect to Core.
func New(pluginName, version string, client pb.PluginHostClient) *Runner {
	return &Runner{
		pluginName: pluginName,
		version:    version,
		client:     client,
		sendCh:     make(chan *pb.PluginMessage, 64),
	}
}

// Register sends the initial registration unary call.
// Returns the assigned plugin_id.
func (r *Runner) Register(ctx context.Context, req *pb.RegisterPluginRequest) (string, error) {
	resp, err := r.client.RegisterPlugin(ctx, req)
	if err != nil {
		return "", err
	}
	if !resp.Success {
		return "", errRegistrationFailed(resp.Message)
	}
	return resp.PluginId, nil
}

// Run opens the bidirectional stream and starts the read loop.
// pluginID is sent via metadata so Core can identify the caller. When
// $CORE_STREAM_TOKEN is set (shared secret with Core), it is attached as
// x-plugin-token — Core rejects token-less streams when it has one configured.
func (r *Runner) Run(ctx context.Context, pluginID string) error {
	r.ctx, r.cancel = context.WithCancel(ctx)

	// Attach plugin ID (+ optional stream token) metadata for identification.
	mdPairs := map[string]string{"x-plugin-id": pluginID}
	if token := os.Getenv("CORE_STREAM_TOKEN"); token != "" {
		mdPairs["x-plugin-token"] = token
	}
	md := metadata.New(mdPairs)
	ctx = metadata.NewOutgoingContext(r.ctx, md)

	var err error
	r.stream, err = r.client.MaintainSession(ctx)
	if err != nil {
		return err
	}

	errCh := make(chan error, 2)

	// Writer loop — exclusive stream.Send() caller.
	go func() {
		for {
			select {
			case msg := <-r.sendCh:
				if err := r.stream.Send(msg); err != nil {
					errCh <- err
					return
				}
			case <-r.ctx.Done():
				return
			}
		}
	}()

	// Reader loop — dispatches CoreMessages.
	go func() {
		for {
			msg, err := r.stream.Recv()
			if err != nil {
				if err != io.EOF {
					errCh <- err
				}
				return
			}
			for _, fn := range r.onCoreFuncs {
				fn(msg)
			}
		}
	}()

	// Block until context is canceled or stream errors.
	select {
	case <-r.ctx.Done():
	case <-errCh:
		r.cancel()
	}

	return nil
}

// SubmitSnapshots enqueues a PushSnapshots message to the send channel.
// Callers use this instead of calling stream.Send directly.
// Safe to call before Run: the batch is dropped with a warning rather than
// dereferencing the not-yet-initialized session context.
func (r *Runner) SubmitSnapshots(pluginID string, snapshots []*pb.MetricSnapshot) {
	if r.ctx == nil {
		log.Warn().Str("plugin", r.pluginName).Msg("runner not started, dropping push")
		return
	}
	select {
	case r.sendCh <- &pb.PluginMessage{
		Payload: &pb.PluginMessage_PushSnapshots{
			PushSnapshots: &pb.PushSnapshotsRequest{
				PluginId:  pluginID,
				Snapshots: snapshots,
			},
		},
	}:
	case <-r.ctx.Done():
		log.Warn().Str("plugin", r.pluginName).Msg("runner closed, dropping push")
	default:
		log.Warn().Str("plugin", r.pluginName).Msg("sendCh full, dropping push")
	}
}

// OnCoreMessage registers a handler for inbound CoreMessages.
func (r *Runner) OnCoreMessage(fn func(*pb.CoreMessage)) {
	r.onCoreFuncs = append(r.onCoreFuncs, fn)
}

// SubmitCommandAck sends a CommandAck back to Core in response to a
// SyncCommand or BackfillCommand. The count of newly-observed samples
// should match what Core's ingester reports via PushAck.
func (r *Runner) SubmitCommandAck(commandID, status, message string, collectedCount int32, errMsg string) {
	if r.ctx == nil {
		log.Warn().Str("command_id", commandID).Msg("runner not started, dropping command ack")
		return
	}
	select {
	case r.sendCh <- &pb.PluginMessage{
		Payload: &pb.PluginMessage_CommandAck{
			CommandAck: &pb.CommandAck{
				CommandId:      commandID,
				Status:         status,
				Message:        message,
				Timestamp:      time.Now().Unix(),
				CollectedCount: collectedCount,
				Error:          errMsg,
			},
		},
	}:
	case <-r.ctx.Done():
		log.Warn().Str("command_id", commandID).Msg("runner closed, dropping command ack")
	default:
		log.Warn().Str("command_id", commandID).Msg("sendCh full, dropping command ack")
	}
}

// Close cancels the session context, terminating the stream.
func (r *Runner) Close() {
	if r.cancel != nil {
		r.cancel()
	}
}

// WaitForReconnect blocks until the gRPC connection is READY.
// Plugins should call this before retrying after a stream error.
func WaitForReconnect(ctx context.Context, conn *grpc.ClientConn) bool {
	for {
		state := conn.GetState()
		if state == connectivity.Ready {
			return true
		}
		if !conn.WaitForStateChange(ctx, state) {
			return false
		}
	}
}

func errRegistrationFailed(msg string) error {
	if msg == "" {
		return errors.New("registration failed")
	}
	return fmt.Errorf("registration failed: %s", msg)
}
