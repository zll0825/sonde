package event

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// ClusterSnapshotStore persists ClusterSnapshot entries to a JSONL file
// (one JSON object per line, append-only). It survives process restarts and is
// the single source of truth shared between the Core writer and the API reader
// when both processes run on the same machine (CLUSTER_SNAPSHOTS_FILE env).
//
// The store is safe for concurrent Append calls — the Core's outbox handlers
// may race with the goroutine that flushes a snapshot after clustering.
type ClusterSnapshotStore struct {
	mu       sync.Mutex
	filePath string
	file     *os.File
}

// NewClusterSnapshotStore opens (creating if needed) a JSONL snapshot file
// in append mode. The parent directory must exist.
func NewClusterSnapshotStore(filePath string) (*ClusterSnapshotStore, error) {
	f, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("open cluster snapshot file %s: %w", filePath, err)
	}
	return &ClusterSnapshotStore{filePath: filePath, file: f}, nil
}

// Append marshals snap to a single JSON line and appends it. fsync guarantees
// durability across a crash so that the API sees a complete line even if it
// reads concurrently.
func (s *ClusterSnapshotStore) Append(snap ClusterSnapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	line, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("marshal cluster snapshot: %w", err)
	}
	line = append(line, '\n')
	if _, err := s.file.Write(line); err != nil {
		return fmt.Errorf("write cluster snapshot: %w", err)
	}
	return s.file.Sync()
}

// ReadAll reads the entire JSONL file and returns snapshots whose
// LastTriggered is within the lookback window from now. Invalid JSON lines
// are silently skipped so a partial write mid-line does not corrupt replay.
//
// Pass a lookback of 0 or negative to return every stored snapshot.
func (s *ClusterSnapshotStore) ReadAll(lookback time.Duration) ([]ClusterSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read cluster snapshot file: %w", err)
	}

	var result []ClusterSnapshot
	var cutoff time.Time
	if lookback > 0 {
		cutoff = time.Now().Add(-lookback)
	}
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var snap ClusterSnapshot
		if err := json.Unmarshal(line, &snap); err != nil {
			continue // skip corrupt line
		}
		if cutoff.IsZero() || !snap.LastTriggered.IsZero() && snap.LastTriggered.After(cutoff) {
			result = append(result, snap)
		}
	}
	return result, nil
}

// Last returns the most recent n snapshots ordered newest-first. Internally it
// reads the whole file and walks backward so the JSONL append-only layout does
// not require an index.
func (s *ClusterSnapshotStore) Last(n int) ([]ClusterSnapshot, error) {
	all, err := s.ReadAll(0)
	if err != nil {
		return nil, err
	}
	if n <= 0 || n > len(all) {
		n = len(all)
	}
	// Reverse the slice to newest-first.
	out := make([]ClusterSnapshot, 0, n)
	for i := len(all) - 1; i >= len(all)-n && i >= 0; i-- {
		out = append(out, all[i])
	}
	return out, nil
}

// Replay loads all stored snapshots into the given ring buffer on startup so
// that Core survives a restart without losing its clustering history. Calling
// this before the first Receive ensures continuity.
func (s *ClusterSnapshotStore) Replay(ring *ClusterRing) (int, error) {
	snaps, err := s.ReadAll(0)
	if err != nil {
		return 0, err
	}
	for _, snap := range snaps {
		ring.PushByID(snap)
	}
	return len(snaps), nil
}

// Close releases the underlying file handle.
func (s *ClusterSnapshotStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file == nil {
		return nil
	}
	return s.file.Close()
}
