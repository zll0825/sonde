// Package collector 提供 etf 插件的数据采集器：Yahoo Finance 真实数据源。
// 采集 GLD 的价格、交易量指标，使用 provider safety 包保护 API 访问。
package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"capital_observatory/pkg/model"
	"capital_observatory/pkg/pluginrunner"
	"capital_observatory/pkg/provider"
)

// providerYahoo is the source_provider recorded on Yahoo-sourced observations.
const providerYahoo = "yahoo_finance"

// YahooCollector fetches real GLD data from Yahoo Finance's public chart API
// with rate limiting and circuit breaker protection.
type YahooCollector struct {
	client *provider.SafeHTTPClient
}

// NewYahooCollector creates a Yahoo Finance collector with safety mechanisms.
func NewYahooCollector() *YahooCollector {
	return &YahooCollector{
		client: provider.NewSafeHTTPClient(provider.YahooFinanceConfig()),
	}
}

// yahooChartResponse is the subset of the v8/finance/chart JSON we care about.
type yahooChartResponse struct {
	Chart struct {
		Result []struct {
			Meta struct {
				RegularMarketPrice       float64 `json:"regularMarketPrice"`
				PreviousClose            float64 `json:"previousClose"`
				Symbol                   string  `json:"symbol"`
				RegularMarketVolume      int64   `json:"regularMarketVolume"`
				AverageDailyVolume3Month int64   `json:"averageDailyVolume3Month"`
			} `json:"meta"`
			Timestamp  []int64 `json:"timestamp"`
			Indicators struct {
				Quote []struct {
					Close  []*float64 `json:"close"`
					Volume []*int64   `json:"volume"`
				} `json:"quote"`
			} `json:"indicators"`
		} `json:"result"`
		Error *struct {
			Code        string `json:"code"`
			Description string `json:"description"`
		} `json:"error"`
	} `json:"chart"`
}

// GetSnapshots returns real observations for gld.ass.price and gld.ass.volume.
func (y *YahooCollector) GetSnapshots(ctx context.Context) ([]pluginrunner.Snapshot, error) {
	price, volume, err := y.fetchLatestPriceAndVolume(ctx, "GLD")
	if err != nil {
		return nil, err
	}
	fetchedAt := time.Now()

	return []pluginrunner.Snapshot{
		{
			MetricID:    "gld.ass.price",
			Value:       price,
			Timestamp:   fetchedAt,
			FetchedAt:   fetchedAt,
			Provider:    providerYahoo,
			SourceClass: model.SourceClassReal,
			Grade:       "delayed",
		},
		{
			MetricID:    "gld.ass.volume",
			Value:       volume,
			Timestamp:   fetchedAt,
			FetchedAt:   fetchedAt,
			Provider:    providerYahoo,
			SourceClass: model.SourceClassReal,
			Grade:       "delayed",
		},
	}, nil
}

// GetSnapshotsForWindow serves the backfill path with REAL Yahoo daily closes
// and volumes for [start, end].
func (y *YahooCollector) GetSnapshotsForWindow(ctx context.Context, start, end time.Time) ([]pluginrunner.Snapshot, error) {
	if end.Before(start) {
		end = start
	}

	times, closes, volumes, err := y.fetchDailyClosesAndVolumes(ctx, "GLD", start, end)
	if err != nil {
		return nil, err
	}
	fetchedAt := time.Now()

	snapshots := make([]pluginrunner.Snapshot, 0, len(times)*2)
	for i, ts := range times {
		if closes[i] == nil {
			continue
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
		if volumes[i] != nil {
			snapshots = append(snapshots, pluginrunner.Snapshot{
				MetricID:    "gld.ass.volume",
				Value:       float64(*volumes[i]),
				Timestamp:   ts,
				FetchedAt:   fetchedAt,
				Provider:    providerYahoo,
				SourceClass: model.SourceClassReal,
				Grade:       "delayed",
			})
		}
	}
	return snapshots, nil
}

// fetchLatestPriceAndVolume queries the current price and daily trading volume.
func (y *YahooCollector) fetchLatestPriceAndVolume(ctx context.Context, symbol string) (float64, float64, error) {
	url := fmt.Sprintf("https://query1.finance.yahoo.com/v8/finance/chart/%s?interval=1m&range=1d", symbol)
	yc, err := y.fetchChart(ctx, url, symbol)
	if err != nil {
		return 0, 0, err
	}

	meta := yc.Chart.Result[0].Meta
	var price float64
	if meta.RegularMarketPrice != 0 {
		price = meta.RegularMarketPrice
	} else if meta.PreviousClose != 0 {
		price = meta.PreviousClose
	} else {
		return 0, 0, fmt.Errorf("no price in yahoo response for %s", symbol)
	}

	var volume float64
	if meta.RegularMarketVolume != 0 {
		volume = float64(meta.RegularMarketVolume)
	} else if meta.AverageDailyVolume3Month != 0 {
		volume = float64(meta.AverageDailyVolume3Month)
	}
	return price, volume, nil
}

// fetchDailyClosesAndVolumes queries real daily close bars and volume for [start, end].
func (y *YahooCollector) fetchDailyClosesAndVolumes(ctx context.Context, symbol string, start, end time.Time) ([]time.Time, []*float64, []*int64, error) {
	url := fmt.Sprintf(
		"https://query1.finance.yahoo.com/v8/finance/chart/%s?interval=1d&period1=%d&period2=%d",
		symbol, start.Unix(), end.Unix())
	yc, err := y.fetchChart(ctx, url, symbol)
	if err != nil {
		return nil, nil, nil, err
	}

	result := yc.Chart.Result[0]
	if len(result.Indicators.Quote) == 0 {
		return nil, nil, nil, fmt.Errorf("no quote series in yahoo response for %s", symbol)
	}
	closes := result.Indicators.Quote[0].Close
	volumes := result.Indicators.Quote[0].Volume
	if len(result.Timestamp) != len(closes) {
		return nil, nil, nil, fmt.Errorf("yahoo series length mismatch for %s: %d timestamps vs %d closes",
			symbol, len(result.Timestamp), len(closes))
	}

	times := make([]time.Time, len(result.Timestamp))
	for i, ts := range result.Timestamp {
		times[i] = time.Unix(ts, 0)
	}
	return times, closes, volumes, nil
}

// fetchChart performs the HTTP GET with safety mechanisms and decodes the response.
func (y *YahooCollector) fetchChart(ctx context.Context, url, symbol string) (*yahooChartResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")
	req.Header.Set("Accept", "application/json")

	resp, err := y.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("yahoo %s: %w", symbol, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := provider.ReadAll(resp, 512)
		return nil, fmt.Errorf("yahoo %s returned %d: %s", symbol, resp.StatusCode, string(body))
	}

	body, err := provider.ReadAll(resp, 1<<20)
	if err != nil {
		return nil, fmt.Errorf("read yahoo %s: %w", symbol, err)
	}

	var yc yahooChartResponse
	if err := json.Unmarshal(body, &yc); err != nil {
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
