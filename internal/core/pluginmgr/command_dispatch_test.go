package pluginmgr

import (
	"testing"
	"time"

	"sonde/internal/core/store"
	pb "sonde/pkg/proto/plugin/v1"
)

func TestCommandToCoreMessage_Sync(t *testing.T) {
	cmd := store.Command{
		CommandID:    "cmd-sync-001",
		CommandType:  store.CmdSync,
		TargetPlugin: "plg_etf",
		Reason:       "manual refresh",
		MetricIDs:    []string{"gld_price", "gld_daily_flow"},
	}

	msg := commandToCoreMessage(cmd)
	if msg == nil {
		t.Fatal("commandToCoreMessage returned nil")
	}

	sync := msg.GetSync()
	if sync == nil {
		t.Fatal("payload is not a SyncCommand")
	}
	if sync.CommandId != "cmd-sync-001" {
		t.Errorf("command_id = %q, want cmd-sync-001", sync.CommandId)
	}
	if sync.Reason != "manual refresh" {
		t.Errorf("reason = %q, want manual refresh", sync.Reason)
	}
	if len(sync.MetricIds) != 2 || sync.MetricIds[0] != "gld_price" {
		t.Errorf("metric_ids = %v, want [gld_price, gld_daily_flow]", sync.MetricIds)
	}
}

func TestCommandToCoreMessage_Backfill(t *testing.T) {
	now := time.Now()
	later := now.Add(1 * time.Hour)

	cmd := store.Command{
		CommandID:    "cmd-backfill-002",
		CommandType:  store.CmdBackfill,
		TargetPlugin: "plg_etf",
		Reason:       "historical backfill",
		MetricIDs:    []string{"gld_price"},
		WindowStart:  &now,
		WindowEnd:    &later,
	}

	msg := commandToCoreMessage(cmd)
	bf := msg.GetBackfill()
	if bf == nil {
		t.Fatal("payload is not a BackfillCommand")
	}
	if bf.CommandId != "cmd-backfill-002" {
		t.Errorf("command_id = %q, want cmd-backfill-002", bf.CommandId)
	}
	if bf.WindowStart != now.Unix() {
		t.Errorf("window_start = %d, want %d", bf.WindowStart, now.Unix())
	}
	if bf.WindowEnd != later.Unix() {
		t.Errorf("window_end = %d, want %d", bf.WindowEnd, later.Unix())
	}
}

func TestCommandToCoreMessage_BackfillNilWindow(t *testing.T) {
	cmd := store.Command{
		CommandID:    "cmd-backfill-003",
		CommandType:  store.CmdBackfill,
		TargetPlugin: "plg_etf",
	}

	msg := commandToCoreMessage(cmd)
	bf := msg.GetBackfill()
	if bf == nil {
		t.Fatal("payload is not a BackfillCommand")
	}
	if bf.WindowStart != 0 {
		t.Errorf("window_start = %d, want 0", bf.WindowStart)
	}
	if bf.WindowEnd != 0 {
		t.Errorf("window_end = %d, want 0", bf.WindowEnd)
	}
}

func TestCommandToCoreMessage_UnknownType(t *testing.T) {
	cmd := store.Command{
		CommandID:   "cmd-bogus",
		CommandType: store.CommandType("nope"),
	}

	msg := commandToCoreMessage(cmd)
	if msg != nil {
		t.Errorf("expected nil for unknown command type, got %T", msg.Payload)
	}
}

func TestHandleCommandAck_NilNoop(t *testing.T) {
	m := &Manager{commandStore: nil}
	// Should not panic on nil ack.
	if err := m.handleCommandAck(nil, nil); err != nil {
		t.Errorf("handleCommandAck(nil) returned error: %v", err)
	}
}

func TestCmdToStreamEndpoint(t *testing.T) {
	cases := map[string]struct {
		msg  *pb.CoreMessage
		want string
	}{
		"sync": {
			msg:  &pb.CoreMessage{Payload: &pb.CoreMessage_Sync{Sync: &pb.SyncCommand{}}},
			want: "Sync",
		},
		"backfill": {
			msg:  &pb.CoreMessage{Payload: &pb.CoreMessage_Backfill{Backfill: &pb.BackfillCommand{}}},
			want: "Backfill",
		},
		"unknown": {
			msg:  &pb.CoreMessage{},
			want: "(unknown)",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := cmdToStreamEndpoint(tc.msg)
			if got != tc.want {
				t.Errorf("cmdToStreamEndpoint = %q, want %q", got, tc.want)
			}
		})
	}
}
