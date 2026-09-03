package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"capital_observatory/pkg/model"
)

type fakeFeedbackStore struct {
	savedAlertID string
	saveCalls    int
}

func (f *fakeFeedbackStore) SaveFeedback(_ context.Context, alertID, _, _, _, _ string) error {
	f.savedAlertID = alertID
	f.saveCalls++
	return nil
}

func (f *fakeFeedbackStore) ListFeedbacks(context.Context, string) ([]model.ResearchFeedback, error) {
	return nil, nil
}

func TestResearchFeedbackRejectsMismatchedBodyAlertID(t *testing.T) {
	store := &fakeFeedbackStore{}
	req := httptest.NewRequest(http.MethodPost, "/api/research/path-alert/feedback",
		strings.NewReader(`{"alert_id":"other-alert","verdict":"irrelevant"}`))
	req.SetPathValue("id", "path-alert")
	rec := httptest.NewRecorder()

	handleSaveFeedback(rec, req, store)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if store.saveCalls != 0 {
		t.Fatalf("SaveFeedback called %d times, want 0", store.saveCalls)
	}
}

func TestResearchFeedbackUsesPathAlertID(t *testing.T) {
	store := &fakeFeedbackStore{}
	req := httptest.NewRequest(http.MethodPost, "/api/research/path-alert/feedback",
		strings.NewReader(`{"verdict":"worth_researching"}`))
	req.SetPathValue("id", "path-alert")
	rec := httptest.NewRecorder()

	handleSaveFeedback(rec, req, store)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}
	if store.savedAlertID != "path-alert" {
		t.Fatalf("saved alert = %q, want path-alert", store.savedAlertID)
	}
}
