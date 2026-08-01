package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
)

// YahooCollector fetches real ETF price data from Yahoo Finance's public chart API.
// No API key required — uses query1.finance.yahoo.com which is publicly accessible.
//
// ETF flow data (daily inflows/outflows) is NOT available from free public APIs,
// so those metrics fall back to MockCollector. The key MVP requirement —
// "one real metric flowing end-to-end from plugin to DB" — is satisfied by
// gld_price which uses real Yahoo Finance data.
type YahooCollector struct {
	client *http.Client
}

// NewYahooCollector creates a Yahoo Finance collector with a default HTTP client.
func NewYahooCollector() *YahooCollector {
	return &YahooCollector{
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// yahooChartResponse is the subset of the v8/finance/chart JSON we care about.
type yahooChartResponse struct {
	Chart struct {
		Result []struct {
			Meta struct {
				RegularMarketPrice float64 `json:"regularMarketPrice"`
				PreviousClose      float64 `json:"previousClose"`
				Symbol             string  `json:"symbol"`
			} `json:"meta"`
			Timestamp []int64 `json:"timestamp"`
		} `json:"result"`
		Error *struct {
			Code        string `json:"code"`
			Description string `json:"description"`
		} `json:"error"`
	} `json:"chart"`
}

// GetSnapshots returns a real observation for gld_price (Yahoo Finance GLD price)
// plus falls back to MockCollector for flow metrics we cannot source publicly.
//
// Error handling: if Yahoo Finance is unreachable, the real-metric error is
// logged AND the collector falls through to MockCollector so the pipeline keeps
// running (degraded mode — real GLD price is reported as synthetic).
func (y *YahooCollector) GetSnapshots(ctx context.Context) ([]Snapshot, error) {
	now := time.Now()

	// --- gld_price: real Yahoo Finance data --------------------------------
	price, err := y.fetchGLDPrice(ctx)
	if err != nil {
		// Graceful degradation: log + serve mock data for ALL metrics.
		log.Warn().Err(err).Msg("Yahoo Finance unavailable, falling back to mock ETF data")
		return Mock{}.GetSnapshots(ctx)
	}
	snapshots := []Snapshot{
		{
			MetricID:  "gld_price",
			Value:     price,
			Timestamp: now,
			Provider:  "yahoo_finance",
			Grade:     "delayed", // Yahoo data is ~15min delayed, treated as delayed not realtime
		},
	}

	// --- Flow metrics: no free public source.
	// Mock data — flagged via Provider="mock_etf" so downstream can distinguish
	// real from synthesized metrics.
	for _, s := range getMockSnapshots(ctx) {
		if s.MetricID != "gld_price" {
			snapshots = append(snapshots, s)
		}
	}

	return snapshots, nil
}

// getMockSnapshots returns mock ETF data (flow metrics). Errors are logged and
// an empty slice is returned so the real-metric data is still useful.
func getMockSnapshots(ctx context.Context) []Snapshot {
	mock := Mock{}
	snaps, err := mock.GetSnapshots(ctx)
	if err != nil {
		log.Warn().Err(err).Msg("mock ETF fallback failed")
		return nil
	}
	return snaps
}

// fetchGLDPrice queries Yahoo Finance for the current GLD price.
func (y *YahooCollector) fetchGLDPrice(ctx context.Context) (float64, error) {
	url := "https://query1.finance.yahoo.com/v8/finance/chart/GLD?interval=1m&range=1d"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	// Yahoo blocks non-browser User-Agent; set a realistic one.
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")

	resp, err := y.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("http GET: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return 0, fmt.Errorf("yahoo returned %d: %s", resp.StatusCode, string(body))
	}

	var yc yahooChartResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&yc); err != nil {
		return 0, fmt.Errorf("decode: %w", err)
	}
	if yc.Chart.Error != nil {
		return 0, fmt.Errorf("yahoo error: %s — %s", yc.Chart.Error.Code, yc.Chart.Error.Description)
	}
	if len(yc.Chart.Result) == 0 || yc.Chart.Result[0].Meta.RegularMarketPrice == 0 {
		return 0, fmt.Errorf("no data in yahoo response")
	}

	return yc.Chart.Result[0].Meta.RegularMarketPrice, nil
}
