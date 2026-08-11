// Package event owns typed payload contracts carried by the database outbox.
//
// ClusterRing is an in-memory ring buffer for the most recent EventCluster
// snapshots produced by the classification pipeline. It is read by the API
// server's /api/clusters/ endpoint and written by the core outbox handler.
package event

import (
	"sync"
	"time"

	"capital_observatory/pkg/model"
)

// ClusterSnapshot is a point-in-time view of an EventCluster suitable for
// API exposure. It intentionally omits the merge log and full alert list to
// keep the JSON payload small.
type ClusterSnapshot struct {
	ClusterID     string         `json:"cluster_id"`
	PrimaryEntity string         `json:"primary_entity"`
	MemberCount   int            `json:"member_count"`
	Severity      model.Severity `json:"severity"`
	LastTriggered time.Time      `json:"last_triggered"`
	Coalesced     bool           `json:"coalesced"`
}

// ClusterRing is a fixed-capacity circular buffer of ClusterSnapshots.
// Writers call Push; readers call Recent. The buffer overwrites the oldest
// entry when full.
type ClusterRing struct {
	mu    sync.RWMutex
	items []ClusterSnapshot
	head  int // index of the next write
	count int // number of valid entries (0..size)
	size  int
}

// NewClusterRing creates a ring buffer that holds at most size snapshots.
// size must be > 0; values <= 0 are treated as 1.
func NewClusterRing(size int) *ClusterRing {
	if size <= 0 {
		size = 1
	}
	return &ClusterRing{
		items: make([]ClusterSnapshot, size),
		size:  size,
	}
}

// Push appends a snapshot to the ring, overwriting the oldest entry when the
// buffer is full.
func (r *ClusterRing) Push(c ClusterSnapshot) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items[r.head] = c
	r.head = (r.head + 1) % r.size
	if r.count < r.size {
		r.count++
	}
}

// Recent returns up to n snapshots in reverse-chronological order (newest
// first). If n exceeds the current count, all entries are returned.
func (r *ClusterRing) Recent(n int) []ClusterSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if n > r.count {
		n = r.count
	}
	if n <= 0 {
		return []ClusterSnapshot{}
	}

	out := make([]ClusterSnapshot, 0, n)
	for i := 0; i < n; i++ {
		// head points at the next write, so the newest valid entry is at head-1.
		idx := r.head - 1 - i
		if idx < 0 {
			idx += r.size
		}
		out = append(out, r.items[idx])
	}
	return returnSlice(out)
}

// returnSlice ensures a non-nil slice when out is empty after trimming.
func returnSlice(s []ClusterSnapshot) []ClusterSnapshot {
	if s == nil {
		return []ClusterSnapshot{}
	}
	return s
}

// newClusterSnapshot builds a ClusterSnapshot from an alert's last-trigger
// time. Used by the core pipeline when pushing classification results.
func newClusterSnapshot(clusterID, primaryEntity string, memberCount int,
	severity model.Severity, lastTriggered time.Time, coalesced bool) ClusterSnapshot {
	return ClusterSnapshot{
		ClusterID:     clusterID,
		PrimaryEntity: primaryEntity,
		MemberCount:   memberCount,
		Severity:      severity,
		LastTriggered: lastTriggered,
		Coalesced:     coalesced,
	}
}
