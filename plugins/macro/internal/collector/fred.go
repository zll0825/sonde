// Package collector 提供 macro 插件的数据采集器：FRED 真实源与离线 mock。
// 使用 SafeHTTPClient 保护 API 访问，包括 FRED 的 per-key 配额。
package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/rs/zerolog/log"

	"capital_observatory/pkg/model"
	"capital_observatory/pkg/pluginrunner"
	"capital_observatory/pkg/provider"
)

// providerFRED is the source_provider recorded on FRED-sourced observations.
const providerFRED = "fred"

// MaxHistoricalWindow is the maximum time span GetSnapshotsForWindow will honor
// without truncating. FRED itself supports decades of data; 10 years is a reasonable
// default that covers most backfill needs without overloading the API.
const MaxHistoricalWindow = 3650 * 24 * time.Hour

// maxFREDPerPage is the maximum number of observations to request per API call.
// FRED's default limit is 1000; the documented maximum is 10000.
const maxFREDPerPage = 10000

// maxFREDTotal is a safety cap on total observations fetched per series per call.
const maxFREDTotal = 100000

// fredSeries defines the FRED data for one metric, including any unit scaling
// needed to match the metric's declared unit.
type fredSeries struct {
	SeriesID  string
	UnitScale float64 // multiply raw FRED value by this to get metric units
	Frequency string
	Units     string // optional FRED units transformation (e.g. "pc1" for percent-change-year); empty to use native
}

// fredSeriesList gives a fixed iteration order so logs and snapshot slices are deterministic.
var fredSeriesList = []struct {
	MetricID string
	fredSeries
}{
	{"fed.ins.balance_sheet", fredSeries{"WALCL", 1e6, "weekly", ""}},
	{"us.mkt.ten_year_yield", fredSeries{"DGS10", 1, "daily", ""}},
	{"us.mkt.dollar_index", fredSeries{"DTWEXBGS", 1, "daily", ""}},
	{"us.mkt.usd_cny", fredSeries{"DEXCHUS", 1, "daily", ""}},
	{"us.mkt.cpi", fredSeries{"CPIAUCSL", 1, "monthly", ""}},
	{"us.mkt.inflation_yoy", fredSeries{"CPIAUCSL_PCH", 1, "monthly", "pc1"}},
}

// FREDCollector fetches real macro data from the Federal Reserve Economic Data
// API. A free API key is required via FRED_API_KEY env.
type FREDCollector struct {
	apiKey string
	client *provider.SafeHTTPClient
}

// NewFREDCollector creates a FRED collector. Fails fast when FRED_API_KEY is missing.
func NewFREDCollector() (*FREDCollector, error) {
	apiKey := os.Getenv("FRED_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("FRED_API_KEY env var is required but not set")
	}
	return &FREDCollector{
		apiKey: apiKey,
		client: provider.SharedFREDClient(),
	}, nil
}

// fredObservationsResponse is the JSON payload returned by FRED's observations endpoint.
type fredObservationsResponse struct {
	Observations []struct {
		Date  string `json:"date"`
		Value string `json:"value"`
	} `json:"observations"`
}

// GetSnapshots returns the latest observation for each FRED series.
func (f *FREDCollector) GetSnapshots(ctx context.Context) ([]pluginrunner.Snapshot, error) {
	now := time.Now()
	end := now
	start := end.AddDate(0, 0, -30)

	snaps := make([]pluginrunner.Snapshot, 0, len(fredSeriesList))
	failed := 0

	for _, entry := range fredSeriesList {
		val, obsTime, err := f.fetchLatest(ctx, entry.SeriesID, entry.Units, entry.UnitScale, start, end)
		if err != nil {
			logFetchSkip(entry.MetricID, entry.SeriesID, err)
			failed++
			continue
		}
		ts := obsTime
		if ts.IsZero() {
			ts = now
		}
		fetchedAt := time.Now()
		snaps = append(snaps, pluginrunner.Snapshot{
			MetricID:    entry.MetricID,
			Value:       *val,
			Timestamp:   ts,
			FetchedAt:   fetchedAt,
			Provider:    providerFRED,
			SourceClass: model.SourceClassReal,
			Grade:       "delayed",
		})
	}

	if len(snaps) == 0 {
		return nil, fmt.Errorf("all %d FRED series fetches failed", len(fredSeriesList))
	}
	if failed > 0 {
		providerLogPartial(len(snaps), failed)
	}
	return snaps, nil
}

// GetSnapshotsForWindow serves the backfill path using FRED's native
// observation_start/observation_end parameters.
//
// Unlike the previous 90-day cap, this implementation allows spans up to
// MaxHistoricalWindow (~10 years) and paginates through results using FRED's
// offset parameter so that full historical backfill is possible.
func (f *FREDCollector) GetSnapshotsForWindow(ctx context.Context, start, end time.Time) ([]pluginrunner.Snapshot, error) {
	if end.Before(start) {
		end = start
	}
	if end.Sub(start) > MaxHistoricalWindow {
		start = end.Add(-MaxHistoricalWindow)
	}

	snaps := make([]pluginrunner.Snapshot, 0, len(fredSeriesList)*100)
	for _, entry := range fredSeriesList {
		times, vals, err := f.fetchRangePaged(ctx, entry.SeriesID, entry.Units, entry.UnitScale, start, end)
		if err != nil {
			logFetchSkip(entry.MetricID, entry.SeriesID, err)
			continue
		}

		seriesSnaps := make([]pluginrunner.Snapshot, 0, len(times))
		fetchedAt := time.Now()
		for i, ts := range times {
			if vals[i] == nil {
				continue
			}
			seriesSnaps = append(seriesSnaps, pluginrunner.Snapshot{
				MetricID:    entry.MetricID,
				Value:       *vals[i],
				Timestamp:   ts,
				FetchedAt:   fetchedAt,
				Provider:    providerFRED,
				SourceClass: model.SourceClassReal,
				Grade:       "delayed",
			})
		}
		detectFREDGaps(entry.MetricID, entry.Frequency, seriesSnaps, start, end)
		snaps = append(snaps, seriesSnaps...)
	}
	return snaps, nil
}

// fetchLatest returns the most recent observation within [windowStart, windowEnd].
func (f *FREDCollector) fetchLatest(ctx context.Context, seriesID, units string, scale float64, windowStart, windowEnd time.Time) (*float64, time.Time, error) {
	reqURL := f.observationsURL(seriesID, units, windowStart, windowEnd, "desc", 1, 0)
	body, err := f.doGet(ctx, reqURL)
	if err != nil {
		return nil, time.Time{}, err
	}
	var resp fredObservationsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, time.Time{}, fmt.Errorf("decode FRED response: %w", err)
	}
	if len(resp.Observations) == 0 {
		return nil, time.Time{}, fmt.Errorf("no observations for %s", seriesID)
	}
	obs := resp.Observations[0]
	val, ok := parseFREDValue(obs.Value, scale)
	if !ok {
		return nil, time.Time{}, fmt.Errorf("missing value for %s @ %s", seriesID, obs.Date)
	}
	ts, _ := time.Parse("2006-01-02", obs.Date)
	return &val, ts, nil
}

// fetchRangePaged returns all observations within [start, end] in ascending date order,
// automatically paginating via FRED's offset parameter when more than maxFREDPerPage
// results exist.
func (f *FREDCollector) fetchRangePaged(ctx context.Context, seriesID, units string, scale float64, start, end time.Time) ([]time.Time, []*float64, error) {
	var allTimes []time.Time
	var allVals []*float64

	offset := 0
	for {
		reqURL := f.observationsURL(seriesID, units, start, end, "asc", maxFREDPerPage, offset)
		body, err := f.doGet(ctx, reqURL)
		if err != nil {
			return nil, nil, err
		}
		var resp fredObservationsResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, nil, fmt.Errorf("decode FRED response: %w", err)
		}
		if len(resp.Observations) == 0 {
			break
		}
		for _, obs := range resp.Observations {
			ts, err := time.Parse("2006-01-02", obs.Date)
			if err != nil {
				continue
			}
			v, ok := parseFREDValue(obs.Value, scale)
			if !ok {
				allVals = append(allVals, nil)
			} else {
				allVals = append(allVals, &v)
			}
			allTimes = append(allTimes, ts)
		}
		if len(resp.Observations) < maxFREDPerPage {
			// Last page
			break
		}
		offset += len(resp.Observations)
		if offset >= maxFREDTotal {
			log.Warn().Str("series", seriesID).Int("offset", offset).
				Msg("FRED backfill hit safety cap; some observations may be missing")
			break
		}
		// Respect context cancellation between pages
		select {
		case <-ctx.Done():
			return allTimes, allVals, ctx.Err()
		default:
		}
	}
	return allTimes, allVals, nil
}

// observationsURL builds the FRED observations endpoint URL.
func (f *FREDCollector) observationsURL(seriesID, units string, start, end time.Time, sortOrder string, limit, offset int) string {
	base := fmt.Sprintf(
		"https://api.stlouisfed.org/fred/series/observations?series_id=%s&api_key=%s&file_type=json&observation_start=%s&observation_end=%s&sort_order=%s",
		seriesID, f.apiKey, start.Format("2006-01-02"), end.Format("2006-01-02"), sortOrder,
	)
	if units != "" {
		base += "&units=" + units
	}
	if limit > 0 {
		base += fmt.Sprintf("&limit=%d", limit)
	}
	if offset > 0 {
		base += fmt.Sprintf("&offset=%d", offset)
	}
	return base
}

// doGet performs HTTP GET using SafeHTTPClient, strips API key from errors.
func (f *FREDCollector) doGet(ctx context.Context, reqURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "capital-observatory/0.1.0")

	resp, err := f.client.Do(req)
	if err != nil {
		// Strip API key from url.Error
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return nil, fmt.Errorf("http GET %s: %w", req.URL.Path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := provider.ReadAll(resp, 512)
		return nil, fmt.Errorf("FRED returned %d: %s", resp.StatusCode, string(body))
	}
	return provider.ReadAll(resp, 1<<20)
}

// parseFREDValue converts a FRED string value to a scaled float64.
// FRED uses "." for missing observations; these return ok=false.
func parseFREDValue(s string, scale float64) (float64, bool) {
	if s == "" || s == "." {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return v * scale, true
}

// detectFREDGaps checks a FRED backfill result for significant timeline gaps
// and logs warnings to support operational monitoring.
func detectFREDGaps(metricID string, frequency string, snaps []pluginrunner.Snapshot, reqStart, reqEnd time.Time) {
	if len(snaps) == 0 {
		log.Warn().Str("metric", metricID).
			Str("requested_start", reqStart.Format("2006-01-02")).
			Str("requested_end", reqEnd.Format("2006-01-02")).
			Msg("FRED backfill returned zero samples")
		return
	}

	// Determine expected gap threshold based on frequency
	var maxGap time.Duration
	switch frequency {
	case "daily":
		maxGap = 7 * 24 * time.Hour // weekly gap is unusual for daily series
	case "weekly":
		maxGap = 4 * 7 * 24 * time.Hour // monthly gap is unusual for weekly
	case "monthly":
		maxGap = 6 * 30 * 24 * time.Hour // 6-month gap is unusual for monthly
	default:
		maxGap = 30 * 24 * time.Hour
	}

	// Sort ascending (should already be sorted from API, but be safe)
	sort.Slice(snaps, func(i, j int) bool {
		return snaps[i].Timestamp.Before(snaps[j].Timestamp)
	})

	// Coverage boundaries
	coverageStart := snaps[0].Timestamp
	coverageEnd := snaps[len(snaps)-1].Timestamp
	if coverageStart.After(reqStart.Add(maxGap)) {
		log.Warn().Str("metric", metricID).
			Str("requested_start", reqStart.Format("2006-01-02")).
			Str("actual_start", coverageStart.Format("2006-01-02")).
			Msg("FRED backfill misses early data at window start")
	}
	if coverageEnd.Before(reqEnd.Add(-maxGap)) {
		log.Warn().Str("metric", metricID).
			Str("requested_end", reqEnd.Format("2006-01-02")).
			Str("actual_end", coverageEnd.Format("2006-01-02")).
			Msg("FRED backfill misses recent data at window end")
	}

	// Internal gaps
	for i := 1; i < len(snaps); i++ {
		gap := snaps[i].Timestamp.Sub(snaps[i-1].Timestamp)
		if gap > maxGap {
			log.Warn().Str("metric", metricID).
				Str("gap_start", snaps[i-1].Timestamp.Format("2006-01-02")).
				Str("gap_end", snaps[i].Timestamp.Format("2006-01-02")).
				Dur("gap_duration", gap).
				Int("total_samples", len(snaps)).
				Msg("FRED backfill timeline gap detected")
		}
	}
}

// logFetchSkip logs a skipped FRED metric.
func logFetchSkip(metric, series string, err error) {
	log.Warn().Err(err).Str("metric", metric).Str("series", series).Msg("FRED fetch failed; skipping metric")
}

// providerLogPartial logs partial FRED fetch results.
func providerLogPartial(succeeded, failed int) {
	log.Warn().Int("succeeded", succeeded).Int("failed", failed).Msg("partial FRED fetch")
}
