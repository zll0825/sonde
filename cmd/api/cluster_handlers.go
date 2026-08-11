// Event cluster snapshot endpoints. cmd/api cannot see cmd/core's in-memory
// ring buffer directly; instead it owns its own ring that is fed by
// cluster_event outbox rows (see core's OutboxClusterMerged wiring in the
// future). For now the handler is wired and returns whatever the ring holds,
// which is initially empty until core begins emitting outbox events.
package main

import (
	"net/http"
	"strconv"

	coreevent "capital_observatory/internal/core/event"
)

// clusterHandler returns the most recent N event-cluster snapshots.
// Implemented as a closure to capture the ring; methods could easily extract
// it onto a struct if more cluster-related endpoints appear.
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
