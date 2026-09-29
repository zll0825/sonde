// Package collector 子模块：DefiLlama 稳定币 API 采集美元稳定币总流通市值。
// 免费、无需 API key；一次请求返回全部日度历史。使用 SafeHTTPClient 保护。
package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"sonde/pkg/model"
	"sonde/pkg/pluginrunner"
	"sonde/pkg/provider"
)

const (
	providerDefiLlama = "defillama"

	// MetricStablecoinSupply 是美元锚定稳定币的总流通市值（美元）。
	MetricStablecoinSupply = "stable.ass.total_supply"

	stablecoinChartsURL = "https://stablecoins.llama.fi/stablecoincharts/all"

	// 全历史自 2017 年起约三千条日度记录，每条带各锚定币种，1 MiB 不够。
	stablecoinMaxBody = 16 << 20
)

// StablecoinCollector fetches total USD-pegged stablecoin supply from DefiLlama.
type StablecoinCollector struct {
	client *provider.SafeHTTPClient
}

// NewStablecoinCollector creates a DefiLlama collector with safety defaults.
func NewStablecoinCollector() *StablecoinCollector {
	return &StablecoinCollector{client: provider.NewSafeHTTPClient(provider.DefiLlamaConfig())}
}

// stablecoinChartPoint is one day of /stablecoincharts/all. date is a unix-seconds
// string; totalCirculatingUSD is keyed by peg type (peggedUSD, peggedEUR, ...).
type stablecoinChartPoint struct {
	Date                string             `json:"date"`
	TotalCirculatingUSD map[string]float64 `json:"totalCirculatingUSD"`
}

// GetSupplyHistory returns daily USD-pegged stablecoin supply within [start, end].
// A zero start means no lower bound.
func (s *StablecoinCollector) GetSupplyHistory(ctx context.Context, start, end time.Time) ([]time.Time, []float64, error) {
	body, err := s.doGet(ctx, stablecoinChartsURL)
	if err != nil {
		return nil, nil, err
	}
	return parseStablecoinCharts(body, start, end)
}

// GetLatestSupply returns the most recent USD-pegged stablecoin supply.
func (s *StablecoinCollector) GetLatestSupply(ctx context.Context) (float64, time.Time, error) {
	times, values, err := s.GetSupplyHistory(ctx, time.Time{}, time.Now())
	if err != nil {
		return 0, time.Time{}, err
	}
	if len(values) == 0 {
		return 0, time.Time{}, fmt.Errorf("no stablecoin supply in DefiLlama response")
	}
	return values[len(values)-1], times[len(times)-1], nil
}

func parseStablecoinCharts(body []byte, start, end time.Time) ([]time.Time, []float64, error) {
	var points []stablecoinChartPoint
	if err := json.Unmarshal(body, &points); err != nil {
		return nil, nil, fmt.Errorf("decode DefiLlama stablecoin charts: %w", err)
	}
	times := make([]time.Time, 0, len(points))
	values := make([]float64, 0, len(points))
	for _, p := range points {
		sec, err := strconv.ParseInt(p.Date, 10, 64)
		if err != nil {
			continue
		}
		ts := time.Unix(sec, 0).UTC()
		if (!start.IsZero() && ts.Before(start)) || ts.After(end) {
			continue
		}
		v := p.TotalCirculatingUSD["peggedUSD"]
		if v <= 0 {
			continue
		}
		times = append(times, ts)
		values = append(values, v)
	}
	return times, values, nil
}

func (s *StablecoinCollector) doGet(ctx context.Context, urlStr string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "sonde/0.1.0")
	req.Header.Set("Accept", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := provider.ReadAll(resp, 512)
		return nil, fmt.Errorf("DefiLlama returned %d: %s", resp.StatusCode, string(body))
	}
	return provider.ReadAllBounded(resp, stablecoinMaxBody)
}

// formatStablecoinSnapshots converts time/values to plugin snapshots.
func formatStablecoinSnapshots(times []time.Time, values []float64, fetchedAt time.Time) []pluginrunner.Snapshot {
	snaps := make([]pluginrunner.Snapshot, 0, len(times))
	for i, ts := range times {
		snaps = append(snaps, pluginrunner.Snapshot{
			MetricID:    MetricStablecoinSupply,
			Value:       values[i],
			Timestamp:   ts,
			FetchedAt:   fetchedAt,
			Provider:    providerDefiLlama,
			SourceClass: model.SourceClassReal,
			Grade:       "delayed",
		})
	}
	return snaps
}
