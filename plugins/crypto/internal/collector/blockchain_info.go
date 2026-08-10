// Package collector 子模块：blockchain.com public chart API 采集 BTC 链上指标。
// 提供交易数量等链上活跃度数据，无需 API key。
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


// blockchainInfoChartResponse represents the chart API response for n-transactions.
type blockchainInfoChartResponse struct {
	Values []struct {
		X int64   `json:"x"` // unix timestamp
		Y float64 `json:"y"` // value
	} `json:"values"`
}

// BlockchainInfoCollector fetches on-chain metrics from blockchain.com's
// public chart API. No API key required. Conservative polling recommended.
type BlockchainInfoCollector struct {
	client *http.Client
}

// NewBlockchainInfoCollector creates a blockchain.com collector.
func NewBlockchainInfoCollector() *BlockchainInfoCollector {
	return &BlockchainInfoCollector{client: &http.Client{Timeout: 15 * time.Second}}
}

// GetTransactionCount returns the latest daily transaction count for BTC.
func (b *BlockchainInfoCollector) GetTransactionCount(ctx context.Context) (float64, time.Time, error) {
	// Fetch the most recent 30 days to find the latest non-zero value
	end := time.Now()
	start := end.AddDate(0, 0, -30)
	url := fmt.Sprintf(
		"https://api.blockchain.info/charts/n-transactions?start=%s&end=%s&format=json&timespan=30days",
		start.Format("2006-01-02"), end.Format("2006-01-02"),
	)
	body, err := b.doGet(ctx, url)
	if err != nil {
		return 0, time.Time{}, err
	}

	var resp blockchainInfoChartResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, time.Time{}, fmt.Errorf("decode blockchain.info response: %w", err)
	}

	// Find the latest non-zero observation
	for i := len(resp.Values) - 1; i >= 0; i-- {
		if resp.Values[i].Y > 0 {
			return resp.Values[i].Y, time.Unix(resp.Values[i].X, 0), nil
		}
	}
	return 0, time.Time{}, fmt.Errorf("no valid transaction count in blockchain.info response")
}

// GetTransactionHistory returns daily transaction counts across [start, end].
func (b *BlockchainInfoCollector) GetTransactionHistory(ctx context.Context, start, end time.Time) ([]time.Time, []float64, error) {
	// blockchain.info allows long timespans; cap at 365 days for stability
	maxWindow := 365 * 24 * time.Hour
	if end.Sub(start) > maxWindow {
		start = end.Add(-maxWindow)
	}
	url := fmt.Sprintf(
		"https://api.blockchain.info/charts/n-transactions?start=%s&end=%s&format=json",
		start.Format("2006-01-02"), end.Format("2006-01-02"),
	)
	body, err := b.doGet(ctx, url)
	if err != nil {
		return nil, nil, err
	}

	var resp blockchainInfoChartResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, nil, fmt.Errorf("decode blockchain.info response: %w", err)
	}

	times := make([]time.Time, 0, len(resp.Values))
	values := make([]float64, 0, len(resp.Values))
	for _, v := range resp.Values {
		if v.Y > 0 {
			times = append(times, time.Unix(v.X, 0))
			values = append(values, v.Y)
		}
	}
	return times, values, nil
}

// doGet performs an HTTP GET and returns the response body.
func (b *BlockchainInfoCollector) doGet(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "capital-observatory/0.1.0")
	req.Header.Set("Accept", "application/json")

	resp, err := b.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http GET: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("blockchain.info returned %d: %s", resp.StatusCode, string(body))
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}


// FormatTxSnapshots converts time/values to plugin snapshots.
func FormatTxSnapshots(times []time.Time, values []float64, fetchedAt time.Time, provider string) []pluginrunner.Snapshot {
	snaps := make([]pluginrunner.Snapshot, 0, len(times))
	for i, ts := range times {
		if values[i] == 0 {
			continue
		}
		snaps = append(snaps, pluginrunner.Snapshot{
			MetricID:    "btc.ass.tx_count",
			Value:       values[i],
			Timestamp:   ts,
			FetchedAt:   fetchedAt,
			Provider:    provider,
			SourceClass: model.SourceClassReal,
			Grade:       "delayed",
		})
	}
	return snaps
}

// LogTxError logs a transaction count fetch error.
func LogTxError(err error) {
	log.Warn().Err(err).Msg("blockchain.info tx_count fetch failed")
}
