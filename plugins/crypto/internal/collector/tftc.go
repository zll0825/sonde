// Package collector 子模块：TFTC 美国现货比特币 ETF 日度净流入。
// tftc.io 提供静态 JSON（CC BY 4.0），一次请求返回自 2024-01-11 起全部日度数据。
package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"sonde/pkg/model"
	"sonde/pkg/pluginrunner"
	"sonde/pkg/provider"
)

const (
	providerTFTC = "tftc"

	// MetricETFNetFlow 是美国现货比特币 ETF 当日合计净流入（美元）。
	MetricETFNetFlow = "btc.ass.etf_net_flow"

	// tftc.io 308-redirects to www.tftc.io; use the final host directly.
	tftcFlowsURL = "https://www.tftc.io/bitcoin-etf-flows/data.json"
	tftcMaxBody  = 8 << 20
)

// TFTCCollector fetches US spot bitcoin ETF daily net flows from tftc.io.
type TFTCCollector struct {
	client *provider.SafeHTTPClient
	url    string
}

// NewTFTCCollector creates a TFTC collector with safety defaults.
func NewTFTCCollector() *TFTCCollector {
	return &TFTCCollector{client: provider.NewSafeHTTPClient(provider.TFTCConfig()), url: tftcFlowsURL}
}

// tftcFile is the subset of data.json we use. "units" is "USD" (whole
// dollars, not millions); per-fund values are ignored.
type tftcFile struct {
	Units string `json:"units"`
	Days  []struct {
		Date       string              `json:"date"`
		NetFlowUSD *float64            `json:"netFlowUsd"`
		PerETFUSD  map[string]*float64 `json:"perEtfUsd"`
	} `json:"days"`
}

// parseTFTCFlows returns daily aggregate net flows within [start, end].
// US market holidays appear as netFlowUsd 0 with perEtfUsd null; those are
// placeholders, not zero-flow trading days, and are skipped. A zero start
// means no lower bound.
func parseTFTCFlows(body []byte, start, end time.Time) ([]time.Time, []float64, error) {
	var f tftcFile
	if err := json.Unmarshal(body, &f); err != nil {
		return nil, nil, fmt.Errorf("decode TFTC ETF flows: %w", err)
	}
	if f.Units != "USD" {
		return nil, nil, fmt.Errorf("TFTC ETF flows: unexpected units %q (want USD)", f.Units)
	}
	times := make([]time.Time, 0, len(f.Days))
	values := make([]float64, 0, len(f.Days))
	for _, d := range f.Days {
		if d.NetFlowUSD == nil {
			continue
		}
		if *d.NetFlowUSD == 0 && d.PerETFUSD == nil {
			continue
		}
		ts, err := time.ParseInLocation("2006-01-02", d.Date, time.UTC)
		if err != nil {
			continue
		}
		if (!start.IsZero() && ts.Before(start)) || ts.After(end) {
			continue
		}
		times = append(times, ts)
		values = append(values, *d.NetFlowUSD)
	}
	return times, values, nil
}

// GetFlowHistory returns daily net flows within [start, end].
func (c *TFTCCollector) GetFlowHistory(ctx context.Context, start, end time.Time) ([]time.Time, []float64, error) {
	body, err := getJSON(ctx, c.client, c.url, tftcMaxBody)
	if err != nil {
		return nil, nil, fmt.Errorf("TFTC ETF flows: %w", err)
	}
	return parseTFTCFlows(body, start, end)
}

// GetLatestFlow returns the most recent trading day's net flow.
func (c *TFTCCollector) GetLatestFlow(ctx context.Context) (float64, time.Time, error) {
	times, values, err := c.GetFlowHistory(ctx, time.Time{}, time.Now())
	if err != nil {
		return 0, time.Time{}, err
	}
	if len(values) == 0 {
		return 0, time.Time{}, fmt.Errorf("no ETF flow rows in TFTC response")
	}
	return values[len(values)-1], times[len(times)-1], nil
}

func formatSeries(metricID, providerName string, times []time.Time, values []float64, fetchedAt time.Time) []pluginrunner.Snapshot {
	snaps := make([]pluginrunner.Snapshot, 0, len(times))
	for i, ts := range times {
		snaps = append(snaps, pluginrunner.Snapshot{
			MetricID:    metricID,
			Value:       values[i],
			Timestamp:   ts,
			FetchedAt:   fetchedAt,
			Provider:    providerName,
			SourceClass: model.SourceClassReal,
			Grade:       "delayed",
		})
	}
	return snaps
}

// getJSON performs a GET through the safe client and returns a bounded body.
func getJSON(ctx context.Context, client *provider.SafeHTTPClient, urlStr string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "sonde/0.1.0")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := provider.ReadAll(resp, 512)
		return nil, fmt.Errorf("%s returned %d: %s", req.URL.Host+req.URL.Path, resp.StatusCode, string(body))
	}
	return provider.ReadAllBounded(resp, limit)
}
