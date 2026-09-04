package event

import (
	"bytes"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"sonde/pkg/model"
)

func TestSnapshotStore_DeduplicatesRetryButPersistsUpdatedState(t *testing.T) {
	path := t.TempDir() + "/clusters.jsonl"
	store, err := NewClusterSnapshotStore(path)
	if err != nil {
		t.Fatalf("NewClusterSnapshotStore: %v", err)
	}

	first := ClusterSnapshot{ClusterID: "c1", MemberCount: 1, LastTriggered: time.Now()}
	if err := store.Append(first); err != nil {
		t.Fatalf("append first: %v", err)
	}
	if err := store.Append(first); err != nil {
		t.Fatalf("append retry: %v", err)
	}
	updated := first
	updated.MemberCount = 2
	updated.MergedAlertIDs = []string{"a1", "a2"}
	if err := store.Append(updated); err != nil {
		t.Fatalf("append update: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read raw snapshots: %v", err)
	}
	if got := bytes.Count(raw, []byte{'\n'}); got != 2 {
		t.Fatalf("durable revisions = %d, want 2", got)
	}

	reopened, err := NewClusterSnapshotStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	if err := reopened.Append(updated); err != nil {
		t.Fatalf("append retry after restart: %v", err)
	}
	snaps, err := reopened.ReadAll(0)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(snaps) != 1 || snaps[0].MemberCount != 2 {
		t.Fatalf("latest snapshots = %#v, want one updated cluster", snaps)
	}
}

func TestSnapshotStore_AppendReadRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := dir + "/clusters.jsonl"

	store, err := NewClusterSnapshotStore(path)
	if err != nil {
		t.Fatalf("NewClusterSnapshotStore: %v", err)
	}
	defer store.Close()

	now := time.Now()
	if err := store.Append(ClusterSnapshot{
		ClusterID:     "evt:a",
		PrimaryEntity: "SPY",
		MemberCount:   2,
		Severity:      model.SeverityWarning,
		LastTriggered: now,
		Coalesced:     true,
	}); err != nil {
		t.Fatalf("append: %v", err)
	}

	snaps, err := store.ReadAll(0)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(snaps) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(snaps))
	}
	if snaps[0].ClusterID != "evt:a" {
		t.Fatalf("ClusterID: got %s want evt:a", snaps[0].ClusterID)
	}
	if !snaps[0].LastTriggered.Equal(now) {
		t.Fatalf("LastTriggered: got %v want %v", snaps[0].LastTriggered, now)
	}
}

func TestSnapshotStore_SkipInvalidJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := dir + "/clusters.jsonl"

	// Pre-write one valid + one invalid line.
	store, err := NewClusterSnapshotStore(path)
	if err != nil {
		t.Fatalf("NewClusterSnapshotStore: %v", err)
	}
	if err := store.Append(ClusterSnapshot{ClusterID: "good", LastTriggered: time.Now()}); err != nil {
		t.Fatalf("append: %v", err)
	}
	// Manually append a corrupt line that will not unmarshal.
	_, _ = store.file.Write([]byte("{this is not valid json}\n"))

	snaps, err := store.ReadAll(0)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(snaps) != 1 {
		t.Fatalf("expected 1 valid snapshot after skipping corrupt line, got %d", len(snaps))
	}
}

func TestSnapshotStore_Last(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := dir + "/clusters.jsonl"

	store, err := NewClusterSnapshotStore(path)
	if err != nil {
		t.Fatalf("NewClusterSnapshotStore: %v", err)
	}
	defer store.Close()

	// Append 5 entries in order: a, b, c, d, e.
	for i, id := range []string{"a", "b", "c", "d", "e"} {
		if err := store.Append(ClusterSnapshot{ClusterID: id, LastTriggered: time.Now().Add(time.Duration(i) * time.Second)}); err != nil {
			t.Fatalf("append %s: %v", id, err)
		}
	}

	got, err := store.Last(3)
	if err != nil {
		t.Fatalf("Last: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 entries from Last(3), got %d", len(got))
	}
	// Last returns newest-first: e, d, c.
	if got[0].ClusterID != "e" || got[1].ClusterID != "d" || got[2].ClusterID != "c" {
		t.Fatalf("unexpected order: %s, %s, %s", got[0].ClusterID, got[1].ClusterID, got[2].ClusterID)
	}
}

func TestSnapshotStore_LookbackFiltering(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := dir + "/clusters.jsonl"

	store, err := NewClusterSnapshotStore(path)
	if err != nil {
		t.Fatalf("NewClusterSnapshotStore: %v", err)
	}
	defer store.Close()

	// One entry 2 hours ago, one entry now.
	old := time.Now().Add(-2 * time.Hour)
	if err := store.Append(ClusterSnapshot{ClusterID: "old", LastTriggered: old}); err != nil {
		t.Fatalf("append old: %v", err)
	}
	if err := store.Append(ClusterSnapshot{ClusterID: "new", LastTriggered: time.Now()}); err != nil {
		t.Fatalf("append new: %v", err)
	}

	// Lookback 1 hour — only "new" survives.
	snaps, err := store.ReadAll(1 * time.Hour)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(snaps) != 1 {
		t.Fatalf("expected 1 snapshot within lookback, got %d", len(snaps))
	}
	if snaps[0].ClusterID != "new" {
		t.Fatalf("expected 'new', got %s", snaps[0].ClusterID)
	}

	// Lookback 0 returns everything.
	snaps, err = store.ReadAll(0)
	if err != nil {
		t.Fatalf("ReadAll(0): %v", err)
	}
	if len(snaps) != 2 {
		t.Fatalf("expected 2 snapshots with no lookback, got %d", len(snaps))
	}
}

func TestSnapshotStore_ConcurrentAppend(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := dir + "/concurrent.jsonl"

	store, err := NewClusterSnapshotStore(path)
	if err != nil {
		t.Fatalf("NewClusterSnapshotStore: %v", err)
	}
	defer store.Close()

	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			id := fmt.Sprintf("c%03d", idx)
			if err := store.Append(ClusterSnapshot{
				ClusterID:      id,
				LastTriggered:  time.Now(),
				MergedAlertIDs: []string{id},
			}); err != nil {
				t.Errorf("concurrent append %d: %v", idx, err)
			}
		}(i)
	}
	wg.Wait()

	snaps, err := store.ReadAll(0)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(snaps) != n {
		t.Fatalf("expected %d snapshots after concurrent appends, got %d", n, len(snaps))
	}
}

func TestSnapshotStore_Replay(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := dir + "/replay.jsonl"

	// Write some snapshots, then close and reopen to simulate restart.
	store, err := NewClusterSnapshotStore(path)
	if err != nil {
		t.Fatalf("NewClusterSnapshotStore: %v", err)
	}
	for _, id := range []string{"x", "y", "z"} {
		if err := store.Append(ClusterSnapshot{ClusterID: id, LastTriggered: time.Now()}); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Reopen and replay.
	store2, err := NewClusterSnapshotStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer store2.Close()

	ring := NewClusterRing(10)
	n, err := store2.Replay(ring)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if n != 3 {
		t.Fatalf("expected 3 replayed, got %d", n)
	}

	// Verify the snapshots made it into the ring.
	if _, ok := ring.ByID("x"); !ok {
		t.Fatal("expected cluster x in ring after replay")
	}
	if _, ok := ring.ByID("z"); !ok {
		t.Fatal("expected cluster z in ring after replay")
	}
}
