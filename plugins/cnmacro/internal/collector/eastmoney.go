package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	"sonde/pkg/pluginrunner"
	"sonde/pkg/provider"
)

const (
	defaultEastMoneyBase = "https://datacenter-web.eastmoney.com"
	providerEastMoney    = "eastmoney"

	metricM2Yoy  = "cn.mkt.m2_yoy"
	metricM1Yoy  = "cn.mkt.m1_yoy"
	metricM1M2   = "cn.mkt.m1_m2_gap"
	metricLPR1Y  = "cn.mkt.lpr_1y"
	metricLPR5Y  = "cn.mkt.lpr_5y"
	reportMoney  = "RPT_ECONOMY_CURRENCY_SUPPLY"
	reportLPR    = "RPTA_WEB_RATE"
	emPageSize   = 200
	emMaxPages   = 5
	emLatestSize = 3
)

// lprReformDate is the first quote under the reformed LPR mechanism
// (2019-08-20). RPTA_WEB_RATE also carries the pre-reform daily 1Y LPR
// (different mechanism, no 5Y tenor); those rows are dropped.
var lprReformDate = dateUTC(2019, time.August, 20)

// EastMoneyCollector reads the official M1/M2 YoY growth and LPR quotes as
// re-published by the East Money datacenter (backup channel; values are
// PBoC/NIFC publications).
type EastMoneyCollector struct {
	client  *provider.SafeHTTPClient
	baseURL string
}

// NewEastMoneyCollector builds the production client.
func NewEastMoneyCollector() *EastMoneyCollector {
	return NewEastMoneyCollectorWithClient(provider.NewSafeHTTPClient(provider.EastMoneyDatacenterConfig()))
}

// NewEastMoneyCollectorWithClient injects the HTTP client (tests use httptest).
func NewEastMoneyCollectorWithClient(client *provider.SafeHTTPClient) *EastMoneyCollector {
	return &EastMoneyCollector{client: client, baseURL: defaultEastMoneyBase}
}

// optFloat distinguishes JSON null (absent) from 0.
type optFloat struct {
	v  float64
	ok bool
}

func (o *optFloat) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" || s == `""` {
		return nil
	}
	s = strings.Trim(s, `"`)
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("parse %q: %w", s, err)
	}
	o.v, o.ok = v, true
	return nil
}

type moneyRow struct {
	ReportDate string   `json:"REPORT_DATE"`
	M2         optFloat `json:"BASIC_CURRENCY"`
	M2Yoy      optFloat `json:"BASIC_CURRENCY_SAME"`
	M1         optFloat `json:"CURRENCY"`
	M1Yoy      optFloat `json:"CURRENCY_SAME"`
}

type lprRow struct {
	TradeDate string   `json:"TRADE_DATE"`
	LPR1Y     optFloat `json:"LPR1Y"`
	LPR5Y     optFloat `json:"LPR5Y"`
}

type emEnvelope[T any] struct {
	Result *struct {
		Pages int `json:"pages"`
		Data  []T `json:"data"`
		Count int `json:"count"`
	} `json:"result"`
	Success bool   `json:"success"`
	Message string `json:"message"`
	Code    int    `json:"code"`
}

// parseEMDate parses "2026-08-01 00:00:00" (or bare date) into a UTC date.
func parseEMDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if len(s) >= 10 {
		s = s[:10]
	}
	return time.ParseInLocation("2006-01-02", s, time.UTC)
}

func moneySnapshots(rows []moneyRow, fetchedAt time.Time) []pluginrunner.Snapshot {
	out := make([]pluginrunner.Snapshot, 0, len(rows)*3)
	for _, r := range rows {
		d, err := parseEMDate(r.ReportDate)
		if err != nil {
			continue
		}
		ts := dateUTC(d.Year(), d.Month(), 1)
		if r.M2Yoy.ok {
			out = append(out, snap(metricM2Yoy, r.M2Yoy.v, ts, fetchedAt, providerEastMoney, "delayed"))
		}
		if r.M1Yoy.ok {
			out = append(out, snap(metricM1Yoy, r.M1Yoy.v, ts, fetchedAt, providerEastMoney, "delayed"))
		}
		if r.M1Yoy.ok && r.M2Yoy.ok {
			gap := roundTo(r.M1Yoy.v-r.M2Yoy.v, 4)
			out = append(out, snap(metricM1M2, gap, ts, fetchedAt, providerEastMoney, "delayed"))
		}
	}
	return out
}

func lprSnapshots(rows []lprRow, fetchedAt time.Time) []pluginrunner.Snapshot {
	out := make([]pluginrunner.Snapshot, 0, len(rows)*2)
	for _, r := range rows {
		ts, err := parseEMDate(r.TradeDate)
		if err != nil || ts.Before(lprReformDate) {
			continue
		}
		if r.LPR1Y.ok {
			out = append(out, snap(metricLPR1Y, r.LPR1Y.v, ts, fetchedAt, providerEastMoney, "delayed"))
		}
		if r.LPR5Y.ok {
			out = append(out, snap(metricLPR5Y, r.LPR5Y.v, ts, fetchedAt, providerEastMoney, "delayed"))
		}
	}
	return out
}

func (c *EastMoneyCollector) GetSnapshots(ctx context.Context) ([]pluginrunner.Snapshot, error) {
	fetchedAt := time.Now()
	var snaps []pluginrunner.Snapshot
	var failures []pluginrunner.CollectionFailure

	money, err := fetchEM[moneyRow](ctx, c, reportMoney, "REPORT_DATE", 1, emLatestSize)
	if err != nil {
		failures = append(failures, pluginrunner.CollectionFailure{Provider: providerEastMoney, MetricID: metricM2Yoy, Err: err})
	} else if latest := latestMoneyRow(money); latest != nil {
		snaps = append(snaps, moneySnapshots([]moneyRow{*latest}, fetchedAt)...)
	}

	lpr, err := fetchEM[lprRow](ctx, c, reportLPR, "TRADE_DATE", 1, emLatestSize)
	if err != nil {
		failures = append(failures, pluginrunner.CollectionFailure{Provider: providerEastMoney, MetricID: metricLPR1Y, Err: err})
	} else if latest := latestLPRRow(lpr); latest != nil {
		snaps = append(snaps, lprSnapshots([]lprRow{*latest}, fetchedAt)...)
	}
	return finish(snaps, failures, true)
}

func (c *EastMoneyCollector) GetSnapshotsForWindow(ctx context.Context, start, end time.Time) ([]pluginrunner.Snapshot, error) {
	if end.Before(start) {
		return nil, fmt.Errorf("east money window end precedes start")
	}
	fetchedAt := time.Now()
	var snaps []pluginrunner.Snapshot
	var failures []pluginrunner.CollectionFailure

	money, err := fetchEMUntil(ctx, c, reportMoney, "REPORT_DATE", start, func(r moneyRow) string { return r.ReportDate })
	if err != nil {
		failures = append(failures, pluginrunner.CollectionFailure{Provider: providerEastMoney, MetricID: metricM2Yoy, Err: err})
	}
	for _, s := range moneySnapshots(money, fetchedAt) {
		// Monthly observations are stamped on the 1st; keep a month when any
		// part of it falls inside the window.
		monthEnd := s.Timestamp.AddDate(0, 1, -1)
		if !monthEnd.Before(dateUTC(start.UTC().Year(), start.UTC().Month(), start.UTC().Day())) && !s.Timestamp.After(end.UTC()) {
			snaps = append(snaps, s)
		}
	}

	lpr, err := fetchEMUntil(ctx, c, reportLPR, "TRADE_DATE", start, func(r lprRow) string { return r.TradeDate })
	if err != nil {
		failures = append(failures, pluginrunner.CollectionFailure{Provider: providerEastMoney, MetricID: metricLPR1Y, Err: err})
	}
	for _, s := range lprSnapshots(lpr, fetchedAt) {
		if inWindow(s.Timestamp, start, end) {
			snaps = append(snaps, s)
		}
	}
	return finish(snaps, failures, false)
}

func latestMoneyRow(rows []moneyRow) *moneyRow {
	var best *moneyRow
	var bestDate time.Time
	for i := range rows {
		d, err := parseEMDate(rows[i].ReportDate)
		if err != nil || !(rows[i].M2Yoy.ok || rows[i].M1Yoy.ok) {
			continue
		}
		if best == nil || d.After(bestDate) {
			best, bestDate = &rows[i], d
		}
	}
	return best
}

func latestLPRRow(rows []lprRow) *lprRow {
	var best *lprRow
	var bestDate time.Time
	for i := range rows {
		d, err := parseEMDate(rows[i].TradeDate)
		if err != nil || d.Before(lprReformDate) || !(rows[i].LPR1Y.ok || rows[i].LPR5Y.ok) {
			continue
		}
		if best == nil || d.After(bestDate) {
			best, bestDate = &rows[i], d
		}
	}
	return best
}

// fetchEMUntil pages newest-first until a page reaches rows older than start
// (or the page cap). A failure after the first page returns what was read.
func fetchEMUntil[T any](ctx context.Context, c *EastMoneyCollector, report, sortCol string, start time.Time, dateOf func(T) string) ([]T, error) {
	startDate := dateUTC(start.UTC().Year(), start.UTC().Month(), 1)
	var all []T
	for page := 1; page <= emMaxPages; page++ {
		rows, err := fetchEM[T](ctx, c, report, sortCol, page, emPageSize)
		if err != nil {
			if len(all) > 0 {
				return all, fmt.Errorf("page %d: %w", page, err)
			}
			return nil, err
		}
		all = append(all, rows...)
		if len(rows) < emPageSize {
			break
		}
		oldest, err := parseEMDate(dateOf(rows[len(rows)-1]))
		if err == nil && oldest.Before(startDate) {
			break
		}
	}
	return all, nil
}

func fetchEM[T any](ctx context.Context, c *EastMoneyCollector, report, sortCol string, page, size int) ([]T, error) {
	u, err := url.Parse(strings.TrimRight(c.baseURL, "/") + "/api/data/v1/get")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("reportName", report)
	q.Set("columns", "ALL")
	q.Set("pageNumber", strconv.Itoa(page))
	q.Set("pageSize", strconv.Itoa(size))
	q.Set("sortColumns", sortCol)
	q.Set("sortTypes", "-1")
	u.RawQuery = q.Encode()
	f, err := doGET(ctx, c.client, u.String(), "application/json")
	if err != nil {
		return nil, err
	}
	var env emEnvelope[T]
	if err := json.Unmarshal(f.body, &env); err != nil {
		return nil, fmt.Errorf("decode east money %s: %w", report, err)
	}
	if !env.Success || env.Result == nil {
		return nil, fmt.Errorf("east money %s: success=%v code=%d message=%q", report, env.Success, env.Code, env.Message)
	}
	if len(env.Result.Data) == 0 && page == 1 {
		return nil, fmt.Errorf("east money %s: empty data", report)
	}
	return env.Result.Data, nil
}

func roundTo(v float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(v*p) / p
}
