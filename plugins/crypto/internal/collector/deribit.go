// Package collector 子模块：Deribit BTC 波动率指数（DVOL）日线。
// 公共接口、无需密钥；DVOL 基于 Deribit 自家 BTC 期权，是单交易所隐含波动率。
package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"time"

	"sonde/pkg/provider"
)

const (
	providerDeribit = "deribit"

	// MetricDVOL 是 Deribit BTC DVOL 日收盘值（年化隐含波动率指数点）。
	MetricDVOL = "btc.ass.dvol"

	deribitDefaultBase = "https://www.deribit.com"
	deribitMaxPages    = 20 // 1000 bars per page
	deribitMaxBody     = 2 << 20
)

// DeribitCollector reads BTC DVOL daily candles from Deribit's public API.
type DeribitCollector struct {
	client *provider.SafeHTTPClient
	base   string
	now    func() time.Time
}

// NewDeribitCollector creates a Deribit collector with safety defaults.
func NewDeribitCollector() *DeribitCollector {
	return &DeribitCollector{client: provider.NewSafeHTTPClient(provider.DeribitConfig()), base: deribitDefaultBase, now: time.Now}
}

type deribitVolResponse struct {
	Result *struct {
		// Data rows are [ts_ms, open, high, low, close].
		Data         [][]float64 `json:"data"`
		Continuation *int64      `json:"continuation"`
	} `json:"result"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// GetDVOLHistory returns completed daily DVOL closes whose bar start lies in
// [start, end]. The bar for the current UTC day is still forming and is
// excluded. Timestamps are the bar's UTC day start.
func (c *DeribitCollector) GetDVOLHistory(ctx context.Context, start, end time.Time) ([]time.Time, []float64, error) {
	now := c.now().UTC()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	byTS := map[int64]float64{}
	cursor := end.UnixMilli()
	for page := 0; page < deribitMaxPages; page++ {
		q := url.Values{
			"currency":        {"BTC"},
			"resolution":      {"1D"},
			"start_timestamp": {strconv.FormatInt(start.UnixMilli(), 10)},
			"end_timestamp":   {strconv.FormatInt(cursor, 10)},
		}
		body, err := getJSON(ctx, c.client, c.base+"/api/v2/public/get_volatility_index_data?"+q.Encode(), deribitMaxBody)
		if err != nil {
			return nil, nil, fmt.Errorf("Deribit DVOL: %w", err)
		}
		var resp deribitVolResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, nil, fmt.Errorf("decode Deribit DVOL: %w", err)
		}
		if resp.Error != nil {
			return nil, nil, fmt.Errorf("Deribit DVOL: %d %s", resp.Error.Code, resp.Error.Message)
		}
		if resp.Result == nil {
			return nil, nil, fmt.Errorf("Deribit DVOL: missing result")
		}
		for _, row := range resp.Result.Data {
			if len(row) < 5 || row[4] <= 0 {
				continue
			}
			ts := time.UnixMilli(int64(row[0])).UTC()
			if !ts.Before(todayStart) || ts.Before(start) || ts.After(end) {
				continue
			}
			byTS[ts.UnixMilli()] = row[4]
		}
		next := resp.Result.Continuation
		if next == nil || *next >= cursor || *next < start.UnixMilli() {
			break
		}
		cursor = *next
	}
	keys := make([]int64, 0, len(byTS))
	for k := range byTS {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	times := make([]time.Time, 0, len(keys))
	values := make([]float64, 0, len(keys))
	for _, k := range keys {
		times = append(times, time.UnixMilli(k).UTC())
		values = append(values, byTS[k])
	}
	return times, values, nil
}

// GetLatestDVOL returns the most recent completed daily close.
func (c *DeribitCollector) GetLatestDVOL(ctx context.Context) (float64, time.Time, error) {
	now := c.now()
	times, values, err := c.GetDVOLHistory(ctx, now.AddDate(0, 0, -7), now)
	if err != nil {
		return 0, time.Time{}, err
	}
	if len(values) == 0 {
		return 0, time.Time{}, fmt.Errorf("Deribit DVOL: no completed daily bar in last 7 days")
	}
	return values[len(values)-1], times[len(times)-1], nil
}
