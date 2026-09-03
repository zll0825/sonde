package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"capital_observatory/pkg/model"
)

// statusQuerier is the minimal query surface statusHandler needs. Production
// passes *pgxpool.Pool; tests pass the handwritten fake in status_test.go
// (pgxmock is rejected: its v5 needs go ≥1.25 + pgx ≥5.9.2 and would break
// the go 1.22 Docker/CI builds). The narrow interface keeps the handler
// unit-testable without a live database.
type statusQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// D1 response shapes for GET /api/status. The JSON field names are the
// frontend contract — do not rename without updating web/index.html.
// Slices are always initialized so an empty database serializes as [].
type statusPayload struct {
	Plugins         []pluginStatus         `json:"plugins"`
	ExpectedPlugins []expectedPluginStatus `json:"expected_plugins"`
	Budget          budgetStatus           `json:"budget"`
	LatestDataAt    *time.Time             `json:"latest_data_at"` // newest observation across all metrics; null when empty
	Metrics         []metricStatus         `json:"metrics"`
}

// expectedPluginStatus is declared topology from the last registration,
// including plugins that are currently disconnected.
type expectedPluginStatus struct {
	ID             string           `json:"id"`
	Name           string           `json:"name"`
	Capabilities   capabilityStatus `json:"capabilities"`
	SecretsPresent map[string]bool  `json:"secrets_present,omitempty"`
}

type capabilityStatus struct {
	WindowedBackfill bool     `json:"windowed_backfill"`
	MaxBackfillDays  int      `json:"max_backfill_days"`
	RequiresSecrets  []string `json:"requires_secrets"`
	MockAvailable    bool     `json:"mock_available"`
}

type pluginStatus struct {
	ID                    string     `json:"id"`
	Name                  string     `json:"name"`
	Connected             bool       `json:"connected"`
	Healthy               bool       `json:"healthy"`
	State                 string     `json:"state"`
	LastCollectAt         *time.Time `json:"last_collect_at"`
	LastCollectDurationMs int        `json:"last_collect_duration_ms"`
	LastCollectCount      int        `json:"last_collect_count"`
	LastCollectError      string     `json:"last_collect_error"`
	ConsecutiveErrors     int        `json:"consecutive_errors"`
}

type budgetStatus struct {
	Today          int  `json:"today"` // compatibility alias for real_today
	Limit          int  `json:"limit"` // compatibility alias for real_limit
	RealToday      int  `json:"real_today"`
	MockToday      int  `json:"mock_today"`
	TestToday      int  `json:"test_today"`
	UnknownToday   int  `json:"unknown_today"`
	RealLimit      int  `json:"real_limit"`
	RealOverBudget bool `json:"real_over_budget"`
}

// metricStatus carries the current definition joined with its newest
// observation. LatestValue/LatestAt are nil (JSON null) when the metric has
// no data yet — missing data is itself a state to surface (red light).
type metricStatus struct {
	MetricID    string        `json:"metric_id"`
	UID         string        `json:"uid"`
	Name        string        `json:"name"`
	Unit        string        `json:"unit"`
	Frequency   string        `json:"frequency"`
	LatestValue *float64      `json:"latest_value"`
	LatestAt    *time.Time    `json:"latest_at"`
	Provider    string        `json:"provider"`
	Grade       string        `json:"grade"`
	Freshness   string        `json:"freshness"`
	Series      []seriesPoint `json:"series"` // newest 30 points, ascending by time
}

type seriesPoint struct {
	T time.Time `json:"t"`
	V float64   `json:"v"`
}

// loadStatus assembles the full /api/status payload with the five queries
// from design D3 and joins them in memory by metric uid. now is injected so
// tests can pin the freshness clock. All timestamps are normalized to UTC
// (ISO8601 with Z) so the frontend computes relative time from Date.now()
// without timezone ambiguity.
func loadStatus(ctx context.Context, db statusQuerier, now time.Time) (*statusPayload, error) {
	plugins, expected, err := queryPlugins(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("query plugins: %w", err)
	}
	defs, err := queryCurrentMetrics(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("query metric definitions: %w", err)
	}
	latest, err := queryLatestObservations(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("query latest observations: %w", err)
	}
	series, err := queryObservationSeries(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("query observation series: %w", err)
	}
	budget, err := queryTodayAlertCounts(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("query today alert counts: %w", err)
	}

	p := &statusPayload{
		Plugins:         plugins,
		ExpectedPlugins: expected,
		Budget:          budget,
		Metrics:         make([]metricStatus, 0, len(defs)),
	}

	var latestDataAt *time.Time
	for _, def := range defs {
		m := metricStatus{
			MetricID:  def.id,
			UID:       def.uid,
			Name:      def.name,
			Unit:      def.unit,
			Frequency: def.frequency,
			Freshness: freshnessRed, // no data yet ⇒ the collection side is broken
			Series:    []seriesPoint{},
		}
		if obs, ok := latest[def.uid]; ok {
			value := obs.value
			at := obs.at.UTC()
			m.LatestValue = &value
			m.LatestAt = &at
			m.Provider = obs.provider
			m.Grade = obs.grade
			m.Freshness = freshness(def.frequency, now.Sub(obs.at))
			if latestDataAt == nil || obs.at.After(*latestDataAt) {
				t := obs.at.UTC()
				latestDataAt = &t
			}
		}
		if pts, ok := series[def.uid]; ok {
			m.Series = pts
		}
		p.Metrics = append(p.Metrics, m)
	}
	p.LatestDataAt = latestDataAt
	return p, nil
}

type metricDef struct {
	id, uid, name, unit, frequency string
}

// queryCurrentMetrics: design D3 query 2 — the current version of every
// active metric definition (versioned tables retire rows via effective_to).
func queryCurrentMetrics(ctx context.Context, db statusQuerier) ([]metricDef, error) {
	rows, err := db.Query(ctx, `
		SELECT id, uid, name, unit, frequency
		FROM metric_definitions
		WHERE effective_to IS NULL AND active = TRUE
		ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	defs := make([]metricDef, 0)
	for rows.Next() {
		var d metricDef
		if err := rows.Scan(&d.id, &d.uid, &d.name, &d.unit, &d.frequency); err != nil {
			return nil, fmt.Errorf("scan metric definition: %w", err)
		}
		defs = append(defs, d)
	}
	return defs, rows.Err()
}

type observationRow struct {
	uid      string
	at       time.Time
	value    float64
	provider string
	grade    string
}

// queryLatestObservations: design D3 query 3 — the newest observation per
// uid. DISTINCT ON (metric_uid) with ORDER BY metric_uid, time DESC keeps the
// newest row per uid and rides idx_obs_metric_uid_time.
func queryLatestObservations(ctx context.Context, db statusQuerier) (map[string]observationRow, error) {
	rows, err := db.Query(ctx, `
		SELECT DISTINCT ON (metric_uid) metric_uid, time, value, source_provider, quality_grade
		FROM observations
		ORDER BY metric_uid, time DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	latest := make(map[string]observationRow)
	for rows.Next() {
		var o observationRow
		if err := rows.Scan(&o.uid, &o.at, &o.value, &o.provider, &o.grade); err != nil {
			return nil, fmt.Errorf("scan latest observation: %w", err)
		}
		latest[o.uid] = o
	}
	return latest, rows.Err()
}

// queryObservationSeries: design D3 query 4 — the newest 30 points per uid,
// ascending by time. A full-table window scan is acceptable at soak scale
// (<1e5 rows); if observations ever exceed ~1e6 rows, switch to a LATERAL
// per-uid fetch bounded by the uid list from query 2 (YAGNI for now).
func queryObservationSeries(ctx context.Context, db statusQuerier) (map[string][]seriesPoint, error) {
	rows, err := db.Query(ctx, `
		SELECT metric_uid, time, value FROM (
			SELECT metric_uid, time, value,
			       row_number() OVER (PARTITION BY metric_uid ORDER BY time DESC) AS rn
			FROM observations
		) t WHERE rn <= 30
		ORDER BY metric_uid, time ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	series := make(map[string][]seriesPoint)
	for rows.Next() {
		var (
			uid string
			at  time.Time
			v   float64
		)
		if err := rows.Scan(&uid, &at, &v); err != nil {
			return nil, fmt.Errorf("scan series point: %w", err)
		}
		series[uid] = append(series[uid], seriesPoint{T: at.UTC(), V: v})
	}
	return series, rows.Err()
}

// queryTodayAlertCounts aggregates alerts triggered since the UTC
// midnight boundary. Core keeps a rolling in-memory budget (noise package);
// the API cannot see it (separate process), so this recomputes the natural-day
// count from the alerts table. The explicit UTC round-trip makes the boundary
// independent of the session timezone.
func queryTodayAlertCounts(ctx context.Context, db statusQuerier) (budgetStatus, error) {
	var counts budgetStatus
	err := db.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE source_class = 'real'),
			count(*) FILTER (WHERE source_class = 'mock'),
			count(*) FILTER (WHERE source_class = 'test'),
			count(*) FILTER (WHERE source_class = 'unknown')
		FROM alerts
		WHERE triggered_at >= date_trunc('day', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'
		  AND triggered_at < (date_trunc('day', now() AT TIME ZONE 'UTC') + interval '1 day') AT TIME ZONE 'UTC'`,
	).Scan(&counts.RealToday, &counts.MockToday, &counts.TestToday, &counts.UnknownToday)
	if err != nil {
		return budgetStatus{}, err
	}
	counts.RealLimit = model.DefaultAlertBudgetPerDay
	counts.RealOverBudget = counts.RealToday > counts.RealLimit
	counts.Today = counts.RealToday
	counts.Limit = counts.RealLimit
	return counts, nil
}

// queryPlugins: design D3 query 1 — plugin registration + health state.
// connected is heartbeat freshness; healthy additionally requires the latest
// persisted collection outcome to be successful. A stale heartbeat therefore
// reads offline even though Core cannot update the row after a plugin crash.
func queryPlugins(ctx context.Context, db statusQuerier) ([]pluginStatus, []expectedPluginStatus, error) {
	rows, err := db.Query(ctx, `
		SELECT id, name,
		       (last_heartbeat IS NOT NULL
		        AND last_heartbeat > now() - interval '90 seconds') AS connected,
		       (healthy AND last_heartbeat IS NOT NULL
		        AND last_heartbeat > now() - interval '90 seconds') AS healthy,
		       state, last_collect_at, last_collect_duration_ms,
		       last_collect_count, COALESCE(last_collect_error, ''), consecutive_errors,
		       COALESCE(capabilities, '{}'::jsonb)
		FROM plugins
		ORDER BY name`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	plugins := make([]pluginStatus, 0)
	expected := make([]expectedPluginStatus, 0)
	for rows.Next() {
		var (
			p      pluginStatus
			lastAt pgtype.Timestamptz // nullable last_collect_at
			caps   []byte
		)
		if err := rows.Scan(&p.ID, &p.Name, &p.Connected, &p.Healthy, &p.State, &lastAt,
			&p.LastCollectDurationMs, &p.LastCollectCount, &p.LastCollectError,
			&p.ConsecutiveErrors, &caps); err != nil {
			return nil, nil, fmt.Errorf("scan plugin %s: %w", p.ID, err)
		}
		if lastAt.Valid {
			t := lastAt.Time.UTC()
			p.LastCollectAt = &t
		}
		plugins = append(plugins, p)
		exp := expectedPluginStatus{
			ID:           p.ID,
			Name:         p.Name,
			Capabilities: parseCapabilityJSON(caps),
		}
		if len(exp.Capabilities.RequiresSecrets) > 0 {
			exp.SecretsPresent = make(map[string]bool, len(exp.Capabilities.RequiresSecrets))
			for _, key := range exp.Capabilities.RequiresSecrets {
				exp.SecretsPresent[key] = os.Getenv(key) != ""
			}
		}
		expected = append(expected, exp)
	}
	return plugins, expected, rows.Err()
}

func parseCapabilityJSON(raw []byte) capabilityStatus {
	out := capabilityStatus{RequiresSecrets: []string{}}
	if len(raw) == 0 || string(raw) == "{}" || string(raw) == "null" {
		return out
	}
	var parsed struct {
		WindowedBackfill bool     `json:"windowed_backfill"`
		MaxBackfillDays  int      `json:"max_backfill_days"`
		RequiresSecrets  []string `json:"requires_secrets"`
		MockAvailable    bool     `json:"mock_available"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return out
	}
	out.WindowedBackfill = parsed.WindowedBackfill
	out.MaxBackfillDays = parsed.MaxBackfillDays
	out.MockAvailable = parsed.MockAvailable
	if parsed.RequiresSecrets != nil {
		out.RequiresSecrets = parsed.RequiresSecrets
	}
	return out
}
