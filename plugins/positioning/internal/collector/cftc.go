package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"sonde/pkg/pluginrunner"
	"sonde/pkg/provider"
)

const (
	// defaultCFTCURL is the Legacy "Futures Only" COT dataset on the CFTC
	// Socrata portal.
	defaultCFTCURL = "https://publicreporting.cftc.gov/resource/6dca-aqww.json"
	providerCFTC   = "cftc"

	// envCFTCAppToken is optional: when set it is sent as X-App-Token,
	// otherwise requests go out anonymously (throttled, occasional 403).
	envCFTCAppToken = "CFTC_APP_TOKEN"

	// cftcPageSize bounds one Socrata page; pages continue via $offset.
	cftcPageSize = 5000
	// cftcLatestLookback covers a missed week or two plus holiday delays.
	cftcLatestLookback = 35 * 24 * time.Hour
)

// cotContract maps a CFTC contract market code to a Sonde metric.
// Codes were read from the dataset itself (market_and_exchange_names) on
// 2026-09-29; all four had reports through 2026-09-22.
type cotContract struct {
	code     string
	metricID string
	name     string
}

var cotContracts = []cotContract{
	{"13874A", "cot.es.noncomm_net", "E-MINI S&P 500 - CHICAGO MERCANTILE EXCHANGE"},
	{"043602", "cot.zn.noncomm_net", "UST 10Y NOTE - CHICAGO BOARD OF TRADE"},
	{"088691", "cot.gc.noncomm_net", "GOLD - COMMODITY EXCHANGE INC."},
	{"133741", "cot.btc.noncomm_net", "BITCOIN - CHICAGO MERCANTILE EXCHANGE"},
}

// CFTCCollector reads non-commercial net positions from the COT report.
type CFTCCollector struct {
	client *provider.SafeHTTPClient
	url    string
	token  string
	now    func() time.Time
	// pageSize is the Socrata $limit per request (tests shrink it).
	pageSize int
}

// NewCFTCCollector builds a production client. The app token is optional.
func NewCFTCCollector() *CFTCCollector {
	c := NewCFTCCollectorWithClient(provider.NewSafeHTTPClient(provider.CFTCConfig()))
	c.token = strings.TrimSpace(os.Getenv(envCFTCAppToken))
	return c
}

// NewCFTCCollectorWithClient injects the HTTP client (tests use httptest).
func NewCFTCCollectorWithClient(client *provider.SafeHTTPClient) *CFTCCollector {
	return &CFTCCollector{client: client, url: defaultCFTCURL, now: time.Now, pageSize: cftcPageSize}
}

type cotRow struct {
	ReportDate string `json:"report_date_as_yyyy_mm_dd"`
	Code       string `json:"cftc_contract_market_code"`
	NoncommL   string `json:"noncomm_positions_long_all"`
	NoncommS   string `json:"noncomm_positions_short_all"`
}

// cotReleaseTime is the earliest moment a COT report is public: the Friday
// after the Tuesday "as of" date, 15:30 ET. We use 20:30 UTC (15:30 EST), which
// is conservative by one hour during daylight saving time. Holiday weeks are
// released later, never earlier, so this bound stays safe.
func cotReleaseTime(reportDate time.Time) time.Time {
	d := time.Date(reportDate.Year(), reportDate.Month(), reportDate.Day(), 20, 30, 0, 0, time.UTC)
	return d.AddDate(0, 0, 3)
}

func parseCOTDate(s string) (time.Time, error) {
	if len(s) < 10 {
		return time.Time{}, fmt.Errorf("bad report date %q", s)
	}
	return time.ParseInLocation("2006-01-02", s[:10], time.UTC)
}

func (c *CFTCCollector) query(start, end time.Time, offset int) string {
	codes := make([]string, 0, len(cotContracts))
	for _, k := range cotContracts {
		codes = append(codes, "'"+k.code+"'")
	}
	where := fmt.Sprintf("cftc_contract_market_code in(%s) AND report_date_as_yyyy_mm_dd between '%s' and '%s'",
		strings.Join(codes, ","),
		start.UTC().Format("2006-01-02T00:00:00"),
		end.UTC().Format("2006-01-02")+"T23:59:59")
	q := url.Values{}
	q.Set("$select", "report_date_as_yyyy_mm_dd,cftc_contract_market_code,noncomm_positions_long_all,noncomm_positions_short_all")
	q.Set("$where", where)
	q.Set("$order", "report_date_as_yyyy_mm_dd ASC,cftc_contract_market_code ASC")
	q.Set("$limit", strconv.Itoa(c.pageSize))
	q.Set("$offset", strconv.Itoa(offset))
	return c.url + "?" + q.Encode()
}

// fetch returns every row for the tracked contracts with report date in [start, end].
func (c *CFTCCollector) fetch(ctx context.Context, start, end time.Time) ([]cotRow, error) {
	var headers map[string]string
	if c.token != "" {
		headers = map[string]string{"X-App-Token": c.token}
	}
	var all []cotRow
	for offset := 0; ; offset += c.pageSize {
		body, err := doGET(ctx, c.client, c.query(start, end, offset), "application/json", headers)
		if err != nil {
			return nil, fmt.Errorf("CFTC COT: %w", err)
		}
		var page []cotRow
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("decode CFTC COT: %w", err)
		}
		all = append(all, page...)
		if len(page) < c.pageSize {
			return all, nil
		}
	}
}

// toSnapshots converts rows to snapshots, dropping any report whose release
// time is still in the future relative to now.
func (c *CFTCCollector) toSnapshots(rows []cotRow) []pluginrunner.Snapshot {
	metricByCode := make(map[string]string, len(cotContracts))
	for _, k := range cotContracts {
		metricByCode[k.code] = k.metricID
	}
	now := c.now()
	out := make([]pluginrunner.Snapshot, 0, len(rows))
	for _, r := range rows {
		metricID, ok := metricByCode[strings.TrimSpace(r.Code)]
		if !ok {
			continue
		}
		ts, err := parseCOTDate(r.ReportDate)
		if err != nil {
			log.Warn().Err(err).Str("metric", metricID).Msg("CFTC COT: skip row")
			continue
		}
		if cotReleaseTime(ts).After(now) {
			continue
		}
		long, errL := strconv.ParseFloat(strings.TrimSpace(r.NoncommL), 64)
		short, errS := strconv.ParseFloat(strings.TrimSpace(r.NoncommS), 64)
		if errL != nil || errS != nil {
			log.Warn().Str("metric", metricID).Str("report_date", r.ReportDate).Msg("CFTC COT: non-numeric positions")
			continue
		}
		out = append(out, snap(metricID, long-short, ts, now, providerCFTC))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Timestamp.Before(out[j].Timestamp) })
	return out
}

// GetSnapshots returns the latest released report for each tracked contract.
func (c *CFTCCollector) GetSnapshots(ctx context.Context) ([]pluginrunner.Snapshot, error) {
	now := c.now()
	rows, err := c.fetch(ctx, now.Add(-cftcLatestLookback), now)
	if err != nil {
		return nil, err
	}
	latest := map[string]pluginrunner.Snapshot{}
	for _, s := range c.toSnapshots(rows) {
		if prev, ok := latest[s.MetricID]; !ok || s.Timestamp.After(prev.Timestamp) {
			latest[s.MetricID] = s
		}
	}
	out := make([]pluginrunner.Snapshot, 0, len(latest))
	var failures []pluginrunner.CollectionFailure
	for _, k := range cotContracts {
		s, ok := latest[k.metricID]
		if !ok {
			failures = append(failures, pluginrunner.CollectionFailure{
				MetricID: k.metricID, Provider: providerCFTC,
				Err: fmt.Errorf("no released report for %s in last %d days", k.code, int(cftcLatestLookback.Hours()/24)),
			})
			continue
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil, pluginrunner.SummarizeCollectionFailures(failures)
	}
	return out, pluginrunner.SummarizeCollectionFailures(failures)
}

// GetSnapshotsForWindow returns every released report in [start, end].
func (c *CFTCCollector) GetSnapshotsForWindow(ctx context.Context, start, end time.Time) ([]pluginrunner.Snapshot, error) {
	if end.Before(start) {
		return nil, fmt.Errorf("CFTC history end precedes start")
	}
	rows, err := c.fetch(ctx, start, end)
	if err != nil {
		return nil, err
	}
	return c.toSnapshots(rows), nil
}
