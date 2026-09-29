// Package collector 子模块：OKX 公共接口的 BTC-USDT-SWAP 未平仓量与资金费率。
// 仅覆盖 OKX 单交易所、单一 USDT 本位永续合约，不代表全市场杠杆。
package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/rs/zerolog/log"

	"sonde/pkg/provider"
)

const (
	providerOKX = "okx"

	// MetricOKXOpenInterest 是 OKX BTC-USDT-SWAP 未平仓量，以 BTC 计（oiCcy）。
	MetricOKXOpenInterest = "btc.ass.okx_open_interest"
	// MetricOKXFundingRate 是 OKX BTC-USDT-SWAP 已结算资金费率，% / 结算期（当前 8 小时）。
	MetricOKXFundingRate = "btc.ass.okx_funding_rate"

	okxDefaultBase = "https://www.okx.com"
	okxInstID      = "BTC-USDT-SWAP"
	okxPageLimit   = 100
	// okxMaxPages bounds one backfill: 100 daily bars per page → ~27 years.
	okxMaxPages   = 100
	okxMaxBody    = 2 << 20
	okxFundingGap = 8 * time.Hour
)

// OKXCollector reads BTC-USDT-SWAP open interest and funding from OKX v5 public endpoints.
type OKXCollector struct {
	client *provider.SafeHTTPClient
	base   string
	now    func() time.Time
}

// NewOKXCollector creates an OKX collector with safety defaults (≤2 req/s,
// well under OKX's 20 req/2s public limit).
func NewOKXCollector() *OKXCollector {
	return &OKXCollector{client: provider.NewSafeHTTPClient(provider.OKXConfig()), base: okxDefaultBase, now: time.Now}
}

type okxEnvelope struct {
	Code string          `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

func (c *OKXCollector) get(ctx context.Context, path string, q url.Values, out any) error {
	body, err := getJSON(ctx, c.client, c.base+path+"?"+q.Encode(), okxMaxBody)
	if err != nil {
		return fmt.Errorf("OKX %s: %w", path, err)
	}
	var env okxEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("decode OKX %s: %w", path, err)
	}
	if env.Code != "0" {
		return fmt.Errorf("OKX %s: code %s: %s", path, env.Code, env.Msg)
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return fmt.Errorf("decode OKX %s data: %w", path, err)
	}
	return nil
}

func parseMs(s string) (time.Time, error) {
	ms, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("bad ms timestamp %q", s)
	}
	return time.UnixMilli(ms).UTC(), nil
}

// GetLatestOpenInterest returns current open interest in BTC (oiCcy) and its timestamp.
func (c *OKXCollector) GetLatestOpenInterest(ctx context.Context) (float64, time.Time, error) {
	var rows []struct {
		OICcy string `json:"oiCcy"`
		TS    string `json:"ts"`
	}
	q := url.Values{"instType": {"SWAP"}, "instId": {okxInstID}}
	if err := c.get(ctx, "/api/v5/public/open-interest", q, &rows); err != nil {
		return 0, time.Time{}, err
	}
	if len(rows) == 0 {
		return 0, time.Time{}, fmt.Errorf("OKX open-interest: empty data")
	}
	v, err := strconv.ParseFloat(rows[0].OICcy, 64)
	if err != nil || v <= 0 {
		return 0, time.Time{}, fmt.Errorf("OKX open-interest: bad oiCcy %q", rows[0].OICcy)
	}
	ts, err := parseMs(rows[0].TS)
	if err != nil {
		return 0, time.Time{}, err
	}
	return v, ts, nil
}

// GetOpenInterestHistory returns daily open interest in BTC within [start, end]
// from the rubik 1D series (rows are [ts, oi, oiCcy, oiUsd], newest first;
// bars open at 16:00 UTC = 00:00 UTC+8). The in-progress bar keeps tracking
// live OI, so a bar's value is its end-of-day level: it is stamped at bar close
// (open + 24h) and only closed bars are returned.
func (c *OKXCollector) GetOpenInterestHistory(ctx context.Context, start, end time.Time) ([]time.Time, []float64, error) {
	now := time.Now()
	if c.now != nil {
		now = c.now()
	}
	var times []time.Time
	var values []float64
	cursor := end.UnixMilli()
	for page := 0; page < okxMaxPages; page++ {
		var rows [][]string
		q := url.Values{
			"instId": {okxInstID}, "period": {"1D"},
			"begin": {strconv.FormatInt(start.Add(-24*time.Hour).UnixMilli(), 10)},
			"end":   {strconv.FormatInt(cursor, 10)},
			"limit": {strconv.Itoa(okxPageLimit)},
		}
		if err := c.get(ctx, "/api/v5/rubik/stat/contracts/open-interest-history", q, &rows); err != nil {
			if len(times) > 0 {
				reverseSeries(times, values)
				return times, values, fmt.Errorf("OKX OI history partial: %w", err)
			}
			return nil, nil, err
		}
		oldest := cursor
		for _, r := range rows {
			if len(r) < 3 {
				continue
			}
			open, err := parseMs(r[0])
			if err != nil {
				continue
			}
			if open.UnixMilli() < oldest {
				oldest = open.UnixMilli()
			}
			ts := open.Add(24 * time.Hour)
			if ts.After(now) || ts.Before(start) || ts.After(end) {
				continue
			}
			v, err := strconv.ParseFloat(r[2], 64)
			if err != nil || v <= 0 {
				continue
			}
			times = append(times, ts)
			values = append(values, v)
		}
		if len(rows) < okxPageLimit || oldest <= start.Add(-24*time.Hour).UnixMilli() || oldest >= cursor {
			break
		}
		cursor = oldest - 1
	}
	reverseSeries(times, values)
	return times, values, nil
}

// GetLatestFundingRate returns the most recently settled funding rate in % per
// settlement period and its settlement time. The live endpoint's fundingRate
// is the not-yet-settled current period, so the settled value is used instead.
func (c *OKXCollector) GetLatestFundingRate(ctx context.Context) (float64, time.Time, error) {
	var rows []struct {
		SettFundingRate string `json:"settFundingRate"`
		SettState       string `json:"settState"`
		PrevFundingTime string `json:"prevFundingTime"`
		FundingTime     string `json:"fundingTime"`
	}
	if err := c.get(ctx, "/api/v5/public/funding-rate", url.Values{"instId": {okxInstID}}, &rows); err != nil {
		return 0, time.Time{}, err
	}
	if len(rows) == 1 && rows[0].SettState == "settled" {
		r := rows[0]
		rate, errR := strconv.ParseFloat(r.SettFundingRate, 64)
		prev, errP := parseMs(r.PrevFundingTime)
		next, errN := parseMs(r.FundingTime)
		if errR == nil && errP == nil {
			if errN == nil && next.Sub(prev) != okxFundingGap {
				log.Warn().Dur("interval", next.Sub(prev)).Msg("OKX BTC-USDT-SWAP funding interval is no longer 8h; okx_funding_rate is per-period")
			}
			return rate * 100, prev, nil
		}
	}
	// Settlement in progress or unexpected shape: fall back to the last history row.
	times, values, err := c.fundingPage(ctx, time.Now().Add(time.Minute), 1)
	if err != nil {
		return 0, time.Time{}, err
	}
	if len(values) == 0 {
		return 0, time.Time{}, fmt.Errorf("OKX funding-rate-history: empty data")
	}
	return values[0], times[0], nil
}

// fundingPage returns settled funding rows strictly before `before`, newest first, in %.
func (c *OKXCollector) fundingPage(ctx context.Context, before time.Time, limit int) ([]time.Time, []float64, error) {
	var rows []struct {
		FundingRate string `json:"fundingRate"`
		FundingTime string `json:"fundingTime"`
	}
	q := url.Values{
		"instId": {okxInstID},
		"after":  {strconv.FormatInt(before.UnixMilli(), 10)},
		"limit":  {strconv.Itoa(limit)},
	}
	if err := c.get(ctx, "/api/v5/public/funding-rate-history", q, &rows); err != nil {
		return nil, nil, err
	}
	times := make([]time.Time, 0, len(rows))
	values := make([]float64, 0, len(rows))
	for _, r := range rows {
		ts, err := parseMs(r.FundingTime)
		if err != nil {
			continue
		}
		v, err := strconv.ParseFloat(r.FundingRate, 64)
		if err != nil {
			continue
		}
		times = append(times, ts)
		values = append(values, v*100)
	}
	return times, values, nil
}

// GetFundingRateHistory returns settled funding rates (%) within [start, end].
// OKX only serves roughly the last three months of funding history.
func (c *OKXCollector) GetFundingRateHistory(ctx context.Context, start, end time.Time) ([]time.Time, []float64, error) {
	var times []time.Time
	var values []float64
	cursor := end.Add(time.Millisecond)
	for page := 0; page < okxMaxPages; page++ {
		pt, pv, err := c.fundingPage(ctx, cursor, okxPageLimit)
		if err != nil {
			if len(times) > 0 {
				reverseSeries(times, values)
				return times, values, fmt.Errorf("OKX funding history partial: %w", err)
			}
			return nil, nil, err
		}
		if len(pt) == 0 {
			break
		}
		oldest := cursor
		for i, ts := range pt {
			if ts.Before(oldest) {
				oldest = ts
			}
			if ts.Before(start) || ts.After(end) {
				continue
			}
			times = append(times, ts)
			values = append(values, pv[i])
		}
		if len(pt) < okxPageLimit || !oldest.After(start) || !oldest.Before(cursor) {
			break
		}
		cursor = oldest
	}
	reverseSeries(times, values)
	return times, values, nil
}

// reverseSeries turns newest-first slices into ascending order, in place.
func reverseSeries(times []time.Time, values []float64) {
	for i, j := 0, len(times)-1; i < j; i, j = i+1, j-1 {
		times[i], times[j] = times[j], times[i]
		values[i], values[j] = values[j], values[i]
	}
}
