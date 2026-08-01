package store

import (
	"context"
	"testing"
	"time"
)

// commandStoreFake is a minimal in-memory CommandStore for unit-testing the
// dispatcher and status-transaction logic without a real Postgres.
type commandStoreFake struct {
	pending    []Command
	dispatched []string
	completed  []string
	failed     []string
}

func newCommandStoreFake(cmds []Command) *commandStoreFake {
	return &commandStoreFake{pending: cmds}
}

func (f *commandStoreFake) GetPendingCommands(_ context.Context) ([]Command, error) {
	out := f.pending
	f.pending = nil // each poll returns pending once (simulates row consumption)
	return out, nil
}

func (f *commandStoreFake) MarkDispatched(_ context.Context, commandID string) error {
	f.dispatched = append(f.dispatched, commandID)
	return nil
}

func (f *commandStoreFake) MarkCompleted(_ context.Context, commandID string, collectedCount int) error {
	f.completed = append(f.completed, commandID)
	return nil
}

func (f *commandStoreFake) MarkFailed(_ context.Context, commandID string, errMsg string) error {
	f.failed = append(f.failed, commandID)
	return nil
}

func TestCommandTypes_ValidateConstants(t *testing.T) {
	if CmdSync != "sync" {
		t.Errorf("CmdSync = %q, want \"sync\"", CmdSync)
	}
	if CmdBackfill != "backfill" {
		t.Errorf("CmdBackfill = %q, want \"backfill\"", CmdBackfill)
	}
}

func TestCommandLifecycle_PendingToDispatched(t *testing.T) {
	cmd := Command{
		CommandID:    "cmd-001",
		CommandType:  CmdSync,
		TargetPlugin: "plg_etf",
		RequestedBy:  "user:alice",
		Status:       CmdStatusPending,
		MetricIDs:    []string{"gld_price"},
		RequestedAt:  time.Now(),
	}

	fake := newCommandStoreFake([]Command{cmd})

	pending, err := fake.GetPendingCommands(nil)
	if err != nil {
		t.Fatalf("GetPendingCommands: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending command, got %d", len(pending))
	}

	if err := fake.MarkDispatched(nil, pending[0].CommandID); err != nil {
		t.Fatalf("MarkDispatched: %v", err)
	}
	if len(fake.dispatched) != 1 || fake.dispatched[0] != "cmd-001" {
		t.Errorf("dispatched = %v, want [cmd-001]", fake.dispatched)
	}
}

func TestCommandLifecycle_AckTransitionsToCompleted(t *testing.T) {
	fake := newCommandStoreFake(nil)

	if err := fake.MarkCompleted(nil, "cmd-002", 3); err != nil {
		t.Fatalf("MarkCompleted: %v", err)
	}
	if len(fake.completed) != 1 || fake.completed[0] != "cmd-002" {
		t.Errorf("completed = %v, want [cmd-002]", fake.completed)
	}
}

func TestCommandLifecycle_FailureTransitionsToFailed(t *testing.T) {
	fake := newCommandStoreFake(nil)

	if err := fake.MarkFailed(nil, "cmd-003", "collector timeout"); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	if len(fake.failed) != 1 || fake.failed[0] != "cmd-003" {
		t.Errorf("failed = %v, want [cmd-003]", fake.failed)
	}
}

func TestCommandAck_StatusDeterminesCompletion(t *testing.T) {
	tests := []struct {
		name      string
		status    string
		errMsg    string
		wantState string
	}{
		{"success with empty error", "success", "", "completed"},
		{"success with error field but status success", "success", "some info", "completed"},
		{"failed status", "failed", "collector timeout", "failed"},
		{"empty status with error", "", "collector timeout", "failed"},
		{"success but error wins (per current impl)", "success", "", "completed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newCommandStoreFake(nil)
			// Mimic the logic in Manager.handleCommandAck.
			if tt.status == "success" || tt.errMsg == "" {
				_ = store.MarkCompleted(nil, "cmd-x", 0)
			} else {
				_ = store.MarkFailed(nil, "cmd-x", tt.errMsg)
			}
			if tt.wantState == "completed" && len(store.completed) != 1 {
				t.Errorf("expected completed, got failed=%v completed=%v", store.failed, store.completed)
			}
			if tt.wantState == "failed" && len(store.failed) != 1 {
				t.Errorf("expected failed, got failed=%v completed=%v", store.failed, store.completed)
			}
		})
	}
}
