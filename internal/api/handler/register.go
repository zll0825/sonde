package handler

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"

	coreevent "sonde/internal/core/event"
)

// Deps is the API process wiring passed from cmd/api.
type Deps struct {
	DB           *pgxpool.Pool
	ClusterStore *coreevent.ClusterSnapshotStore
}

// Register mounts all REST routes on mux. cmd/api only wires this.
func Register(mux *http.ServeMux, deps Deps) {
	db := deps.DB
	researchStore := newResearchFeedbackStore(db)

	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/api/alerts", alertsHandler(db))
	mux.HandleFunc("PATCH /api/alerts/{id}", patchAlertHandler(db))
	mux.HandleFunc("/api/status", statusHandler(db))
	mux.HandleFunc("/api/research/", researchHandler(db))
	mux.HandleFunc("/api/control/sync", syncHandler(db))
	mux.HandleFunc("/api/control/backfill", backfillHandler(db))
	mux.HandleFunc("GET /api/signal/quality/{metric_uid}", signalQualityHandler(db))

	ont := newOntologyStore(db)
	mux.HandleFunc("GET /api/ontology/relations/", ont.ontologyRelationsHandler)
	mux.HandleFunc("POST /api/ontology/relations/", ont.ontologyRelationsHandler)
	mux.HandleFunc("GET /api/ontology/relations/{id}", ont.ontologyRelationByIDHandler)
	mux.HandleFunc("PUT /api/ontology/relations/{id}", ont.ontologyRelationByIDHandler)
	mux.HandleFunc("DELETE /api/ontology/relations/{id}", ont.ontologyRelationByIDHandler)
	mux.HandleFunc("POST /api/ontology/discover", ont.ontologyDiscoverHandler)
	mux.HandleFunc("GET /api/ontology/candidates/", ont.ontologyCandidatesHandler)
	mux.HandleFunc("GET /api/ontology/candidates/{id}", ont.ontologyCandidatesHandler)
	mux.HandleFunc("POST /api/ontology/candidates/{id}/accept", ont.ontologyCandidateActionHandler)
	mux.HandleFunc("POST /api/ontology/candidates/{id}/reject", ont.ontologyCandidateActionHandler)

	rules := newRulesStore(db)
	mux.HandleFunc("GET /api/rules/", rules.listHandler)
	mux.HandleFunc("PATCH /api/rules/{id}", rules.patchHandler)
	mux.HandleFunc("POST /api/rules/{id}/restore/{version}", rules.restoreHandler)
	mux.HandleFunc("GET /api/rules/{id}/history", rules.historyHandler)

	mux.HandleFunc("POST /api/research/{id}/feedback", researchFeedbackHandler(researchStore))
	mux.HandleFunc("GET /api/research/feedback", researchFeedbackHandler(researchStore))

	if deps.ClusterStore != nil {
		mux.HandleFunc("GET /api/clusters/", clusterHandlerWithStore(deps.ClusterStore))
		mux.HandleFunc("GET /api/clusters/{id}", clusterHandlerWithID(deps.ClusterStore))
	} else {
		log.Info().Msg("CLUSTER_SNAPSHOTS_FILE unset; serving empty cluster list")
		clusterRing := coreevent.NewClusterRing(200)
		mux.HandleFunc("GET /api/clusters/", clusterHandler(clusterRing))
	}
}
