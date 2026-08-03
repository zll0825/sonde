// Package collector 提供 crypto 插件的数据采集器：CoinGecko / mempool.space
// 真实源与离线 mock。
package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"

	"capital_observatory/pkg/pluginrunner"
)

// source labels — distinct from mock so downstream can filter by trust boundary.
const (
	providerCoinGecko = "coingecko"
	providerMempool   = "mempool_space"
)

// priceURLCoinGecko is the /simple/price endpoint (no key needed, free tier
// tolerates small polling cadences ~1 req / few seconds per IP).
const priceURLCoinGecko = "https://api.coingecko.com/api/v3/simple/price?ids=bitcoin&vs_currencies=usd"

// mempoolHashrateURL returns the latest network hash rate in H/s (difficulty
// adjustment projection + latest block timing). The /3d projection includes a
// rolling 2016-block estimate in its latest window.
const mempoolHashrateURL = "https://mempool.space/api/v1/mining/hashrate/3d"

// RealCollector pulls price and hash-rate from free public sources and
// synthesizes exchange-balance from Mock (no free Glassnode / CryptoQuant
// tier implements this series; the rule on it degrades gracefully — see
// plugins/crypto/cmd/crypto/main.go rule config).
type RealCollector struct {
	client *http.Client
}

// NewRealCollector creates a crypto collector that uses real public sources.
// No API key is required for the free endpoints.
func NewRealCollector() *RealCollector {
	return &RealCollector{client: &http.Client{Timeout: 10 * time.Second}}
}

// priceResponseCoinGecko is the subset of /simple/price we care about.
type priceResponseCoinGecko struct {
	Bitcoin struct {
		USD float64 `json:"usd"`
	} `json:"bitcoin"`
}

// mempoolHashrateResponse carries currentHashrate (hashes/sec).
type mempoolHashrateResponse struct {
	CurrentHashrate float64 `json:"currentHashrate"`
}

// GetSnapshots fetches real price + hash rate and backfills exchange-balance
// from Mock. A failed real fetch for one metric does not abort the other —
// it is reflected in the returned Snapshot (omitted) so the caller sees a
// partial but coherent view.
func (r *RealCollector) GetSnapshots(ctx context.Context) ([]pluginrunner.Snapshot, error) {
	now := time.Now()
	snaps := make([]pluginrunner.Snapshot, 0, 3)

	// Real price
	if price, err := r.fetchPrice(ctx); err != nil {
		log.Warn().Err(err).Msg("CoinGecko price fetch failed; dropping btc.ass.price")
	} else {
		snaps = append(snaps, pluginrunner.Snapshot{
			MetricID:  "btc.ass.price",
			Value:     price,
			Timestamp: now,
			Provider:  providerCoinGecko,
			Grade:     "delayed", // CoinGecko is ~1m delayed under normal load
		})
	}

	// Real hash rate (reported in EH/s; convert H/s -> EH/s)
	if hr, err := r.fetchHashRate(ctx); err != nil {
		log.Warn().Err(err).Msg("mempool.space hash rate fetch failed; dropping btc.ass.hash_rate")
	} else {
		snaps = append(snaps, pluginrunner.Snapshot{
			MetricID:  "btc.ass.hash_rate",
			Value:     hr,
			Timestamp: now,
			Provider:  providerMempool,
			Grade:     "delayed",
		})
	}

	// Synthetic exchange balance (no free public source exists)
	mockSnaps, merr := Mock{}.GetSnapshots(ctx)
	if merr == nil {
		for _, s := range mockSnaps {
			if s.MetricID == "btc.ass.exchange_balance" {
				snaps = append(snaps, s)
				break
			}
		}
	} else {
		log.Warn().Err(merr).Msg("mock exchange_balance fallback failed")
	}

	if len(snaps) == 0 {
		return nil, fmt.Errorf("all crypto sources (CoinGecko, mempool, mock-fallback) failed")
	}
	return snaps, nil
}

// GetSnapshotsForWindow serves backfill for exchange-balance ONLY. Price and
// hash-rate have real live sources, so fabricating their history from the Mock
// random walk (67k base vs. real spot) would poison the observations table:
// GetObservations has no provider/grade filter, and the percentile/trend
// detectors would then compare real values against fake baselines and fire
// bogus alerts. Exchange balance is mock in the live path too, so its mock
// history is at least self-consistent.
//
// Real history endpoints exist (CoinGecko /market_chart/range, mempool.space
// hashrate intervals) — wiring them is a follow-up; until then a backfill
// command leaves price/hash-rate history empty rather than fabricated.
func (r *RealCollector) GetSnapshotsForWindow(ctx context.Context, start, end time.Time) ([]pluginrunner.Snapshot, error) {
	mockSnaps, err := (Mock{}).GetSnapshotsForWindow(ctx, start, end)
	if err != nil {
		return nil, err
	}
	out := make([]pluginrunner.Snapshot, 0, len(mockSnaps)/3)
	for _, s := range mockSnaps {
		if s.MetricID == "btc.ass.exchange_balance" {
			out = append(out, s)
		}
	}
	return out, nil
}

// fetchPrice queries CoinGecko /simple/price for the current BTC/USD spot.
func (r *RealCollector) fetchPrice(ctx context.Context) (float64, error) {
	body, err := r.doGet(ctx, priceURLCoinGecko)
	if err != nil {
		return 0, err
	}
	var resp priceResponseCoinGecko
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, fmt.Errorf("decode coingecko response: %w", err)
	}
	if resp.Bitcoin.USD == 0 {
		return 0, fmt.Errorf("zero price in coingecko response")
	}
	return resp.Bitcoin.USD, nil
}

// fetchHashRate queries mempool.space for the current network hash rate in H/s
// and converts to EH/s (1 EH/s = 1e18 H/s). Mempool reports ~620 EH/s in 2025.
func (r *RealCollector) fetchHashRate(ctx context.Context) (float64, error) {
	body, err := r.doGet(ctx, mempoolHashrateURL)
	if err != nil {
		return 0, err
	}
	var resp mempoolHashrateResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, fmt.Errorf("decode mempool response: %w", err)
	}
	if resp.CurrentHashrate == 0 {
		return 0, fmt.Errorf("zero hashrate in mempool response")
	}
	return resp.CurrentHashrate / 1e18, nil
}

// doGet performs an HTTP GET and returns the response body, with a
// CoinGecko-compatible User-Agent.
func (r *RealCollector) doGet(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "capital-observatory/0.1.0")
	req.Header.Set("Accept", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http GET: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("crypto endpoint returned %d: %s", resp.StatusCode, string(body))
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}
