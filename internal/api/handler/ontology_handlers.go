// Ontology Phase 2 management API. Exposes CRUD for manual_relations and the
// accept / reject workflow for pending relation_suggestions.
package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"

	"sonde/internal/core/ontology"
)

// ontologyStore wraps the handles the ontology management API handlers need:
// the relation store (PostgreSQL-backed) and an optional cluster-ring reader
// from core process. Since cmd/api is its own binary, it instantiates its
// own ontology.Store / RelationManager from the pgxpool.
type ontologyStore struct {
	db          *pgxpool.Pool
	store       *ontology.Store
	relationMgr *ontology.RelationManager
}

// newOntologyStore builds an ontologyStore from a database pool.
func newOntologyStore(db *pgxpool.Pool) *ontologyStore {
	store := ontology.NewStore(db)
	return &ontologyStore{
		db:          db,
		store:       store,
		relationMgr: ontology.NewRelationManager(store),
	}
}

// ------- /api/ontology/relations/ -------

// ontologyRelationsHandler dispatches:
//
//	GET  /api/ontology/relations/         → list (optionally filtered by entity)
//	POST /api/ontology/relations/         → create (admin)
func (s *ontologyStore) ontologyRelationsHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listRelations(w, r)
	case http.MethodPost:
		s.createRelation(w, r)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET or POST required"})
	}
}

// ontologyRelationByIDHandler dispatches:
//
//	PUT    /api/ontology/relations/{id}    → update (admin)
//	DELETE /api/ontology/relations/{id}    → delete (admin)
//	GET    /api/ontology/relations/{id}    → get by ID
func (s *ontologyStore) ontologyRelationByIDHandler(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/ontology/relations/")
	id = strings.TrimSpace(id)
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "relation_id required"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.getRelationByID(w, r, id)
	case http.MethodPut:
		s.updateRelation(w, r, id)
	case http.MethodDelete:
		s.deleteRelation(w, r, id)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET, PUT, or DELETE required"})
	}
}

func (s *ontologyStore) listRelations(w http.ResponseWriter, r *http.Request) {
	entityID := r.URL.Query().Get("entity_id")

	relations, err := s.relationMgr.ListAllManualRelations(r.Context(), entityID)
	if err != nil {
		log.Error().Err(err).Str("entity_id", entityID).Msg("list manual relations failed")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	if relations == nil {
		relations = []ontology.ManualRelation{}
	}
	writeJSON(w, http.StatusOK, relations)
}

func (s *ontologyStore) getRelationByID(w http.ResponseWriter, r *http.Request, id string) {
	rel, err := s.relationMgr.GetByID(r.Context(), id)
	if err != nil {
		log.Error().Err(err).Str("relation_id", id).Msg("get manual relation failed")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	if rel == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "relation not found"})
		return
	}
	writeJSON(w, http.StatusOK, rel)
}

func (s *ontologyStore) createRelation(w http.ResponseWriter, r *http.Request) {
	var input ontology.RelationInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if input.SourceID == "" || input.TargetID == "" || input.RelationType == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "source_id, target_id, and relation_type required"})
		return
	}
	userID := r.Header.Get("X-User-ID")
	if userID == "" {
		userID = "api"
	}
	input.UserID = userID

	rel, err := s.relationMgr.Create(r.Context(), input)
	if err != nil {
		log.Error().Err(err).Interface("input", input).Msg("create manual relation failed")
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, rel)
}

func (s *ontologyStore) updateRelation(w http.ResponseWriter, r *http.Request, id string) {
	var updates map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	userID := r.Header.Get("X-User-ID")
	if userID == "" {
		userID = "api"
	}

	rel, err := s.relationMgr.Update(r.Context(), id, updates, userID)
	if err != nil {
		log.Error().Err(err).Str("relation_id", id).Msg("update manual relation failed")
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, rel)
}

func (s *ontologyStore) deleteRelation(w http.ResponseWriter, r *http.Request, id string) {
	userID := r.Header.Get("X-User-ID")
	if userID == "" {
		userID = "api"
	}
	if err := s.relationMgr.Delete(r.Context(), id, userID); err != nil {
		log.Error().Err(err).Str("relation_id", id).Msg("delete manual relation failed")
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted", "relation_id": id})
}

// ------- /api/ontology/candidates/ -------

// ontologyCandidatesHandler dispatches:
//
//	GET /api/ontology/candidates/            → list pending suggestions
//	GET /api/ontology/candidates/{id}        → get single pending suggestion
func (s *ontologyStore) ontologyCandidatesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET required"})
		return
	}
	idStr := r.PathValue("id")
	if idStr == "" {
		rest := strings.TrimPrefix(r.URL.Path, "/api/ontology/candidates/")
		if rest != "" && !strings.Contains(rest, "/") {
			idStr = rest
		}
	}
	if idStr == "" {
		// List all pending suggestions.
		suggestions, err := s.store.ListPendingSuggestions(r.Context())
		if err != nil {
			log.Error().Err(err).Msg("list pending suggestions failed")
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		if suggestions == nil {
			suggestions = []ontology.PendingSuggestion{}
		}
		writeJSON(w, http.StatusOK, suggestions)
		return
	}

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "candidate id must be numeric"})
		return
	}
	suggestion, err := s.store.GetPendingSuggestionByID(r.Context(), id)
	if err != nil {
		log.Error().Err(err).Int64("id", id).Msg("get pending suggestion failed")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	if suggestion == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "candidate not found or not pending"})
		return
	}
	writeJSON(w, http.StatusOK, suggestion)
}

// ontologyCandidateActionHandler dispatches:
//
//	POST /api/ontology/candidates/{id}/accept → accept (admin)
//	POST /api/ontology/candidates/{id}/reject → reject (admin)
func (s *ontologyStore) ontologyCandidateActionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST required"})
		return
	}

	rest := strings.TrimPrefix(r.URL.Path, "/api/ontology/candidates/")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "path must be /api/ontology/candidates/{id}/{accept|reject}",
		})
		return
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "candidate id must be numeric"})
		return
	}
	action := parts[1]
	userID := r.Header.Get("X-User-ID")
	if userID == "" {
		userID = "api"
	}

	switch action {
	case "accept":
		s.acceptCandidate(w, r, id, userID)
	case "reject":
		var req struct {
			Reason string `json:"reason"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		s.rejectCandidate(w, r, id, req.Reason, userID)
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "action must be 'accept' or 'reject'"})
	}
}

func (s *ontologyStore) acceptCandidate(w http.ResponseWriter, r *http.Request, id int64, userID string) {
	suggestion, err := s.store.GetPendingSuggestionByID(r.Context(), id)
	if err != nil {
		log.Error().Err(err).Int64("id", id).Msg("get pending suggestion for accept failed")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	if suggestion == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "candidate not found or not pending"})
		return
	}

	rel, err := s.relationMgr.AcceptSuggestionByCandidate(r.Context(), *suggestion, userID)
	if err != nil {
		if errors.Is(err, ontology.ErrAlreadyReviewed) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "candidate already reviewed"})
			return
		}
		log.Error().Err(err).Int64("id", id).Msg("accept candidate failed")
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":      "accepted",
		"relation_id": rel.RelationID,
	})
}

// ------- /api/ontology/discover -------

// ontologyDiscoverHandler triggers the statistical candidate finder on demand.
//
//	POST /api/ontology/discover  body: {"entity_pairs":[["btc","gld"], ...]}
//
// Runs CandidateFinder.Discover over each pair and returns the merged
// candidates sorted by |correlation|.
func (s *ontologyStore) ontologyDiscoverHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST required"})
		return
	}

	var req struct {
		EntityPairs [][2]string `json:"entity_pairs"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if len(req.EntityPairs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "entity_pairs required"})
		return
	}

	finder := ontology.NewCandidateFinder(s.store, ontology.DefaultCandidateConfig())
	finder.SetEntityResolver(s.store)
	finder.SetAPI(ontology.ObservationAPI{
		Fetch: s.store.FetchObservations,
	})
	candidates, err := finder.Discover(r.Context(), req.EntityPairs)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := s.store.SaveCandidates(r.Context(), candidates); err != nil {
		log.Error().Err(err).Msg("persist discovered ontology candidates failed")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to persist candidates"})
		return
	}
	if candidates == nil {
		candidates = []ontology.Candidate{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"candidates": candidates,
		"count":      len(candidates),
	})
}

func (s *ontologyStore) rejectCandidate(w http.ResponseWriter, r *http.Request, id int64, reason, userID string) {
	if err := s.store.RejectSuggestion(r.Context(), id, reason, userID); err != nil {
		if err == ontology.ErrSuggestionNotPending {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "candidate not found or not pending"})
			return
		}
		log.Error().Err(err).Int64("id", id).Msg("reject candidate failed")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "rejected"})
}
