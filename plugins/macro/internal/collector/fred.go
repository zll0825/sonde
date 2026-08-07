// Package collector 提供 macro 插件的数据采集器：FRED 真实源与离线 mock。
package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/rs/zerolog/log"

	"capital_observatory/pkg/model"
	"capital_observatory/pkg/pluginrunner"
)

// providerFRED is the source_provider recorded on FRED-sourced observations.
const providerFRED = "fred"

// fredSeries defines the FRED data for one metric, including any unit scaling
// needed to match the metric's declared unit.
type fredSeries struct {
	SeriesID  string
	UnitScale float64 // multiply raw FRED value by this to get metric units
	Frequency string
}

// fredSeriesList gives a fixed iteration order so logs and snapshot slices
// are deterministic.
var fredSeriesList = []struct {
	MetricID string
	fredSeries
}{
	{"fed.ins.balance_sheet", fredSeries{"WALCL", 1e6, "weekly"}}, // millions USD → USD
	{"us.mkt.ten_year_yield", fredSeries{"DGS10", 1, "daily"}},    // percent, no scaling
	{"us.mkt.dollar_index", fredSeries{"DTWEXBGS", 1, "daily"}},   // index, no scaling
	{"us.mkt.usd_cny", fredSeries{"DEXCHUS", 1, "daily"}},         // CNY per USD, no scaling
}

// FREDCollector fetches real macro data from the Federal Reserve Economic Data
// API (fred.stlouisfed.org). A free API key is required via FRED_API_KEY env.
type FREDCollector struct {
	apiKey string
	client *http.Client
}

// NewFREDCollector creates a FRED collector. Fails fast when FRED_API_KEY is
// missing — a macro plugin that cannot reach its only real source shouldn't
// start and silently emit nothing.
func NewFREDCollector() (*FREDCollector, error) {
	apiKey := os.Getenv("FRED_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("FRED_API_KEY env var is required but not set")
	}
	return &FREDCollector{
		apiKey: apiKey,
		client: &http.Client{Timeout: 15 * time.Second},
	}, nil
}

// fredObservationsResponse is the JSON payload returned by FRED's
// series/observations endpoint.
type fredObservationsResponse struct {
	Observations []struct {
		Date  string `json:"date"`
		Value string `json:"value"`
	} `json:"observations"`
}

// GetSnapshots returns the latest observation for each FRED series. If a series
// fails to fetch, it is skipped with a warning; the method reports an error only
// when ALL series fail (the whole source is down, not a single flaky series).
func (f *FREDCollector) GetSnapshots(ctx context.Context) ([]pluginrunner.Snapshot, error) {
	now := time.Now()
	// FRED series are published with a lookback lag; fetch a 14-day window so
	// we pick up the freshest release even if last week's data just dropped.
	end := now
	start := end.AddDate(0, 0, -14)

	snaps := make([]pluginrunner.Snapshot, 0, len(fredSeriesList))
	failed := 0

	for _, entry := range fredSeriesList {
		val, obsTime, err := f.fetchLatest(ctx, entry.SeriesID, entry.UnitScale, start, end)
		if err != nil {
			log.Warn().
				Err(err).
				Str("metric", entry.MetricID).
				Str("series", entry.SeriesID).
				Msg("FRED fetch failed; skipping metric")
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
			Grade:       "delayed", // FRED publishes with 1..7 day lag; not realtime-grade
		})
	}

	if len(snaps) == 0 {
		return nil, fmt.Errorf("all %d FRED series fetches failed", len(fredSeriesList))
	}
	if failed > 0 {
		log.Warn().
			Int("succeeded", len(snaps)).
			Int("failed", failed).
			Msg("partial FRED fetch — some metrics updated from stale data")
	}
	return snaps, nil
}

// GetSnapshotsForWindow serves the backfill path using FRED's native
// observation_start/observation_end parameters. Observations with missing
// values (".") are emitted as nil closes so the caller can decide whether to
// skip or interpolate.
func (f *FREDCollector) GetSnapshotsForWindow(ctx context.Context, start, end time.Time) ([]pluginrunner.Snapshot, error) {
	if end.Before(start) {
		end = start
	}
	// Cap window to 90 days to stay under FRED's default 100k-observation cap.
	const maxWindow = 90 * 24 * time.Hour
	if end.Sub(start) > maxWindow {
		start = end.Add(-maxWindow)
	}

	snaps := make([]pluginrunner.Snapshot, 0, len(fredSeriesList)*60)
	for _, entry := range fredSeriesList {
		times, vals, err := f.fetchRange(ctx, entry.SeriesID, entry.UnitScale, start, end)
		if err != nil {
			log.Warn().
				Err(err).
				Str("metric", entry.MetricID).
				Str("series", entry.SeriesID).
				Msg("FRED window fetch failed; skipping metric")
			continue
		}
		fetchedAt := time.Now()
		for i, ts := range times {
			if vals[i] == nil {
				continue // missing observation (holiday, etc.)
			}
			snaps = append(snaps, pluginrunner.Snapshot{
				MetricID:    entry.MetricID,
				Value:       *vals[i],
				Timestamp:   ts,
				FetchedAt:   fetchedAt,
				Provider:    providerFRED,
				SourceClass: model.SourceClassReal,
				Grade:       "delayed",
			})
		}
	}
	return snaps, nil
}

// fetchLatest returns the most recent observation within [windowStart, windowEnd].
func (f *FREDCollector) fetchLatest(ctx context.Context, seriesID string, scale float64, windowStart, windowEnd time.Time) (*float64, time.Time, error) {
	reqURL := f.observationsURL(seriesID, windowStart, windowEnd, "desc", 1)
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

// fetchRange returns all observations within [start, end] in ascending date order.
func (f *FREDCollector) fetchRange(ctx context.Context, seriesID string, scale float64, start, end time.Time) ([]time.Time, []*float64, error) {
	reqURL := f.observationsURL(seriesID, start, end, "asc", 0)
	body, err := f.doGet(ctx, reqURL)
	if err != nil {
		return nil, nil, err
	}
	var resp fredObservationsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, nil, fmt.Errorf("decode FRED response: %w", err)
	}
	times := make([]time.Time, 0, len(resp.Observations))
	vals := make([]*float64, 0, len(resp.Observations))
	for _, obs := range resp.Observations {
		ts, err := time.Parse("2006-01-02", obs.Date)
		if err != nil {
			continue
		}
		v, ok := parseFREDValue(obs.Value, scale)
		if !ok {
			vals = append(vals, nil)
		} else {
			vals = append(vals, &v)
		}
		times = append(times, ts)
	}
	return times, vals, nil
}

// observationsURL builds the FRED observations endpoint URL.
// limit=0 lets FRED use its default (100k); sortOrder is asc or desc.
func (f *FREDCollector) observationsURL(seriesID string, start, end time.Time, sortOrder string, limit int) string {
	reqURL := fmt.Sprintf(
		"https://api.stlouisfed.org/fred/series/observations?series_id=%s&api_key=%s&file_type=json&observation_start=%s&observation_end=%s&sort_order=%s",
		seriesID, f.apiKey, start.Format("2006-01-02"), end.Format("2006-01-02"), sortOrder,
	)
	if limit > 0 {
		reqURL += fmt.Sprintf("&limit=%d", limit)
	}
	return reqURL
}

// doGet performs HTTP GET and returns the response body.
func (f *FREDCollector) doGet(ctx context.Context, reqURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "capital-observatory/0.1.0")
	resp, err := f.client.Do(req)
	if err != nil {
		// url.Error embeds the full request URL, which contains api_key as a
		// query param — strip it so the key never reaches logs.
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return nil, fmt.Errorf("http GET %s: %w", req.URL.Path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("FRED returned %d: %s", resp.StatusCode, string(body))
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
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
