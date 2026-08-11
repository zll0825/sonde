// Event cluster snapshot endpoints. The API reads from the JSONL snapshot
// file produced by Core (CLUSTER_SNAPSHOTS_FILE) so that both processes share
// the same clustering history without an RPC layer. A fallback ring-backed
// handler is available for when no snapshot file path is configured.
package main

import (
	"net/http"
	"strconv"

	coreevent "capital_observatory/internal/core/event"
)

// clusterHandler returns the most recent N event-cluster snapshots.
// Implemented as a closure to capture the ring; kept as a fallback for when
// no snapshot file is configured (returns empty until Core emits data).
func clusterHandler(ring *coreevent.ClusterRing) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET required"})
			return
		}
		q := r.URL.Query().Get("limit")
		limit := 50
		if q != "" {
			if n, err := strconv.Atoi(q); err == nil && n > 0 && n <= 200 {
				limit = n
			}
		}
		snapshots := ring.Recent(limit)
		if snapshots == nil {
			snapshots = []coreevent.ClusterSnapshot{}
		}
		writeJSON(w, http.StatusOK, snapshots)
	}
}

// clusterHandlerWithStore returns the most recent N event-cluster snapshots
// from the snapshot file produced by Core. This is the same data Core sees,
// shared across processes via the JSONL file (no in-process ring duplication).
func clusterHandlerWithStore(store *coreevent.ClusterSnapshotStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET required"})
			return
		}
		q := r.URL.Query().Get("limit")
		limit := 50
		if q != "" {
			if n, err := strconv.Atoi(q); err == nil && n > 0 && n <= 200 {
				limit = n
			}
		}
		snapshots, err := store.Last(limit)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to read cluster snapshots"})
			return
		}
		if snapshots == nil {
			snapshots = []coreevent.ClusterSnapshot{}
		}
		writeJSON(w, http.StatusOK, snapshots)
	}
}
