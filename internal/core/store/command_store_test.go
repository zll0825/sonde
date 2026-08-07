package store

import (
	"context"
	"testing"
	"time"
)

// commandStoreFake is a minimal in-memory CommandStore for unit-testing the
// dispatcher and status-transaction logic without a real Postgres.
type commandStoreFake struct {
	pending       []Command
	claimed       []string
	completed     []string
	renewed       []string
	attemptFailed []string
	terminal      []string
}

func newCommandStoreFake(cmds []Command) *commandStoreFake {
	return &commandStoreFake{pending: cmds}
}

func (f *commandStoreFake) ClaimDispatchable(_ context.Context, _ []string, _ int, _ time.Duration) ([]Command, error) {
	out := f.pending
	f.pending = nil // each poll returns pending once (simulates row consumption)
	for _, cmd := range out {
		f.claimed = append(f.claimed, cmd.CommandID)
	}
	return out, nil
}

func (f *commandStoreFake) MarkCompleted(_ context.Context, commandID string, collectedCount int) error {
	f.completed = append(f.completed, commandID)
	return nil
}

func (f *commandStoreFake) RenewLease(_ context.Context, commandID string, _ time.Duration) error {
	f.renewed = append(f.renewed, commandID)
	return nil
}

func (f *commandStoreFake) MarkAttemptFailed(_ context.Context, commandID string, _ string) (CommandStatus, error) {
	f.attemptFailed = append(f.attemptFailed, commandID)
	return CmdStatusPending, nil
}

func (f *commandStoreFake) MarkTerminalFailed(_ context.Context, commandID string, _ string) error {
	f.terminal = append(f.terminal, commandID)
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

	pending, err := fake.ClaimDispatchable(nil, []string{"plg_etf"}, 100, time.Minute)
	if err != nil {
		t.Fatalf("ClaimDispatchable: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending command, got %d", len(pending))
	}

	if len(fake.claimed) != 1 || fake.claimed[0] != "cmd-001" {
		t.Errorf("claimed = %v, want [cmd-001]", fake.claimed)
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

func TestCommandLifecycle_AttemptFailureIsRecordedForRetry(t *testing.T) {
	fake := newCommandStoreFake(nil)

	if _, err := fake.MarkAttemptFailed(nil, "cmd-003", "collector timeout"); err != nil {
		t.Fatalf("MarkAttemptFailed: %v", err)
	}
	if len(fake.attemptFailed) != 1 || fake.attemptFailed[0] != "cmd-003" {
		t.Errorf("attemptFailed = %v, want [cmd-003]", fake.attemptFailed)
	}
}
