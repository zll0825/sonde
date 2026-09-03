package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type controlFake struct {
	caps      []byte
	lookupErr error
	execs     int
	execErr   error
}

func (f *controlFake) QueryRow(context.Context, string, ...any) pgx.Row {
	if f.lookupErr != nil {
		return errRow{err: f.lookupErr}
	}
	if f.caps == nil && f.lookupErr == nil {
		return errRow{err: pgx.ErrNoRows}
	}
	return fakeRow{r: &fakeRows{rows: [][]any{{f.caps}}, idx: 0}}
}

func (f *controlFake) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	f.execs++
	if f.execErr != nil {
		return pgconn.CommandTag{}, f.execErr
	}
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}

func TestBackfillHandler_RejectsMissingCapability(t *testing.T) {
	body := `{"plugin_id":"plg_etf","window_start":1,"window_end":2}`
	cases := []struct {
		name   string
		db     *controlFake
		status int
		execs  int
	}{
		{"not registered", &controlFake{}, http.StatusNotFound, 0},
		{"omitted bit", &controlFake{caps: []byte(`{}`)}, http.StatusForbidden, 0},
		{"explicit false", &controlFake{caps: []byte(`{"windowed_backfill":false}`)}, http.StatusForbidden, 0},
		{"allowed", &controlFake{caps: []byte(`{"windowed_backfill":true}`)}, http.StatusAccepted, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/control/backfill", bytes.NewReader([]byte(body)))
			rec := httptest.NewRecorder()
			backfillHandler(tc.db).ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d body=%s", rec.Code, tc.status, rec.Body.String())
			}
			if tc.db.execs != tc.execs {
				t.Fatalf("inserts = %d, want %d", tc.db.execs, tc.execs)
			}
		})
	}
}

func TestSyncHandler_NotGatedOnBackfill(t *testing.T) {
	db := &controlFake{caps: []byte(`{}`)}
	req := httptest.NewRequest(http.MethodPost, "/api/control/sync", bytes.NewReader([]byte(`{"plugin_id":"plg_etf"}`)))
	rec := httptest.NewRecorder()
	syncHandler(db).ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 body=%s", rec.Code, rec.Body.String())
	}
	if db.execs != 1 {
		t.Fatalf("sync inserts = %d, want 1", db.execs)
	}
}

func TestBackfillHandler_JSONErrorShape(t *testing.T) {
	db := &controlFake{caps: []byte(`{}`)}
	req := httptest.NewRequest(http.MethodPost, "/api/control/backfill", bytes.NewReader([]byte(`{"plugin_id":"macro","window_start":1,"window_end":2}`)))
	rec := httptest.NewRecorder()
	backfillHandler(db).ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["error"] != "plugin does not declare windowedBackfill" {
		t.Fatalf("error = %q", body["error"])
	}
}

func TestBearerActorIdentifier_NeverContainsBearerMaterial(t *testing.T) {
	secret := "super-secret-token"
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Bearer "+secret)
	actor := bearerActorIdentifier(req)
	if actor != "authenticated_api_client" {
		t.Fatalf("actor = %q, want authenticated_api_client", actor)
	}
	if strings.Contains(actor, secret) || strings.Contains(strings.ToLower(actor), "bearer") {
		t.Fatalf("actor leaked credential material: %q", actor)
	}

	anon := bearerActorIdentifier(httptest.NewRequest(http.MethodPost, "/", nil))
	if anon != "anonymous" {
		t.Fatalf("anonymous actor = %q", anon)
	}
}
