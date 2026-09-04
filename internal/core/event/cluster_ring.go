// Package event owns typed payload contracts carried by the database outbox.
//
// ClusterRing is an in-memory ring buffer for the most recent EventCluster
// snapshots produced by the classification pipeline. It is read by the API
// server's /api/clusters/ endpoint and written by the core outbox handler.
package event

import (
	"sync"
	"time"

	"sonde/pkg/model"
)

// ClusterSnapshot is a point-in-time view of an EventCluster suitable for
// API exposure. It intentionally omits the merge log and full alert list to
// keep the JSON payload small.
type ClusterSnapshot struct {
	ClusterID      string         `json:"cluster_id"`
	PrimaryEntity  string         `json:"primary_entity"`
	MemberCount    int            `json:"member_count"`
	Severity       model.Severity `json:"severity"`
	LastTriggered  time.Time      `json:"last_triggered"`
	Coalesced      bool           `json:"coalesced"`
	MergedAlertIDs []string       `json:"merged_alert_ids,omitempty"`
	TriggeredEvent string         `json:"triggered_event,omitempty"`
	// Priority is the computed research/notification priority score (higher =
	// more urgent). Populated by the outbox handler after Layer-2 gating.
	Priority float64 `json:"priority"`
	// MergeTrail is a point-in-time snapshot of the underlying cluster's
	// classification.MergeEntry list. It is populated by the outbox handler and
	// surfaced by /api/clusters/{id} so the UI / operators can see *why* two
	// alerts share a cluster instead of just that they do.
	MergeTrail *MergeTrail `json:"merge_trail,omitempty"`
}

// MergeAudit is a concise, API-safe view of the cluster's merge decisions.
// It is a snapshot of the underlying classification.MergeEntry at persist time
// so that the API can explain the cluster's member relationship without
// exporting the full research/notification machinery.
type MergeAudit struct {
	AlertID   string `json:"alert_id"`
	MetricID  string `json:"metric_id"`
	Reason    string `json:"reason"`
	Coalesced bool   `json:"coalesced"`
}

// MergeTrail is the persist-time snapshot carried by ClusterSnapshot.
type MergeTrail struct {
	Entries []MergeAudit `json:"entries"`
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

// PushByID appends a snapshot keyed by ClusterID. If a snapshot with the same
// ClusterID already exists, it is updated in place (fields refreshed, alert IDs
// merged) so that re-clustering the same logical event from the retry path
// never appends a duplicate.
func (r *ClusterRing) PushByID(c ClusterSnapshot) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Linear scan for an existing entry with the same ClusterID.
	for i := 0; i < r.count; i++ {
		idx := (r.head - r.count + i + r.size) % r.size
		if r.items[idx].ClusterID == c.ClusterID {
			// Update in place — merge alert IDs without duplicates.
			existing := &r.items[idx]
			existing.MemberCount = c.MemberCount
			existing.Severity = c.Severity
			existing.LastTriggered = c.LastTriggered
			existing.Coalesced = c.Coalesced
			existing.PrimaryEntity = c.PrimaryEntity
			existing.MergedAlertIDs = mergeAlertIDs(existing.MergedAlertIDs, c.MergedAlertIDs)
			existing.Priority = c.Priority
			existing.MergeTrail = c.MergeTrail
			if c.TriggeredEvent != "" {
				existing.TriggeredEvent = c.TriggeredEvent
			}
			return
		}
	}

	// Not found — append as usual.
	r.items[r.head] = c
	r.head = (r.head + 1) % r.size
	if r.count < r.size {
		r.count++
	}
}

// ByID returns the snapshot with the given ClusterID if it exists in the ring.
func (r *ClusterRing) ByID(id string) (ClusterSnapshot, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for i := 0; i < r.count; i++ {
		idx := (r.head - r.count + i + r.size) % r.size
		if r.items[idx].ClusterID == id {
			return r.items[idx], true
		}
	}
	return ClusterSnapshot{}, false
}

func mergeAlertIDs(a, b []string) []string {
	if len(b) == 0 {
		return a
	}
	seen := make(map[string]struct{}, len(a)+len(b))
	for _, id := range a {
		seen[id] = struct{}{}
	}
	result := append([]string{}, a...)
	for _, id := range b {
		if _, ok := seen[id]; !ok {
			seen[id] = struct{}{}
			result = append(result, id)
		}
	}
	return result
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
