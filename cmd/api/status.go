package main

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// budgetLimit is the frontend's "today X/10" denominator and mirrors
// internal/core/noise.DefaultBudgetPerDay (10). The API process runs
// separately from core and must not import core internals — keep this in
// sync when the budget default changes.
const budgetLimit = 10

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
	Plugins      []pluginStatus `json:"plugins"`
	Budget       budgetStatus   `json:"budget"`
	LatestDataAt *time.Time     `json:"latest_data_at"` // newest observation across all metrics; null when empty
	Metrics      []metricStatus `json:"metrics"`
}

type pluginStatus struct {
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	Healthy          bool       `json:"healthy"`
	State            string     `json:"state"`
	LastCollectAt    *time.Time `json:"last_collect_at"`
	LastCollectCount int        `json:"last_collect_count"`
}

type budgetStatus struct {
	Today int `json:"today"`
	Limit int `json:"limit"`
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
	plugins, err := queryPlugins(ctx, db)
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
	today, err := queryTodayAlertCount(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("query today alert count: %w", err)
	}

	p := &statusPayload{
		Plugins: plugins,
		Budget:  budgetStatus{Today: today, Limit: budgetLimit},
		Metrics: make([]metricStatus, 0, len(defs)),
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

// queryTodayAlertCount: design D3 query 5 — alerts triggered since the UTC
// midnight boundary. Core keeps a rolling in-memory budget (noise package);
// the API cannot see it (separate process), so this recomputes the natural-day
// count from the alerts table. The explicit UTC round-trip makes the boundary
// independent of the session timezone.
func queryTodayAlertCount(ctx context.Context, db statusQuerier) (int, error) {
	var count int
	err := db.QueryRow(ctx, `
		SELECT count(*) FROM alerts
		WHERE triggered_at >= date_trunc('day', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'`,
	).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
}

// queryPlugins: design D3 query 1 — plugin registration + health state.
// healthy is derated by heartbeat staleness: the writer (core) sets
// plugins.healthy=true on heartbeat but cannot flip it back on crash, so a
// heartbeat older than 90s (3× the 30s plugin heartbeat interval) reads as
// offline here.
func queryPlugins(ctx context.Context, db statusQuerier) ([]pluginStatus, error) {
	rows, err := db.Query(ctx, `
		SELECT id, name,
		       (healthy AND last_heartbeat IS NOT NULL
		        AND last_heartbeat > now() - interval '90 seconds') AS healthy,
		       state, last_collect_at, last_collect_count
		FROM plugins
		ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	plugins := make([]pluginStatus, 0)
	for rows.Next() {
		var (
			p      pluginStatus
			lastAt pgtype.Timestamptz // nullable last_collect_at
		)
		if err := rows.Scan(&p.ID, &p.Name, &p.Healthy, &p.State, &lastAt, &p.LastCollectCount); err != nil {
			return nil, fmt.Errorf("scan plugin %s: %w", p.ID, err)
		}
		if lastAt.Valid {
			t := lastAt.Time.UTC()
			p.LastCollectAt = &t
		}
		plugins = append(plugins, p)
	}
	return plugins, rows.Err()
}
