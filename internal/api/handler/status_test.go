package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ---- fake statusQuerier -----------------------------------------------------
// A minimal in-memory stand-in for *pgxpool.Pool / pgxmock. The five status
// queries run in a fixed order, so stubs are consumed FIFO and identified by
// their position. Keeps cmd/api free of test-only module dependencies (the
// project pins go 1.22 + pgx v5.6.0; pgxmock would force upgrades).

type queryStub struct {
	rows *fakeRows
	err  error
}

type fakeDB struct {
	stubs    []queryStub
	lastSQL  string
	lastArgs []any
}

// stub appends a canned result; the i-th stub answers the i-th query call.
func (f *fakeDB) stub(rows *fakeRows) *fakeDB {
	f.stubs = append(f.stubs, queryStub{rows: rows})
	return f
}

func (f *fakeDB) stubErr(err error) *fakeDB {
	f.stubs = append(f.stubs, queryStub{err: err})
	return f
}

// exhausted fails the test if any stub was left unconsumed.
func (f *fakeDB) exhausted(t *testing.T) {
	t.Helper()
	if len(f.stubs) != 0 {
		t.Errorf("%d query stub(s) left unconsumed", len(f.stubs))
	}
}

func (f *fakeDB) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	f.lastSQL = sql
	f.lastArgs = append([]any(nil), args...)
	if len(f.stubs) == 0 {
		return nil, fmt.Errorf("no stub for query: %s", sql)
	}
	s := f.stubs[0]
	f.stubs = f.stubs[1:]
	if s.err != nil {
		return nil, s.err
	}
	return s.rows, nil
}

func (f *fakeDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	rows, err := f.Query(ctx, sql, args...)
	if err != nil {
		return errRow{err: err}
	}
	return fakeRow{r: rows.(*fakeRows)}
}

// fakeRow adapts *fakeRows to pgx.Row with pgx's QueryRow semantics: the
// first row is consumed by Next() before Scan, and no rows yields ErrNoRows.
type fakeRow struct{ r *fakeRows }

func (fr fakeRow) Scan(dest ...any) error {
	if !fr.r.Next() {
		if err := fr.r.Err(); err != nil {
			return err
		}
		return pgx.ErrNoRows
	}
	return fr.r.Scan(dest...)
}

// errRow adapts an error to pgx.Row for QueryRow callers.
type errRow struct{ err error }

func (r errRow) Scan(dest ...any) error { return r.err }

// fakeRows implements pgx.Rows over a fixed [][]any. NULL is expressed as a
// nil cell and leaves the destination at its zero value.
type fakeRows struct {
	rows [][]any
	idx  int
}

func newFakeRows(rows ...[]any) *fakeRows { return &fakeRows{rows: rows} }

func (r *fakeRows) Next() bool {
	if r.idx >= len(r.rows) {
		return false
	}
	r.idx++
	return true
}

func (r *fakeRows) Scan(dest ...any) error {
	if r.idx == 0 || r.idx > len(r.rows) {
		return pgx.ErrNoRows
	}
	row := r.rows[r.idx-1]
	if len(row) != len(dest) {
		return fmt.Errorf("scan: %d columns for %d destinations", len(row), len(dest))
	}
	for i, d := range dest {
		v := row[i]
		if v == nil {
			continue // leave the destination zero value (NULL semantics)
		}
		if err := assignScan(d, v); err != nil {
			return err
		}
	}
	return nil
}

func (r *fakeRows) Values() ([]any, error)                       { return nil, errors.New("Values not implemented") }
func (r *fakeRows) RawValues() [][]byte                          { return nil }
func (r *fakeRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *fakeRows) Err() error                                   { return nil }
func (r *fakeRows) Close()                                       {}
func (r *fakeRows) Conn() *pgx.Conn                              { return nil }

// assignScan mirrors pgx's minimal assignment rules: exact type match first,
// then a Scan(any) receiver (pgtype.Timestamptz for nullable timestamps).
func assignScan(dst, src any) error {
	dv := reflect.ValueOf(dst)
	if dv.Kind() != reflect.Ptr || dv.IsNil() {
		return fmt.Errorf("scan destination must be a non-nil pointer, got %T", dst)
	}
	ev := dv.Elem()
	if sv := reflect.ValueOf(src); sv.Type().AssignableTo(ev.Type()) {
		ev.Set(sv)
		return nil
	}
	if sc, ok := dst.(interface{ Scan(any) error }); ok {
		return sc.Scan(src)
	}
	return fmt.Errorf("cannot scan %T into %T", src, dst)
}

// ---- tests ------------------------------------------------------------------

func TestLoadStatus_EmptyDB(t *testing.T) {
	db := &fakeDB{}
	db.stub(newFakeRows())                  // plugins
	db.stub(newFakeRows())                  // metric definitions
	db.stub(newFakeRows())                  // latest observations
	db.stub(newFakeRows())                  // series
	db.stub(newFakeRows([]any{0, 0, 0, 0})) // today's alert counts
	defer db.exhausted(t)

	p, err := loadStatus(context.Background(), db, time.Now())
	if err != nil {
		t.Fatalf("loadStatus: %v", err)
	}
	if p.Plugins == nil || len(p.Plugins) != 0 {
		t.Errorf("plugins = %#v, want empty non-nil slice", p.Plugins)
	}
	if p.Metrics == nil || len(p.Metrics) != 0 {
		t.Errorf("metrics = %#v, want empty non-nil slice", p.Metrics)
	}
	if p.Budget.Today != 0 || p.Budget.Limit != 10 {
		t.Errorf("budget = %+v, want today 0 limit 10", p.Budget)
	}
	if p.LatestDataAt != nil {
		t.Errorf("latest_data_at = %v, want nil on empty db", p.LatestDataAt)
	}
	if p.ExpectedPlugins == nil || len(p.ExpectedPlugins) != 0 {
		t.Errorf("expected_plugins = %#v, want empty non-nil slice", p.ExpectedPlugins)
	}
}

func TestLoadStatus_JoinAndFreshness(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	db := &fakeDB{}
	db.stub(newFakeRows( // plugins
		[]any{"plg_etf", "etf", true, true, "running", now.Add(-2 * time.Hour), 125, 3, "", 0, []byte(`{}`), []byte(`{}`)},
		[]any{"plg_crypto", "crypto", true, false, "running", nil, 320, 0, "coingecko/btc.price: timeout", 2, []byte(`{"windowed_backfill":true,"max_backfill_days":365,"requires_secrets":["COINGECKO_KEY"],"mock_available":true}`), []byte(`{"secret.COINGECKO_KEY":"missing"}`)},
	))
	db.stub(newFakeRows( // metric definitions
		[]any{"gld.ass.price", "mtr_aaa", "GLD Price", "USD", "daily"},
		[]any{"btc.usd.price", "mtr_bbb", "BTC Price", "USD", "hourly"},
		[]any{"walcl", "mtr_ccc", "WALCL", "USD bn", "weekly"},
	))
	// mtr_ccc deliberately has no observation: the metric must still appear,
	// red, with a null value — missing data is itself a state to surface.
	db.stub(newFakeRows( // latest observations
		[]any{"mtr_aaa", now.Add(-5 * 24 * time.Hour), 310.5, "yahoo", "delayed"},
		[]any{"mtr_bbb", now.Add(-6 * time.Hour), 12345.6, "binance", "realtime"},
	))
	db.stub(newFakeRows( // series
		[]any{"mtr_aaa", now.Add(-5 * 24 * time.Hour), 310.5},
		[]any{"mtr_aaa", now.Add(-4 * 24 * time.Hour), 312.0},
		[]any{"mtr_bbb", now.Add(-6 * time.Hour), 12300.0},
	))
	db.stub(newFakeRows([]any{2, 3, 1, 4})) // today's alert counts
	defer db.exhausted(t)

	p, err := loadStatus(context.Background(), db, now)
	if err != nil {
		t.Fatalf("loadStatus: %v", err)
	}

	if len(p.Plugins) != 2 {
		t.Fatalf("plugins len = %d, want 2", len(p.Plugins))
	}
	if !p.Plugins[0].Connected || !p.Plugins[0].Healthy {
		t.Errorf("plugins[0] = %+v, want connected and healthy", p.Plugins[0])
	}
	if !p.Plugins[1].Connected || p.Plugins[1].Healthy || p.Plugins[1].LastCollectAt != nil {
		t.Errorf("plugins[1] = %+v, want connected, unhealthy with nil last_collect_at", p.Plugins[1])
	}
	if p.Plugins[1].LastCollectError == "" || p.Plugins[1].ConsecutiveErrors != 2 || p.Plugins[1].LastCollectDurationMs != 320 {
		t.Errorf("plugins[1] collection health = %+v", p.Plugins[1])
	}
	if p.Plugins[0].LastCollectAt == nil || !p.Plugins[0].LastCollectAt.Equal(now.Add(-2*time.Hour)) {
		t.Errorf("plugins[0].LastCollectAt = %v, want 2h ago", p.Plugins[0].LastCollectAt)
	}
	if len(p.ExpectedPlugins) != 2 {
		t.Fatalf("expected_plugins len = %d, want 2", len(p.ExpectedPlugins))
	}
	if p.ExpectedPlugins[1].Name != "crypto" || !p.ExpectedPlugins[1].Capabilities.WindowedBackfill {
		t.Errorf("expected_plugins[1] = %+v", p.ExpectedPlugins[1])
	}
	if p.ExpectedPlugins[1].SecretsPresent["COINGECKO_KEY"] {
		t.Errorf("secrets_present should be false when the plugin reports it missing: %+v", p.ExpectedPlugins[1].SecretsPresent)
	}
	if p.ExpectedPlugins[1].Ready {
		t.Errorf("expected_plugins[1].ready = true, want false when the plugin reports a required secret missing")
	}
	if !p.ExpectedPlugins[0].Ready {
		t.Errorf("expected_plugins[0].ready = false, want true when no secrets required")
	}

	if p.Budget.Today != 2 {
		t.Errorf("budget today = %d, want 2", p.Budget.Today)
	}
	if p.Budget.RealToday != 2 || p.Budget.MockToday != 3 || p.Budget.TestToday != 1 || p.Budget.UnknownToday != 4 {
		t.Errorf("detailed budget = %+v", p.Budget)
	}
	if p.Budget.RealLimit != 10 || p.Budget.RealOverBudget {
		t.Errorf("real budget = %+v, want limit 10 and not over", p.Budget)
	}

	if len(p.Metrics) != 3 {
		t.Fatalf("metrics len = %d, want 3", len(p.Metrics))
	}

	// daily metric 5d stale → yellow (green ≤ 4d, yellow ≤ 7d)
	if p.Metrics[0].Freshness != freshnessYellow {
		t.Errorf("metrics[0].Freshness = %q, want yellow", p.Metrics[0].Freshness)
	}
	if p.Metrics[0].LatestValue == nil || *p.Metrics[0].LatestValue != 310.5 {
		t.Errorf("metrics[0].LatestValue = %v, want 310.5", p.Metrics[0].LatestValue)
	}
	if p.Metrics[0].Provider != "yahoo" || p.Metrics[0].Grade != "delayed" {
		t.Errorf("metrics[0] provider/grade = %q/%q, want yahoo/delayed", p.Metrics[0].Provider, p.Metrics[0].Grade)
	}
	// series: newest 30, ascending by time
	if len(p.Metrics[0].Series) != 2 || p.Metrics[0].Series[0].V != 310.5 || p.Metrics[0].Series[1].V != 312.0 {
		t.Errorf("metrics[0].Series = %+v, want 2 ascending points 310.5, 312.0", p.Metrics[0].Series)
	}

	// hourly metric 6h stale → yellow (green ≤ 3h, yellow ≤ 12h)
	if p.Metrics[1].Freshness != freshnessYellow {
		t.Errorf("metrics[1].Freshness = %q, want yellow", p.Metrics[1].Freshness)
	}

	// no-data metric → red, null value, empty (non-nil) series
	if p.Metrics[2].Freshness != freshnessRed {
		t.Errorf("metrics[2].Freshness = %q, want red", p.Metrics[2].Freshness)
	}
	if p.Metrics[2].LatestValue != nil || p.Metrics[2].LatestAt != nil {
		t.Errorf("metrics[2].LatestValue/LatestAt = %v/%v, want nil", p.Metrics[2].LatestValue, p.Metrics[2].LatestAt)
	}
	if p.Metrics[2].Series == nil || len(p.Metrics[2].Series) != 0 {
		t.Errorf("metrics[2].Series = %#v, want empty non-nil slice", p.Metrics[2].Series)
	}

	// latest_data_at = max observation time across metrics (btc, 6h ago)
	if p.LatestDataAt == nil || !p.LatestDataAt.Equal(now.Add(-6*time.Hour)) {
		t.Errorf("latest_data_at = %v, want %v", p.LatestDataAt, now.Add(-6*time.Hour))
	}
}

// TestLoadStatus_SecretReadinessComesFromTheHeartbeat 锁住一个曾经长期误报的
// 结论来源：密钥就绪必须来自插件自己上报的 runtime，而不是 API 进程的环境变量。
//
// 旧实现是 os.Getenv(key)。密钥只存在于插件容器——compose 里 sonde-api 没有
// FRED_API_KEY——所以 macro / commodities 明明拿着真实密钥正常采集，首页却
// 一直显示「缺少密钥」。
func TestLoadStatus_SecretReadinessComesFromTheHeartbeat(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	caps := []byte(`{"windowed_backfill":true,"requires_secrets":["FRED_API_KEY"]}`)
	cases := []struct {
		name         string
		runtime      string
		wantReady    bool
		wantReported bool
		wantPresent  bool
	}{
		{
			name:         "plugin reports the secret present",
			runtime:      `{"secret.FRED_API_KEY":"present"}`,
			wantReady:    true,
			wantReported: true,
			wantPresent:  true,
		},
		{
			name:         "plugin reports the secret missing",
			runtime:      `{"secret.FRED_API_KEY":"missing"}`,
			wantReady:    false,
			wantReported: true,
			wantPresent:  false,
		},
		{
			// 尚未升级的插件、或还没心跳过的插件：不知道就不表态。
			// 把「不知道」显示成「缺失」正是上一版的错误。
			name:         "plugin has not reported yet",
			runtime:      `{}`,
			wantReady:    true,
			wantReported: false,
		},
		{
			name:         "runtime carries other keys only",
			runtime:      `{"circuit_state":"closed"}`,
			wantReady:    true,
			wantReported: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// 环境变量刻意设成与期望相反：结论必须完全来自 runtime。
			t.Setenv("FRED_API_KEY", "this-must-be-ignored")

			db := &fakeDB{}
			db.stub(newFakeRows(
				[]any{"plg_macro", "macro", true, true, "running", now.Add(-time.Hour), 10, 6, "", 0, caps, []byte(tc.runtime)},
			))
			db.stub(newFakeRows())
			db.stub(newFakeRows())
			db.stub(newFakeRows())
			db.stub(newFakeRows([]any{0, 0, 0, 0}))
			defer db.exhausted(t)

			p, err := loadStatus(context.Background(), db, now)
			if err != nil {
				t.Fatalf("loadStatus: %v", err)
			}
			if len(p.ExpectedPlugins) != 1 {
				t.Fatalf("expected_plugins len = %d, want 1", len(p.ExpectedPlugins))
			}
			exp := p.ExpectedPlugins[0]
			if exp.Ready != tc.wantReady {
				t.Errorf("ready = %v, want %v", exp.Ready, tc.wantReady)
			}
			got, reported := exp.SecretsPresent["FRED_API_KEY"]
			if reported != tc.wantReported {
				t.Fatalf("secrets_present reported = %v, want %v (map=%+v)", reported, tc.wantReported, exp.SecretsPresent)
			}
			if reported && got != tc.wantPresent {
				t.Errorf("secrets_present[FRED_API_KEY] = %v, want %v", got, tc.wantPresent)
			}
		})
	}
}

func TestQueryTodayAlertCounts_OnlyRealDrivesBudget(t *testing.T) {
	db := (&fakeDB{}).stub(newFakeRows([]any{11, 200, 300, 400}))
	defer db.exhausted(t)

	got, err := queryTodayAlertCounts(context.Background(), db)
	if err != nil {
		t.Fatalf("queryTodayAlertCounts: %v", err)
	}
	if got.Today != 11 || got.Limit != 10 || got.RealToday != 11 || !got.RealOverBudget {
		t.Errorf("real budget = %+v, want 11/10 over budget with compatibility aliases", got)
	}
	if got.MockToday != 200 || got.TestToday != 300 || got.UnknownToday != 400 {
		t.Errorf("categorized counts = %+v", got)
	}
	if !strings.Contains(db.lastSQL, "AND mode = 'live'") {
		t.Fatalf("budget SQL must exclude observe alerts: %s", db.lastSQL)
	}
}

// TestStatusHandler_EmptyDB_JSONShape exercises the full HTTP path (httptest,
// mirroring middleware_test.go) against an empty database: 200, frozen plus
// additive expected_plugins keys, arrays serialize as [] rather than null.
func TestStatusHandler_EmptyDB_JSONShape(t *testing.T) {
	db := &fakeDB{}
	db.stub(newFakeRows())                  // plugins
	db.stub(newFakeRows())                  // metric definitions
	db.stub(newFakeRows())                  // latest observations
	db.stub(newFakeRows())                  // series
	db.stub(newFakeRows([]any{0, 0, 0, 0})) // today's alert counts
	defer db.exhausted(t)

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	rec := httptest.NewRecorder()
	statusHandler(db).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q, want application/json", ct)
	}

	var body map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	for _, k := range []string{"plugins", "expected_plugins", "budget", "latest_data_at", "metrics"} {
		if _, ok := body[k]; !ok {
			t.Errorf("response missing key %q (body: %s)", k, rec.Body.String())
		}
	}
	var plugins []any
	if err := json.Unmarshal(body["plugins"], &plugins); err != nil || plugins == nil {
		t.Errorf("plugins = %s, want []", body["plugins"])
	}
	var expected []any
	if err := json.Unmarshal(body["expected_plugins"], &expected); err != nil || expected == nil {
		t.Errorf("expected_plugins = %s, want []", body["expected_plugins"])
	}
	var metrics []any
	if err := json.Unmarshal(body["metrics"], &metrics); err != nil || metrics == nil {
		t.Errorf("metrics = %s, want []", body["metrics"])
	}
	if string(body["latest_data_at"]) != "null" {
		t.Errorf("latest_data_at = %s, want null on empty db", body["latest_data_at"])
	}
}

// TestStatusHandler_DBError verifies the handler degrades to a 500 JSON error
// (never a raw SQL error leaking to the client) when a query fails.
func TestStatusHandler_DBError(t *testing.T) {
	db := &fakeDB{}
	db.stubErr(errors.New("connection reset"))
	defer db.exhausted(t)

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	rec := httptest.NewRecorder()
	statusHandler(db).ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("error body is not valid JSON: %v", err)
	}
	if body["error"] == "" {
		t.Errorf("error body missing message: %s", rec.Body.String())
	}
}
