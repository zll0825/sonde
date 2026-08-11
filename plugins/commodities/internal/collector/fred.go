// Package collector 提供 commodities 插件的数据采集器：FRED 真实源与离线 mock。
// WTI 原油、COMEX 铜、LBMA 下午金——统一通过 FRED series 接口获取。
package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/rs/zerolog/log"

	"capital_observatory/pkg/model"
	"capital_observatory/pkg/pluginrunner"
	"capital_observatory/pkg/provider"
)

// providerFRED is the source_provider recorded on FRED-sourced observations.
const providerFRED = "fred"

// fredSeries defines the FRED data for one commodity metric, including any unit
// scaling needed to match the metric's declared unit.
type fredSeries struct {
	SeriesID  string
	UnitScale float64 // multiply raw FRED value by this to get metric units
	Frequency string
}

// fredSeriesList gives a fixed iteration order so logs and snapshot slices are deterministic.
var fredSeriesList = []struct {
	MetricID string
	fredSeries
}{
	{"oil.energy.wti", fredSeries{"DCOILWTICO", 1, "daily"}},
	{"metal.industrial.copper", fredSeries{"PCOPPUSDM", 1, "daily"}},
	{"metal.precious.gold", fredSeries{"GOLDAMGBD228NLBM", 1, "daily"}},
}

// FREDCollector fetches real commodity data from the Federal Reserve Economic
// Data API. A free API key is required via FRED_API_KEY env.
type FREDCollector struct {
	apiKey string
	client *provider.SafeHTTPClient
}

// NewFREDCollector creates a commodity collector. Fails fast when FRED_API_KEY is missing.
func NewFREDCollector() (*FREDCollector, error) {
	apiKey := os.Getenv("FRED_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("FRED_API_KEY env var is required but not set")
	}
	return &FREDCollector{
		apiKey: apiKey,
		client: provider.NewSafeHTTPClient(provider.FREDConfig()),
	}, nil
}

// fredObservationsResponse is the JSON payload returned by FRED's observations endpoint.
type fredObservationsResponse struct {
	Observations []struct {
		Date  string `json:"date"`
		Value string `json:"value"`
	} `json:"observations"`
}

// GetSnapshots returns the latest observation for each commodity series.
func (f *FREDCollector) GetSnapshots(ctx context.Context) ([]pluginrunner.Snapshot, error) {
	now := time.Now()
	end := now
	start := end.AddDate(0, 0, -45) // 45-day lookback: aligns with oil/copper release lag

	snaps := make([]pluginrunner.Snapshot, 0, len(fredSeriesList))
	failed := 0

	for _, entry := range fredSeriesList {
		val, obsTime, err := f.fetchLatest(ctx, entry.SeriesID, entry.UnitScale, start, end)
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
		return nil, fmt.Errorf("all %d FRED commodity fetches failed", len(fredSeriesList))
	}
	if failed > 0 {
		providerLogPartial(len(snaps), failed)
	}
	return snaps, nil
}

// GetSnapshotsForWindow serves the backfill path using FRED's native
// observation_start/observation_end parameters.
func (f *FREDCollector) GetSnapshotsForWindow(ctx context.Context, start, end time.Time) ([]pluginrunner.Snapshot, error) {
	if end.Before(start) {
		end = start
	}
	const maxWindow = 90 * 24 * time.Hour
	if end.Sub(start) > maxWindow {
		start = end.Add(-maxWindow)
	}

	snaps := make([]pluginrunner.Snapshot, 0, len(fredSeriesList)*60)
	for _, entry := range fredSeriesList {
		times, vals, err := f.fetchRange(ctx, entry.SeriesID, entry.UnitScale, start, end)
		if err != nil {
			logFetchSkip(entry.MetricID, entry.SeriesID, err)
			continue
		}
		fetchedAt := time.Now()
		for i, ts := range times {
			if vals[i] == nil {
				continue
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

// doGet performs HTTP GET using SafeHTTPClient, strips API key from errors.
func (f *FREDCollector) doGet(ctx context.Context, reqURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "capital-observatory/0.1.0")

	resp, err := f.client.Do(req)
	if err != nil {
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

func logFetchSkip(metric, series string, err error) {
	log.Warn().Err(err).Str("metric", metric).Str("series", series).Msg("FRED commodity fetch failed; skipping metric")
}

func providerLogPartial(succeeded, failed int) {
	log.Warn().Int("succeeded", succeeded).Int("failed", failed).Msg("partial FRED commodity fetch")
}
