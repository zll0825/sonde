// Package pluginmgr 实现 Plugin ↔ Core 双向 gRPC 流的 Core 侧逻辑，含
// ADR-6 规定的单写者（single-writer）发送循环、注册评审入口、快照摄入
// 管道（pipeline.go）与命令派发。
package pluginmgr

import (
	"context"
	"errors"
	"io"
	"sync"

	"github.com/rs/zerolog/log"

	pb "capital_observatory/pkg/proto/plugin/v1"
)

// StreamSession holds the state for one connected Plugin.
//
// Concurrency model (ADR-6):
//   - Exactly ONE goroutine calls stream.Send(): the writer loop started by StartWriter().
//   - Everyone else calls SendAsync() which enqueues into sendCh.
//   - sendCh is buffered (size 64) to absorb short bursts.
type StreamSession struct {
	pluginID string
	version  int

	stream pb.PluginHost_MaintainSessionServer
	sendCh chan *pb.CoreMessage

	ctx    context.Context
	cancel context.CancelFunc

	closeOnce sync.Once
	closed    chan struct{}
}

// NewStreamSession creates a session for the given plugin.
func NewStreamSession(pluginID string, version int, stream pb.PluginHost_MaintainSessionServer) *StreamSession {
	ctx, cancel := context.WithCancel(stream.Context())
	return &StreamSession{
		pluginID: pluginID,
		version:  version,
		stream:   stream,
		sendCh:   make(chan *pb.CoreMessage, 64),
		ctx:      ctx,
		cancel:   cancel,
		closed:   make(chan struct{}),
	}
}

// StartWriter runs the single writer goroutine.
// It blocks until the stream errors out or the session is closed.
func (s *StreamSession) StartWriter() {
	for {
		select {
		case msg := <-s.sendCh:
			if err := s.stream.Send(msg); err != nil {
				s.cancel()
				return
			}
		case <-s.ctx.Done():
			return
		}
	}
}

// SendAsync enqueues a message to be sent to the plugin.
// Non-blocking: returns an error if the session is closed or the buffer is full.
// NEVER call stream.Send() directly outside of StartWriter().
func (s *StreamSession) SendAsync(msg *pb.CoreMessage) error {
	select {
	case s.sendCh <- msg:
		return nil
	case <-s.ctx.Done():
		return errors.New("session closed")
	default:
		return errors.New("session send buffer full")
	}
}

// Close shuts down the writer loop and cancels the context.
func (s *StreamSession) Close() {
	s.closeOnce.Do(func() {
		s.cancel()
		close(s.closed)
	})
}

// Done returns a channel that's closed when the session has been closed.
func (s *StreamSession) Done() <-chan struct{} {
	return s.closed
}

// PluginID returns the plugin identifier.
func (s *StreamSession) PluginID() string {
	return s.pluginID
}

// ReadLoop is the Core-side reader for one session.
// It dispatches incoming PluginMessage payloads to the manager until the stream ends.
func (s *StreamSession) ReadLoop(onMessage func(*pb.PluginMessage)) {
	for {
		msg, err := s.stream.Recv()
		if err != nil {
			if err != io.EOF && !errors.Is(err, context.Canceled) {
				log.Warn().Err(err).Str("plugin_id", s.pluginID).Msg("stream read error")
			}
			s.Close()
			return
		}
		onMessage(msg)
	}
}
