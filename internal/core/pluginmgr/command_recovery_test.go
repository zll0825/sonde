package pluginmgr

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"sonde/internal/core/store"
	pb "sonde/pkg/proto/plugin/v1"
)

type commandLeaseStore struct {
	claimed        []store.Command
	claimPluginIDs []string
	completed      []string
	completedCount int
	renewed        []string
	attemptFailed  []string
	attemptReason  string
	attemptStatus  store.CommandStatus
	attemptErr     error
	terminal       []string
}

func (f *commandLeaseStore) ClaimDispatchable(_ context.Context, pluginIDs []string, _ int, _ time.Duration) ([]store.Command, error) {
	f.claimPluginIDs = append([]string(nil), pluginIDs...)
	return f.claimed, nil
}

func (f *commandLeaseStore) MarkCompleted(_ context.Context, commandID string, collectedCount int) error {
	f.completed = append(f.completed, commandID)
	f.completedCount = collectedCount
	return nil
}

func (f *commandLeaseStore) RenewLease(_ context.Context, commandID string, _ time.Duration) error {
	f.renewed = append(f.renewed, commandID)
	return nil
}

func (f *commandLeaseStore) MarkAttemptFailed(_ context.Context, commandID, reason string) (store.CommandStatus, error) {
	f.attemptFailed = append(f.attemptFailed, commandID)
	f.attemptReason = reason
	status := f.attemptStatus
	if status == "" {
		status = store.CmdStatusPending
	}
	return status, f.attemptErr
}

func (f *commandLeaseStore) MarkTerminalFailed(_ context.Context, commandID, _ string) error {
	f.terminal = append(f.terminal, commandID)
	return nil
}

func testStreamSession(pluginID string, capacity int) *StreamSession {
	ctx, cancel := context.WithCancel(context.Background())
	return &StreamSession{
		pluginID: pluginID,
		sendCh:   make(chan *pb.CoreMessage, capacity),
		ctx:      ctx,
		cancel:   cancel,
		closed:   make(chan struct{}),
	}
}

func TestDispatchOnceClaimsConnectedPluginBeforeSend(t *testing.T) {
	cmdStore := &commandLeaseStore{claimed: []store.Command{{
		CommandID: "cmd_claimed", CommandType: store.CmdSync,
		TargetPlugin: "plg_online", Attempts: 1,
	}}}
	session := testStreamSession("plg_online", 1)
	mgr := &Manager{
		sessions:     map[string]*StreamSession{"plg_online": session},
		commandStore: cmdStore,
	}

	if err := mgr.dispatchOnce(context.Background()); err != nil {
		t.Fatalf("dispatchOnce() error = %v", err)
	}
	if !slices.Equal(cmdStore.claimPluginIDs, []string{"plg_online"}) {
		t.Fatalf("claim plugin IDs = %v, want [plg_online]", cmdStore.claimPluginIDs)
	}
	select {
	case message := <-session.sendCh:
		if message.GetSync() == nil || message.GetSync().CommandId != "cmd_claimed" {
			t.Fatalf("sent message = %+v, want claimed sync command", message)
		}
	default:
		t.Fatal("claimed command was not enqueued")
	}
	if len(cmdStore.attemptFailed) != 0 {
		t.Fatalf("successful dispatch released attempts: %v", cmdStore.attemptFailed)
	}
}

func TestDispatchOnceReleasesClaimWhenSessionDisappears(t *testing.T) {
	cmdStore := &commandLeaseStore{claimed: []store.Command{{
		CommandID: "cmd_missing", CommandType: store.CmdSync,
		TargetPlugin: "plg_missing", Attempts: 1,
	}}}
	mgr := &Manager{sessions: map[string]*StreamSession{}, commandStore: cmdStore}

	if err := mgr.dispatchOnce(context.Background()); err != nil {
		t.Fatalf("dispatchOnce() error = %v", err)
	}
	if !slices.Equal(cmdStore.attemptFailed, []string{"cmd_missing"}) {
		t.Fatalf("released commands = %v, want cmd_missing", cmdStore.attemptFailed)
	}
}

func TestDispatchOnceReleasesClaimWhenBufferFull(t *testing.T) {
	session := testStreamSession("plg_full", 1)
	session.sendCh <- &pb.CoreMessage{}
	cmdStore := &commandLeaseStore{claimed: []store.Command{{
		CommandID: "cmd_full", CommandType: store.CmdSync,
		TargetPlugin: "plg_full", Attempts: 5,
	}}, attemptStatus: store.CmdStatusFailed}
	mgr := &Manager{
		sessions:     map[string]*StreamSession{"plg_full": session},
		commandStore: cmdStore,
	}

	if err := mgr.dispatchOnce(context.Background()); err != nil {
		t.Fatalf("dispatchOnce() error = %v", err)
	}
	if !slices.Equal(cmdStore.attemptFailed, []string{"cmd_full"}) {
		t.Fatalf("released commands = %v, want cmd_full", cmdStore.attemptFailed)
	}
	if cmdStore.attemptReason == "" {
		t.Fatal("buffer failure reason was not recorded")
	}
}

func TestDispatchOnceUnknownTypeIsTerminal(t *testing.T) {
	session := testStreamSession("plg_unknown", 1)
	cmdStore := &commandLeaseStore{claimed: []store.Command{{
		CommandID: "cmd_unknown", CommandType: store.CommandType("unknown"),
		TargetPlugin: "plg_unknown", Attempts: 1,
	}}}
	mgr := &Manager{
		sessions:     map[string]*StreamSession{"plg_unknown": session},
		commandStore: cmdStore,
	}

	if err := mgr.dispatchOnce(context.Background()); err != nil {
		t.Fatalf("dispatchOnce() error = %v", err)
	}
	if !slices.Equal(cmdStore.terminal, []string{"cmd_unknown"}) {
		t.Fatalf("terminal commands = %v, want cmd_unknown", cmdStore.terminal)
	}
}

func TestDispatchOnceContinuesAfterReleaseFailure(t *testing.T) {
	session := testStreamSession("plg_online", 1)
	cmdStore := &commandLeaseStore{
		claimed: []store.Command{
			{CommandID: "cmd_missing", CommandType: store.CmdSync, TargetPlugin: "plg_missing", Attempts: 1},
			{CommandID: "cmd_online", CommandType: store.CmdSync, TargetPlugin: "plg_online", Attempts: 1},
		},
		attemptErr: errors.New("database unavailable"),
	}
	mgr := &Manager{
		sessions:     map[string]*StreamSession{"plg_online": session},
		commandStore: cmdStore,
	}

	if err := mgr.dispatchOnce(context.Background()); err == nil {
		t.Fatal("dispatchOnce() error = nil, want release failure")
	}
	select {
	case message := <-session.sendCh:
		if message.GetSync() == nil || message.GetSync().CommandId != "cmd_online" {
			t.Fatalf("sent message = %+v, want later claimed command", message)
		}
	default:
		t.Fatal("release failure abandoned a later claimed command")
	}
}

func TestHandleCommandAckLifecycle(t *testing.T) {
	tests := []struct {
		name          string
		ack           *pb.CommandAck
		wantCompleted bool
		wantRenewed   bool
		wantFailed    bool
	}{
		{name: "live success", ack: &pb.CommandAck{CommandId: "cmd", Status: "success", CollectedCount: 3}, wantCompleted: true},
		{name: "documented completed", ack: &pb.CommandAck{CommandId: "cmd", Status: "completed"}, wantCompleted: true},
		{name: "legacy empty success", ack: &pb.CommandAck{CommandId: "cmd"}, wantCompleted: true},
		{name: "accepted progress", ack: &pb.CommandAck{CommandId: "cmd", Status: "accepted"}, wantRenewed: true},
		{name: "running progress", ack: &pb.CommandAck{CommandId: "cmd", Status: "running"}, wantRenewed: true},
		{name: "failed status", ack: &pb.CommandAck{CommandId: "cmd", Status: "failed", Message: "provider down"}, wantFailed: true},
		{name: "error overrides success", ack: &pb.CommandAck{CommandId: "cmd", Status: "success", Error: "push failed"}, wantFailed: true},
		{name: "unknown status", ack: &pb.CommandAck{CommandId: "cmd", Status: "mystery"}, wantFailed: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmdStore := &commandLeaseStore{}
			mgr := &Manager{commandStore: cmdStore}
			if err := mgr.handleCommandAck(context.Background(), tt.ack); err != nil {
				t.Fatalf("handleCommandAck() error = %v", err)
			}
			if got := len(cmdStore.completed) == 1; got != tt.wantCompleted {
				t.Fatalf("completed = %v, want %v", cmdStore.completed, tt.wantCompleted)
			}
			if got := len(cmdStore.renewed) == 1; got != tt.wantRenewed {
				t.Fatalf("renewed = %v, want %v", cmdStore.renewed, tt.wantRenewed)
			}
			if got := len(cmdStore.attemptFailed) == 1; got != tt.wantFailed {
				t.Fatalf("failed = %v, want %v", cmdStore.attemptFailed, tt.wantFailed)
			}
		})
	}
}

func TestHandleCommandAckDuplicateFailureIsIdempotent(t *testing.T) {
	cmdStore := &commandLeaseStore{attemptErr: store.ErrCommandNotDispatched}
	mgr := &Manager{commandStore: cmdStore}
	err := mgr.handleCommandAck(context.Background(), &pb.CommandAck{
		CommandId: "cmd_done", Status: "failed", Error: "late duplicate",
	})
	if err != nil && !errors.Is(err, store.ErrCommandNotDispatched) {
		t.Fatalf("duplicate failure error = %v, want nil", err)
	}
}
