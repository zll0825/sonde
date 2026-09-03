package fred

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"capital_observatory/pkg/model"
	"capital_observatory/pkg/pluginrunner"
	"capital_observatory/pkg/provider"
)

// Collector pulls FRED observations for a binding list. Plugin packages
// wrap this type; they must not copy the HTTP loop.
type Collector struct {
	apiKey         string
	client         *provider.SafeHTTPClient
	bindings       []Binding
	latestLookback time.Duration

	mu           sync.Mutex
	coverages    map[string]provider.BackfillCoverage
	lastCoverage provider.BackfillCoverage
}

// Options construct a Collector. Tests inject Client; production uses
// SharedClient and RequireAPIKey.
type Options struct {
	APIKey         string
	Client         *provider.SafeHTTPClient
	Bindings       []Binding
	LatestLookback time.Duration
}

// NewCollector builds a FRED collector. Bindings must be non-empty.
func NewCollector(opts Options) (*Collector, error) {
	if len(opts.Bindings) == 0 {
		return nil, fmt.Errorf("FRED collector needs at least one binding")
	}
	apiKey := opts.APIKey
	if apiKey == "" {
		var err error
		apiKey, err = RequireAPIKey()
		if err != nil {
			return nil, err
		}
	}
	client := opts.Client
	if client == nil {
		client = SharedClient()
	}
	lookback := opts.LatestLookback
	if lookback <= 0 {
		lookback = defaultLatestLookback
	}
	return &Collector{
		apiKey:         apiKey,
		client:         client,
		bindings:       append([]Binding(nil), opts.Bindings...),
		latestLookback: lookback,
		coverages:      make(map[string]provider.BackfillCoverage),
	}, nil
}

// Bindings returns a copy of the configured series list (deterministic order).
func (c *Collector) Bindings() []Binding {
	out := make([]Binding, len(c.bindings))
	copy(out, c.bindings)
	return out
}

// LastCoverage returns the most recent BackfillCoverage for a metric ID.
func (c *Collector) LastCoverage(metricID string) (provider.BackfillCoverage, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cov, ok := c.coverages[metricID]
	return cov, ok
}

// CircuitState returns the current FRED circuit breaker state.
func (c *Collector) CircuitState() string {
	if c.client == nil {
		return "unknown"
	}
	return c.client.CircuitState()
}

type fredObservationsResponse struct {
	Observations []struct {
		Date  string `json:"date"`
		Value string `json:"value"`
	} `json:"observations"`
}

// GetSnapshots returns the latest observation for each binding.
func (c *Collector) GetSnapshots(ctx context.Context) ([]pluginrunner.Snapshot, error) {
	time.Sleep(time.Duration(rand.Int63n(100)) * time.Millisecond)

	now := time.Now()
	end := now
	start := end.Add(-c.latestLookback)

	snaps := make([]pluginrunner.Snapshot, 0, len(c.bindings))
	failures := make([]pluginrunner.CollectionFailure, 0)

	for _, entry := range c.bindings {
		val, obsTime, err := c.fetchLatest(ctx, entry.SeriesID, entry.Units, entry.UnitScale, start, end)
		if err != nil {
			logFetchSkip(entry.MetricID, entry.SeriesID, err)
			failures = append(failures, pluginrunner.CollectionFailure{
				MetricID: entry.MetricID, Provider: ProviderName, Err: err,
			})
			continue
		}
		ts := obsTime
		if ts.IsZero() {
			ts = now
		}
		snaps = append(snaps, pluginrunner.Snapshot{
			MetricID:    entry.MetricID,
			Value:       *val,
			Timestamp:   ts,
			FetchedAt:   time.Now(),
			Provider:    ProviderName,
			SourceClass: model.SourceClassReal,
			Grade:       "delayed",
		})
	}

	if len(snaps) == 0 {
		return nil, pluginrunner.SummarizeCollectionFailures(failures)
	}
	if len(failures) > 0 {
		log.Warn().Int("succeeded", len(snaps)).Int("failed", len(failures)).Msg("partial FRED fetch")
		return snaps, pluginrunner.SummarizeCollectionFailures(failures)
	}
	return snaps, nil
}

// GetSnapshotsForWindow paginates FRED history up to MaxHistoricalWindow.
func (c *Collector) GetSnapshotsForWindow(ctx context.Context, start, end time.Time) ([]pluginrunner.Snapshot, error) {
	time.Sleep(time.Duration(rand.Int63n(100)) * time.Millisecond)

	if end.Before(start) {
		end = start
	}
	if end.Sub(start) > MaxHistoricalWindow {
		start = end.Add(-MaxHistoricalWindow)
	}

	snaps := make([]pluginrunner.Snapshot, 0, len(c.bindings)*100)
	failures := make([]pluginrunner.CollectionFailure, 0)
	for _, entry := range c.bindings {
		times, vals, err := c.fetchRangePaged(ctx, entry.SeriesID, entry.Units, entry.UnitScale, start, end)
		if err != nil {
			logFetchSkip(entry.MetricID, entry.SeriesID, err)
			failures = append(failures, pluginrunner.CollectionFailure{
				MetricID: entry.MetricID, Provider: ProviderName, Err: err,
			})
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
				Provider:    ProviderName,
				SourceClass: model.SourceClassReal,
				Grade:       "delayed",
			})
		}
		c.detectGaps(entry.MetricID, entry.Frequency, seriesSnaps, start, end)
		snaps = append(snaps, seriesSnaps...)
	}
	return snaps, pluginrunner.SummarizeCollectionFailures(failures)
}

func (c *Collector) fetchLatest(ctx context.Context, seriesID, units string, scale float64, windowStart, windowEnd time.Time) (*float64, time.Time, error) {
	reqURL := c.observationsURL(seriesID, units, windowStart, windowEnd, "desc", 1, 0)
	body, err := c.doGet(ctx, reqURL)
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
	val, ok := ParseValue(obs.Value, scale)
	if !ok {
		return nil, time.Time{}, fmt.Errorf("missing value for %s @ %s", seriesID, obs.Date)
	}
	ts, _ := time.Parse("2006-01-02", obs.Date)
	return &val, ts, nil
}

func (c *Collector) fetchRangePaged(ctx context.Context, seriesID, units string, scale float64, start, end time.Time) ([]time.Time, []*float64, error) {
	var allTimes []time.Time
	var allVals []*float64

	offset := 0
	for {
		reqURL := c.observationsURL(seriesID, units, start, end, "asc", maxFREDPerPage, offset)
		body, err := c.doGet(ctx, reqURL)
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
			v, ok := ParseValue(obs.Value, scale)
			if !ok {
				allVals = append(allVals, nil)
			} else {
				allVals = append(allVals, &v)
			}
			allTimes = append(allTimes, ts)
		}
		if len(resp.Observations) < maxFREDPerPage {
			break
		}
		offset += len(resp.Observations)
		if offset >= maxFREDTotal {
			log.Warn().Str("series", seriesID).Int("offset", offset).
				Msg("FRED backfill hit safety cap; some observations may be missing")
			break
		}
		select {
		case <-ctx.Done():
			return allTimes, allVals, ctx.Err()
		default:
		}
	}
	return allTimes, allVals, nil
}

func (c *Collector) observationsURL(seriesID, units string, start, end time.Time, sortOrder string, limit, offset int) string {
	base := fmt.Sprintf(
		"https://api.stlouisfed.org/fred/series/observations?series_id=%s&api_key=%s&file_type=json&observation_start=%s&observation_end=%s&sort_order=%s",
		seriesID, c.apiKey, start.Format("2006-01-02"), end.Format("2006-01-02"), sortOrder,
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

func (c *Collector) doGet(ctx context.Context, reqURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "capital-observatory/0.1.0")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http GET %s: %w", req.URL.Path, provider.SanitizeTransportError(err))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := provider.ReadAll(resp, 512)
		return nil, fmt.Errorf("FRED returned %d: %s", resp.StatusCode, string(body))
	}
	return provider.ReadAll(resp, 1<<20)
}

// ParseValue converts a FRED string to a scaled float64. "." is missing.
func ParseValue(s string, scale float64) (float64, bool) {
	if s == "" || s == "." {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return v * scale, true
}

func (c *Collector) detectGaps(metricID, frequency string, snaps []pluginrunner.Snapshot, reqStart, reqEnd time.Time) {
	if len(snaps) == 0 {
		log.Warn().Str("metric", metricID).
			Str("requested_start", reqStart.Format("2006-01-02")).
			Str("requested_end", reqEnd.Format("2006-01-02")).
			Msg("FRED backfill returned zero samples")
		c.recordCoverage(metricID, provider.BackfillCoverage{
			MetricID: metricID, SampleCount: 0, HasGaps: false,
		})
		return
	}

	sort.Slice(snaps, func(i, j int) bool {
		return snaps[i].Timestamp.Before(snaps[j].Timestamp)
	})
	times := make([]time.Time, len(snaps))
	for i, s := range snaps {
		times[i] = s.Timestamp
	}
	gaps := provider.DetectGaps(times, frequency)
	coverageStart := snaps[0].Timestamp
	coverageEnd := snaps[len(snaps)-1].Timestamp
	c.recordCoverage(metricID, provider.BackfillCoverage{
		MetricID:    metricID,
		ActualStart: coverageStart,
		ActualEnd:   coverageEnd,
		SampleCount: len(snaps),
		HasGaps:     len(gaps) > 0,
		GapCount:    len(gaps),
	})
	if coverageStart.After(reqStart.Add(24 * 30 * time.Hour)) {
		log.Warn().Str("metric", metricID).
			Str("requested_start", reqStart.Format("2006-01-02")).
			Str("actual_start", coverageStart.Format("2006-01-02")).
			Msg("FRED backfill misses early data at window start")
	}
	if coverageEnd.Before(reqEnd.Add(-24 * 30 * time.Hour)) {
		log.Warn().Str("metric", metricID).
			Str("requested_end", reqEnd.Format("2006-01-02")).
			Str("actual_end", coverageEnd.Format("2006-01-02")).
			Msg("FRED backfill misses recent data at window end")
	}
	for _, gap := range gaps {
		log.Warn().Str("metric", metricID).
			Str("gap_start", gap.Start.Format("2006-01-02")).
			Str("gap_end", gap.End.Format("2006-01-02")).
			Dur("gap_duration", gap.Duration).
			Int("total_samples", len(snaps)).
			Msg("FRED backfill timeline gap detected")
	}
}

func (c *Collector) recordCoverage(metricID string, cov provider.BackfillCoverage) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.coverages == nil {
		c.coverages = make(map[string]provider.BackfillCoverage)
	}
	c.coverages[metricID] = cov
	c.lastCoverage = cov
}

func logFetchSkip(metric, series string, err error) {
	log.Warn().Str("error", pluginrunner.SanitizeCollectionError(err)).
		Str("metric", metric).Str("series", series).
		Msg("FRED fetch failed; skipping metric")
}
