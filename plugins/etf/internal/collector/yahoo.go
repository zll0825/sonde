// Package collector 提供 etf 插件的数据采集器：Yahoo Finance 真实价格源
// 与离线 mock。
package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"

	"capital_observatory/pkg/model"
	"capital_observatory/pkg/pluginrunner"
)

// providerYahoo is the source_provider recorded on Yahoo-sourced observations.
// It is part of the observation idempotency key — renaming it would orphan
// previously ingested rows under the old name.
const providerYahoo = "yahoo_finance"

// YahooCollector fetches real GLD price data from Yahoo Finance's public chart
// API (no API key required). ETF flow data is NOT available from free public
// APIs, so flow metrics come from MockCollector and are honestly labeled
// Provider="mock_etf" / Grade="estimated" — synthetic data must never wear a
// real provider's name. The MVP requirement "one real metric end-to-end" is
// carried by gld.ass.price.
type YahooCollector struct {
	client *http.Client
}

// NewYahooCollector creates a Yahoo Finance collector with a default HTTP client.
func NewYahooCollector() *YahooCollector {
	return &YahooCollector{client: &http.Client{Timeout: 15 * time.Second}}
}

// yahooChartResponse is the subset of the v8/finance/chart JSON we care about.
// Indicators carry the historical close series used by the backfill path.
type yahooChartResponse struct {
	Chart struct {
		Result []struct {
			Meta struct {
				RegularMarketPrice float64 `json:"regularMarketPrice"`
				PreviousClose      float64 `json:"previousClose"`
				Symbol             string  `json:"symbol"`
			} `json:"meta"`
			Timestamp  []int64 `json:"timestamp"`
			Indicators struct {
				Quote []struct {
					Close []*float64 `json:"close"`
				} `json:"quote"`
			} `json:"indicators"`
		} `json:"result"`
		Error *struct {
			Code        string `json:"code"`
			Description string `json:"description"`
		} `json:"error"`
	} `json:"chart"`
}

// GetSnapshots returns a real observation for gld.ass.price plus mock flow metrics.
//
// Error handling: if Yahoo Finance is unreachable, the error is logged and the
// collector degrades to MockCollector for ALL metrics (Provider="mock_etf", so
// downstream can always tell real from synthetic).
func (y *YahooCollector) GetSnapshots(ctx context.Context) ([]pluginrunner.Snapshot, error) {
	price, err := y.fetchLatestPrice(ctx, "GLD")
	if err != nil {
		log.Warn().Err(err).Msg("Yahoo Finance unavailable, falling back to mock ETF data")
		return Mock{}.GetSnapshots(ctx)
	}
	fetchedAt := time.Now()

	snapshots := []pluginrunner.Snapshot{
		{
			MetricID:    "gld.ass.price",
			Value:       price,
			Timestamp:   fetchedAt,
			FetchedAt:   fetchedAt,
			Provider:    providerYahoo,
			SourceClass: model.SourceClassReal,
			Grade:       "delayed", // Yahoo data is ~15min delayed, not realtime
		},
	}

	// Flow metrics have no free public source — synthesize via Mock, keeping
	// its own provider/grade labels so the data never masquerades as Yahoo's.
	mockSnaps, merr := Mock{}.GetSnapshots(ctx)
	if merr != nil {
		log.Warn().Err(merr).Msg("mock ETF flow fallback failed")
		return snapshots, nil
	}
	for _, s := range mockSnaps {
		if s.MetricID != "gld.ass.price" {
			snapshots = append(snapshots, s)
		}
	}
	return snapshots, nil
}

// GetSnapshotsForWindow serves the backfill path with REAL Yahoo daily closes
// for gld.ass.price across [start, end], plus Mock's synthesized flow history.
// If Yahoo is unreachable the whole window degrades to Mock.
func (y *YahooCollector) GetSnapshotsForWindow(ctx context.Context, start, end time.Time) ([]pluginrunner.Snapshot, error) {
	if end.Before(start) {
		end = start
	}

	times, closes, err := y.fetchDailyCloses(ctx, "GLD", start, end)
	if err != nil {
		log.Warn().Err(err).Msg("Yahoo Finance history unavailable, falling back to mock window")
		return Mock{}.GetSnapshotsForWindow(ctx, start, end)
	}
	fetchedAt := time.Now()

	snapshots := make([]pluginrunner.Snapshot, 0, len(times)*2)
	for i, ts := range times {
		if closes[i] == nil {
			continue // market holiday / missing bar
		}
		snapshots = append(snapshots, pluginrunner.Snapshot{
			MetricID:    "gld.ass.price",
			Value:       *closes[i],
			Timestamp:   ts,
			FetchedAt:   fetchedAt,
			Provider:    providerYahoo,
			SourceClass: model.SourceClassReal,
			Grade:       "delayed",
		})
	}

	mockSnaps, merr := Mock{}.GetSnapshotsForWindow(ctx, start, end)
	if merr != nil {
		log.Warn().Err(merr).Msg("mock ETF flow window fallback failed")
		return snapshots, nil
	}
	for _, s := range mockSnaps {
		if s.MetricID != "gld.ass.price" {
			snapshots = append(snapshots, s)
		}
	}
	return snapshots, nil
}

// fetchLatestPrice queries the current price for a symbol.
func (y *YahooCollector) fetchLatestPrice(ctx context.Context, symbol string) (float64, error) {
	url := fmt.Sprintf("https://query1.finance.yahoo.com/v8/finance/chart/%s?interval=1m&range=1d", symbol)
	yc, err := y.fetchChart(ctx, url, symbol)
	if err != nil {
		return 0, err
	}
	meta := yc.Chart.Result[0].Meta
	if meta.RegularMarketPrice != 0 {
		return meta.RegularMarketPrice, nil
	}
	if meta.PreviousClose != 0 {
		return meta.PreviousClose, nil
	}
	return 0, fmt.Errorf("no price in yahoo response for %s", symbol)
}

// fetchDailyCloses queries real daily close bars for [start, end].
func (y *YahooCollector) fetchDailyCloses(ctx context.Context, symbol string, start, end time.Time) ([]time.Time, []*float64, error) {
	url := fmt.Sprintf(
		"https://query1.finance.yahoo.com/v8/finance/chart/%s?interval=1d&period1=%d&period2=%d",
		symbol, start.Unix(), end.Unix())
	yc, err := y.fetchChart(ctx, url, symbol)
	if err != nil {
		return nil, nil, err
	}

	result := yc.Chart.Result[0]
	if len(result.Indicators.Quote) == 0 {
		return nil, nil, fmt.Errorf("no quote series in yahoo response for %s", symbol)
	}
	closes := result.Indicators.Quote[0].Close
	if len(result.Timestamp) != len(closes) {
		return nil, nil, fmt.Errorf("yahoo series length mismatch for %s: %d timestamps vs %d closes",
			symbol, len(result.Timestamp), len(closes))
	}

	times := make([]time.Time, len(result.Timestamp))
	for i, ts := range result.Timestamp {
		times[i] = time.Unix(ts, 0)
	}
	return times, closes, nil
}

// fetchChart performs the HTTP GET and decodes/validates the chart envelope.
func (y *YahooCollector) fetchChart(ctx context.Context, url, symbol string) (*yahooChartResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	// Yahoo blocks non-browser User-Agents; present a realistic one.
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")

	resp, err := y.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http GET %s: %w", symbol, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("yahoo %s returned %d: %s", symbol, resp.StatusCode, string(body))
	}

	var yc yahooChartResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&yc); err != nil {
		return nil, fmt.Errorf("decode yahoo response for %s: %w", symbol, err)
	}
	if yc.Chart.Error != nil {
		return nil, fmt.Errorf("yahoo error for %s: %s — %s", symbol, yc.Chart.Error.Code, yc.Chart.Error.Description)
	}
	if len(yc.Chart.Result) == 0 {
		return nil, fmt.Errorf("empty yahoo result for %s", symbol)
	}
	return &yc, nil
}
