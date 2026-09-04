package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"sonde/pkg/model"
	"sonde/pkg/pluginrunner"
	"sonde/pkg/provider"
)

const (
	providerAlphaVantage       = "alpha_vantage"
	goldMetricID               = "metal.precious.gold"
	alphaVantageEndpoint       = "https://www.alphavantage.co/query"
	alphaVantageGoldNominal    = "XAUUSD"
	alphaVantageResponseLimit  = 2 << 20
	maxAlphaVantageHistoryRows = 4000
	maxGoldSpotAge             = 7 * 24 * time.Hour
	maxGoldFutureSkew          = 5 * time.Minute

	// MaxGoldHistoricalWindow matches the existing bounded Commodities backfill contract.
	MaxGoldHistoricalWindow = MaxHistoricalWindow
)

type alphaVantageEnvelope struct {
	Nominal      string `json:"nominal"`
	Information  string `json:"Information"`
	Note         string `json:"Note"`
	ErrorMessage string `json:"Error Message"`
}

type alphaVantageSpotResponse struct {
	Nominal   string `json:"nominal"`
	Timestamp string `json:"timestamp"`
	Price     string `json:"price"`
}

type alphaVantageHistoryResponse struct {
	Nominal string `json:"nominal"`
	Data    []struct {
		Date  string `json:"date"`
		Price string `json:"price"`
	} `json:"data"`
}

// AlphaVantageGoldCollector fetches XAUUSD spot and daily history without
// changing the physical/spot semantics of metal.precious.gold.
type AlphaVantageGoldCollector struct {
	apiKey string
	client *provider.SafeHTTPClient
	now    func() time.Time

	mu       sync.Mutex
	coverage provider.BackfillCoverage
	hasCover bool
}

// NewAlphaVantageGoldCollector fails fast when the required key is missing.
func NewAlphaVantageGoldCollector() (*AlphaVantageGoldCollector, error) {
	apiKey := strings.TrimSpace(os.Getenv("ALPHAVANTAGE_API_KEY"))
	if apiKey == "" {
		return nil, errors.New("ALPHAVANTAGE_API_KEY env var is required but not set")
	}
	return &AlphaVantageGoldCollector{
		apiKey: apiKey,
		client: provider.SharedAlphaVantageClient(),
		now:    time.Now,
	}, nil
}

// CircuitState returns the Alpha Vantage circuit state for health reporting.
func (a *AlphaVantageGoldCollector) CircuitState() string {
	if a.client == nil {
		return "unknown"
	}
	return a.client.CircuitState()
}

// LastCoverage returns the most recent bounded gold-history coverage result.
func (a *AlphaVantageGoldCollector) LastCoverage(metricID string) (provider.BackfillCoverage, bool) {
	if metricID != goldMetricID {
		return provider.BackfillCoverage{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.coverage, a.hasCover
}

// GetSnapshots returns the current XAUUSD spot quote.
func (a *AlphaVantageGoldCollector) GetSnapshots(ctx context.Context) ([]pluginrunner.Snapshot, error) {
	body, fetchedAt, err := a.doQuery(ctx, "GOLD_SILVER_SPOT", nil)
	if err != nil {
		return nil, err
	}
	if err := validateAlphaVantageEnvelope(body); err != nil {
		return nil, err
	}

	var payload alphaVantageSpotResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode Alpha Vantage gold spot response: %w", err)
	}
	if payload.Nominal != alphaVantageGoldNominal {
		return nil, fmt.Errorf("Alpha Vantage gold spot returned unexpected nominal %q", payload.Nominal)
	}
	value, err := parsePositivePrice(payload.Price)
	if err != nil {
		return nil, fmt.Errorf("Alpha Vantage gold spot price: %w", err)
	}
	timestamp, err := time.ParseInLocation("2006-01-02 15:04:05", payload.Timestamp, time.UTC)
	if err != nil {
		return nil, fmt.Errorf("Alpha Vantage gold spot timestamp: %w", err)
	}
	if timestamp.After(fetchedAt.Add(maxGoldFutureSkew)) {
		return nil, errors.New("Alpha Vantage gold spot timestamp is in the future")
	}
	if fetchedAt.Sub(timestamp) > maxGoldSpotAge {
		return nil, errors.New("Alpha Vantage gold spot observation is stale")
	}

	return []pluginrunner.Snapshot{{
		MetricID:    goldMetricID,
		Value:       value,
		Timestamp:   timestamp.UTC(),
		FetchedAt:   fetchedAt,
		Provider:    providerAlphaVantage,
		SourceClass: model.SourceClassReal,
		Grade:       "realtime",
	}}, nil
}

// GetSnapshotsForWindow returns bounded daily XAUUSD closes in [start, end].
func (a *AlphaVantageGoldCollector) GetSnapshotsForWindow(ctx context.Context, start, end time.Time) ([]pluginrunner.Snapshot, error) {
	if end.Before(start) {
		return nil, errors.New("gold history end precedes start")
	}
	if end.Sub(start) > MaxGoldHistoricalWindow {
		return nil, fmt.Errorf("gold history window exceeds %s", MaxGoldHistoricalWindow)
	}

	body, fetchedAt, err := a.doQuery(ctx, "GOLD_SILVER_HISTORY", url.Values{"interval": {"daily"}})
	if err != nil {
		return nil, err
	}
	if err := validateAlphaVantageEnvelope(body); err != nil {
		return nil, err
	}

	var payload alphaVantageHistoryResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode Alpha Vantage gold history response: %w", err)
	}
	if payload.Nominal != alphaVantageGoldNominal {
		return nil, fmt.Errorf("Alpha Vantage gold history returned unexpected nominal %q", payload.Nominal)
	}
	if len(payload.Data) == 0 {
		a.recordCoverage(nil)
		return nil, errors.New("Alpha Vantage gold history returned no data")
	}

	byDate := make(map[int64]pluginrunner.Snapshot)
	for _, row := range payload.Data {
		timestamp, err := time.ParseInLocation("2006-01-02", row.Date, time.UTC)
		if err != nil {
			return nil, fmt.Errorf("Alpha Vantage gold history date: %w", err)
		}
		if timestamp.Before(start) || timestamp.After(end) {
			continue
		}
		key := timestamp.Unix()
		if _, exists := byDate[key]; exists {
			continue
		}
		value, err := parsePositivePrice(row.Price)
		if err != nil {
			return nil, fmt.Errorf("Alpha Vantage gold history price at %s: %w", row.Date, err)
		}
		byDate[key] = pluginrunner.Snapshot{
			MetricID:    goldMetricID,
			Value:       value,
			Timestamp:   timestamp,
			FetchedAt:   fetchedAt,
			Provider:    providerAlphaVantage,
			SourceClass: model.SourceClassReal,
			Grade:       "delayed",
		}
		if len(byDate) > maxAlphaVantageHistoryRows {
			return nil, fmt.Errorf("Alpha Vantage gold history exceeds %d accepted rows", maxAlphaVantageHistoryRows)
		}
	}

	snapshots := make([]pluginrunner.Snapshot, 0, len(byDate))
	for _, snapshot := range byDate {
		snapshots = append(snapshots, snapshot)
	}
	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].Timestamp.Before(snapshots[j].Timestamp)
	})
	a.recordCoverage(snapshots)
	if len(snapshots) == 0 {
		return nil, errors.New("Alpha Vantage gold history has no observations in requested window")
	}
	return snapshots, nil
}

func (a *AlphaVantageGoldCollector) doQuery(ctx context.Context, function string, extra url.Values) ([]byte, time.Time, error) {
	endpoint, err := url.Parse(alphaVantageEndpoint)
	if err != nil {
		return nil, time.Time{}, errors.New("invalid Alpha Vantage endpoint")
	}
	query := endpoint.Query()
	query.Set("function", function)
	query.Set("symbol", "GOLD")
	query.Set("apikey", a.apiKey)
	for key, values := range extra {
		for _, value := range values {
			query.Add(key, value)
		}
	}
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, time.Time{}, errors.New("create Alpha Vantage request")
	}
	req.Header.Set("User-Agent", "sonde/0.2.0")
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("http GET %s: %w", req.URL.Path, provider.SanitizeTransportError(err))
	}
	if resp.StatusCode != http.StatusOK {
		_, _ = provider.ReadAll(resp, 512)
		return nil, time.Time{}, fmt.Errorf("Alpha Vantage returned HTTP %d", resp.StatusCode)
	}
	body, err := provider.ReadAllBounded(resp, alphaVantageResponseLimit)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("read Alpha Vantage response: %w", err)
	}
	return body, a.clockNow().UTC(), nil
}

func (a *AlphaVantageGoldCollector) clockNow() time.Time {
	if a.now == nil {
		return time.Now()
	}
	return a.now()
}

func validateAlphaVantageEnvelope(body []byte) error {
	if len(strings.TrimSpace(string(body))) == 0 {
		return errors.New("Alpha Vantage returned an empty response")
	}
	var envelope alphaVantageEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("decode Alpha Vantage response envelope: %w", err)
	}
	switch {
	case envelope.Information != "":
		return errors.New("Alpha Vantage returned an informational response")
	case envelope.Note != "":
		return errors.New("Alpha Vantage rate limit response")
	case envelope.ErrorMessage != "":
		return errors.New("Alpha Vantage provider error response")
	default:
		return nil
	}
}

func parsePositivePrice(raw string) (float64, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, errors.New("missing value")
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 {
		return 0, errors.New("value must be finite and greater than zero")
	}
	return value, nil
}

func (a *AlphaVantageGoldCollector) recordCoverage(snapshots []pluginrunner.Snapshot) {
	coverage := provider.BackfillCoverage{MetricID: goldMetricID, SampleCount: len(snapshots)}
	if len(snapshots) > 0 {
		coverage.ActualStart = snapshots[0].Timestamp
		coverage.ActualEnd = snapshots[len(snapshots)-1].Timestamp
		times := make([]time.Time, len(snapshots))
		for i, snapshot := range snapshots {
			times[i] = snapshot.Timestamp
		}
		gaps := provider.DetectGaps(times, "daily")
		coverage.HasGaps = len(gaps) > 0
		coverage.GapCount = len(gaps)
		if len(gaps) > 0 {
			log.Warn().Str("metric", goldMetricID).Int("gap_count", len(gaps)).
				Msg("Alpha Vantage gold history timeline gaps detected")
		}
	}
	a.mu.Lock()
	a.coverage = coverage
	a.hasCover = true
	a.mu.Unlock()
}
