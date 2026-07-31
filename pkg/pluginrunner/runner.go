// Package pluginrunner provides the plugin-side stream session template.
// Plugins embed Runner and override the collect hook to implement data fetching.
//
// Usage:
//
//	conn, _ := grpc.Dial("core:50051", grpc.WithInsecure())
//	client := pb.NewPluginHostClient(conn)
//	r := pluginrunner.New("crypto", client)
//	reg := &pb.RegisterPluginRequest{Info: &pb.PluginInfo{Name: "crypto"}, ...}
//	pluginID, _ := r.Register(ctx, reg)
//	r.Run(ctx, pluginID)
package pluginrunner

import (
	"context"
	"io"
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
	client     pb.PluginHostClient

	stream      pb.PluginHost_MaintainSessionClient
	sendCh      chan *pb.PluginMessage
	ctx         context.Context
	cancel      context.CancelFunc
	onCoreFuncs []func(*pb.CoreMessage)
}

// New creates a new Runner that will use the given gRPC client to connect to Core.
func New(pluginName string, client pb.PluginHostClient) *Runner {
	return &Runner{
		pluginName: pluginName,
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
// pluginID is sent via metadata so Core can identify the caller.
func (r *Runner) Run(ctx context.Context, pluginID string) error {
	r.ctx, r.cancel = context.WithCancel(ctx)

	// Attach plugin ID metadata for stream identification.
	md := metadata.New(map[string]string{"x-plugin-id": pluginID})
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
func (r *Runner) SubmitSnapshots(pluginID string, snapshots []*pb.MetricSnapshot) {
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

// CollectLoop is an example periodic collection that submits empty batches.
// Real plugins override this.
func (r *Runner) CollectLoop(ctx context.Context, interval time.Duration, pluginID string) chan struct{} {
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				r.SubmitSnapshots(pluginID, nil)
			case <-ctx.Done():
				close(done)
				return
			}
		}
	}()
	return done
}

func errRegistrationFailed(msg string) error {
	if msg == "" {
		return _errRegistrationFailed
	}
	return registrationError{msg: msg}
}

var _errRegistrationFailed = fmtError("registration failed")

type registrationError struct{ msg string }

func (e registrationError) Error() string { return "registration failed: " + e.msg }

func fmtError(s string) error { return &simpleError{s} }

type simpleError struct{ s string }

func (e *simpleError) Error() string { return e.s }
