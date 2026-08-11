// Package pluginrunner 提供插件侧的通用运行骨架。
//
// 入口：插件的 main.go 构造 Config 并调用 NewLifecycle(cfg).Run()，即获得
// 完整的 重连→注册→采集→推流 循环。底层 Runner / Register / Run 保留导出，
// 供测试或需要手工管理会话的插件使用。
//
// 用法：
//
//	pluginrunner.NewLifecycle(pluginrunner.Config{
//	    PluginName: "crypto", Version: "0.1.0", DefaultInterval: time.Hour,
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
	if err := r.trySubmitSnapshots(pluginID, snapshots); err != nil {
		log.Warn().Err(err).Str("plugin", r.pluginName).Msg("dropping push")
	}
}

func (r *Runner) trySubmitSnapshots(pluginID string, snapshots []*pb.MetricSnapshot) error {
	if r.ctx == nil {
		return errors.New("runner not started")
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
		return nil
	case <-r.ctx.Done():
		return errors.New("runner closed")
	default:
		return errors.New("runner send buffer full")
	}
}

// SubmitHeartbeat enqueues a stream heartbeat so Core can mark the plugin
// healthy between data pushes (hourly collectors would otherwise look offline).
// The optional status carries runtime metadata such as the current circuit
// breaker state. Same drop-don't-block semantics as SubmitSnapshots.
func (r *Runner) SubmitHeartbeat(pluginID string, status *pb.PluginStatus) {
	if r.ctx == nil {
		return
	}
	hb := &pb.HeartbeatRequest{
		PluginId:  pluginID,
		Timestamp: time.Now().Unix(),
	}
	if status != nil {
		hb.Status = status
	}
	select {
	case r.sendCh <- &pb.PluginMessage{
		Payload: &pb.PluginMessage_Heartbeat{
			Heartbeat: hb,
		},
	}:
	case <-r.ctx.Done():
	default:
		log.Warn().Str("plugin", r.pluginName).Msg("sendCh full, dropping heartbeat")
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
	r.submitCommandAckAt(commandID, status, message, collectedCount, errMsg, time.Now().Unix())
}

func (r *Runner) submitCommandAckAt(commandID, status, message string, collectedCount int32, errMsg string, timestamp int64) {
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
				Timestamp:      timestamp,
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
