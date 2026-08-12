package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type rulesFakeDB struct {
	execTag  pgconn.CommandTag
	execErr  error
	execArgs []any
}

func (f *rulesFakeDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("unexpected Query")
}

func (f *rulesFakeDB) QueryRow(context.Context, string, ...any) pgx.Row {
	return errRow{err: errors.New("unexpected QueryRow")}
}

func (f *rulesFakeDB) Exec(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
	f.execArgs = append([]any(nil), args...)
	return f.execTag, f.execErr
}

type rulesFakeTx struct {
	rows       []pgx.Row
	execTags   []pgconn.CommandTag
	execErrs   []error
	queryArgs  [][]any
	execArgs   [][]any
	committed  bool
	rolledBack bool
}

func (f *rulesFakeTx) QueryRow(_ context.Context, _ string, args ...any) pgx.Row {
	f.queryArgs = append(f.queryArgs, append([]any(nil), args...))
	if len(f.rows) == 0 {
		return errRow{err: errors.New("unexpected QueryRow")}
	}
	row := f.rows[0]
	f.rows = f.rows[1:]
	return row
}

func (f *rulesFakeTx) Exec(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
	f.execArgs = append(f.execArgs, append([]any(nil), args...))
	var tag pgconn.CommandTag
	if len(f.execTags) > 0 {
		tag = f.execTags[0]
		f.execTags = f.execTags[1:]
	}
	if len(f.execErrs) == 0 {
		return tag, nil
	}
	err := f.execErrs[0]
	f.execErrs = f.execErrs[1:]
	return tag, err
}

func (f *rulesFakeTx) Commit(context.Context) error {
	f.committed = true
	return nil
}

func (f *rulesFakeTx) Rollback(context.Context) error {
	f.rolledBack = true
	return nil
}

func txRow(values ...any) pgx.Row {
	return fakeRow{r: newFakeRows(values)}
}

func restoreRequest(id, version string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/rules/"+id+"/restore/"+version, nil)
	req.SetPathValue("id", id)
	req.SetPathValue("version", version)
	return req
}

func TestRulesRestoreCreatesNewPhysicalRowAtomically(t *testing.T) {
	tx := &rulesFakeTx{
		rows: []pgx.Row{
			txRow("rule", "metric", "threshold", "warning", []byte(`{"limit": 2}`), "desc", "user_override", true, false),
			txRow(4),
			txRow(22),
		},
		execTags: []pgconn.CommandTag{pgconn.NewCommandTag("UPDATE 1")},
	}
	store := &rulesStore{
		db: &rulesFakeDB{},
		beginTx: func(context.Context) (rulesTx, error) {
			return tx, nil
		},
	}

	rec := httptest.NewRecorder()
	store.restoreHandler(rec, restoreRequest("17", "2"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["id"] != float64(22) || body["version"] != float64(5) {
		t.Fatalf("response = %#v, want new id 22 and version 5", body)
	}
	if !tx.committed {
		t.Fatal("restore transaction was not committed")
	}
	if len(tx.queryArgs) != 3 || tx.queryArgs[0][0] != 17 || tx.queryArgs[0][1] != 2 {
		t.Fatalf("historical lookup args = %#v, want physical id 17 and version 2", tx.queryArgs)
	}
	if len(tx.execArgs) != 2 || tx.execArgs[0][0] != "rule" || tx.execArgs[0][1] != "metric" {
		t.Fatalf("close-current args = %#v, want logical rule identity", tx.execArgs)
	}
	if tx.execArgs[1][0] != 22 {
		t.Fatalf("restore audit args = %#v, want new physical id 22", tx.execArgs[1])
	}
}

func TestRulesRestoreRollsBackWhenInsertFails(t *testing.T) {
	tx := &rulesFakeTx{
		rows: []pgx.Row{
			txRow("rule", "metric", "threshold", "warning", []byte(`{}`), "", "system_default", false, true),
			txRow(3),
			errRow{err: errors.New("insert failed")},
		},
		execTags: []pgconn.CommandTag{pgconn.NewCommandTag("UPDATE 1")},
	}
	store := &rulesStore{
		db: &rulesFakeDB{},
		beginTx: func(context.Context) (rulesTx, error) {
			return tx, nil
		},
	}

	rec := httptest.NewRecorder()
	store.restoreHandler(rec, restoreRequest("9", "1"))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if tx.committed {
		t.Fatal("failed restore transaction must not commit")
	}
	if !tx.rolledBack {
		t.Fatal("failed restore transaction was not rolled back")
	}
}

func TestRulesPatchRejectsHistoricalVersion(t *testing.T) {
	tx := &rulesFakeTx{
		rows: []pgx.Row{errRow{err: pgx.ErrNoRows}},
	}
	store := &rulesStore{
		db: &rulesFakeDB{execTag: pgconn.NewCommandTag("UPDATE 0")},
		beginTx: func(context.Context) (rulesTx, error) {
			return tx, nil
		},
	}
	req := httptest.NewRequest(http.MethodPatch, "/api/rules/7", strings.NewReader(`{"enabled":false}`))
	req.SetPathValue("id", "7")
	rec := httptest.NewRecorder()

	store.patchHandler(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if len(tx.execArgs) != 0 {
		t.Fatalf("expected no writes for historical id, got %d", len(tx.execArgs))
	}
	if tx.committed {
		t.Fatal("transaction with 0-row UPDATE must not commit")
	}
	if !tx.rolledBack {
		t.Fatal("transaction with 0-row UPDATE must be rolled back")
	}
}

func TestRulesPatchCreatesVersionAndAuditAtomically(t *testing.T) {
	tx := &rulesFakeTx{
		rows: []pgx.Row{
			txRow("rule", "metric", "threshold", "warning", []byte(`{"limit":2}`), nil,
				true, "system_default", false, 4),
			txRow(23),
		},
		execTags: []pgconn.CommandTag{pgconn.NewCommandTag("UPDATE 1")},
	}
	store := &rulesStore{
		db: &rulesFakeDB{},
		beginTx: func(context.Context) (rulesTx, error) {
			return tx, nil
		},
	}
	req := httptest.NewRequest(http.MethodPatch, "/api/rules/17", strings.NewReader(`{"enabled":false}`))
	req.SetPathValue("id", "17")
	rec := httptest.NewRecorder()

	store.patchHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !tx.committed {
		t.Fatal("patch transaction was not committed")
	}
	if len(tx.queryArgs) != 2 || tx.queryArgs[1][9] != 5 {
		t.Fatalf("query args = %#v, want inserted version 5", tx.queryArgs)
	}
	if len(tx.execArgs) != 2 || tx.execArgs[0][0] != 17 || tx.execArgs[1][0] != 23 {
		t.Fatalf("exec args = %#v, want close old id 17 and audit new id 23", tx.execArgs)
	}
}
