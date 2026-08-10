// Package collector 提供 crypto 插件的数据采集器：CoinGecko / mempool.space /
// blockchain.com 真实源与离线 mock。
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

// source labels — distinct from mock so downstream can filter by trust boundary.
const (
	providerCoinGecko      = "coingecko"
	providerMempool        = "mempool_space"
	providerBlockchainInfo = "blockchain_com"
)

// priceURLCoinGecko is the /simple/price endpoint (no key needed, free tier
// tolerates small polling cadences ~1 req / few seconds per IP).
const priceURLCoinGecko = "https://api.coingecko.com/api/v3/simple/price?ids=bitcoin&vs_currencies=usd"

// mempoolHashrateURL returns the latest network hash rate in H/s (difficulty
// adjustment projection + latest block timing). The /3d projection includes a
// rolling 2016-block estimate in its latest window.
const mempoolHashrateURL = "https://mempool.space/api/v1/mining/hashrate/3d"

// RealCollector pulls price, hash-rate, and on-chain activity from free public
// sources. Exchange balance (btc.ass.exchange_balance) has been retired — no
// trustworthy free source exists.
type RealCollector struct {
	client           *http.Client
	blockchainClient *BlockchainInfoCollector
}

// NewRealCollector creates a crypto collector that uses real public sources.
// No API keys are required for the free endpoints.
func NewRealCollector() *RealCollector {
	return &RealCollector{
		client:           &http.Client{Timeout: 10 * time.Second},
		blockchainClient: NewBlockchainInfoCollector(),
	}
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

// GetSnapshots fetches real price + hash rate + transaction count.
// A failed real fetch for one metric does not abort the others —
// it is reflected in the returned Snapshot (omitted) so the caller sees a
// partial but coherent view.
func (r *RealCollector) GetSnapshots(ctx context.Context) ([]pluginrunner.Snapshot, error) {
	snaps := make([]pluginrunner.Snapshot, 0, 3)

	// Real price from CoinGecko
	if price, err := r.fetchPrice(ctx); err != nil {
		log.Warn().Err(err).Msg("CoinGecko price fetch failed; dropping btc.ass.price")
	} else {
		fetchedAt := time.Now()
		snaps = append(snaps, pluginrunner.Snapshot{
			MetricID:    "btc.ass.price",
			Value:       price,
			Timestamp:   fetchedAt,
			FetchedAt:   fetchedAt,
			Provider:    providerCoinGecko,
			SourceClass: model.SourceClassReal,
			Grade:       "delayed",
		})
	}

	// Real hash rate from mempool.space
	if hr, err := r.fetchHashRate(ctx); err != nil {
		log.Warn().Err(err).Msg("mempool.space hash rate fetch failed; dropping btc.ass.hash_rate")
	} else {
		fetchedAt := time.Now()
		snaps = append(snaps, pluginrunner.Snapshot{
			MetricID:    "btc.ass.hash_rate",
			Value:       hr,
			Timestamp:   fetchedAt,
			FetchedAt:   fetchedAt,
			Provider:    providerMempool,
			SourceClass: model.SourceClassReal,
			Grade:       "delayed",
		})
	}

	// Real transaction count from blockchain.com
	if txCount, txTime, err := r.blockchainClient.GetTransactionCount(ctx); err != nil {
		log.Warn().Err(err).Msg("blockchain.com tx_count fetch failed; dropping btc.ass.tx_count")
	} else {
		fetchedAt := time.Now()
		snaps = append(snaps, pluginrunner.Snapshot{
			MetricID:    "btc.ass.tx_count",
			Value:       txCount,
			Timestamp:   txTime,
			FetchedAt:   fetchedAt,
			Provider:    providerBlockchainInfo,
			SourceClass: model.SourceClassReal,
			Grade:       "delayed",
		})
	}

	if len(snaps) == 0 {
		return nil, fmt.Errorf("all crypto sources (CoinGecko, mempool, blockchain.com) failed")
	}
	return snaps, nil
}

// GetSnapshotsForWindow serves the backfill for price, hash-rate, and tx_count.
// All history comes from their respective real sources.
func (r *RealCollector) GetSnapshotsForWindow(ctx context.Context, start, end time.Time) ([]pluginrunner.Snapshot, error) {
	if end.Before(start) {
		end = start
	}
	snaps := make([]pluginrunner.Snapshot, 0, 90)

	// Price history from CoinGecko (not implemented here yet — documented as follow-up)
	//
	// CoinGecko /coins/{id}/market_chart/range provides historical prices.
	// Implementation deferred to a follow-up commit.

	// Hash rate history from mempool.space
	hashTimes, hashValues, err := r.fetchHashRateHistory(ctx, start, end)
	if err != nil {
		log.Warn().Err(err).Msg("mempool.hashrate history fetch failed")
	} else {
		fetchedAt := time.Now()
		for i, ts := range hashTimes {
			if hashValues[i] == 0 {
				continue
			}
			snaps = append(snaps, pluginrunner.Snapshot{
				MetricID:    "btc.ass.hash_rate",
				Value:       hashValues[i],
				Timestamp:   ts,
				FetchedAt:   fetchedAt,
				Provider:    providerMempool,
				SourceClass: model.SourceClassReal,
				Grade:       "delayed",
			})
		}
	}

	// Transaction count history from blockchain.com
	txTimes, txValues, err := r.blockchainClient.GetTransactionHistory(ctx, start, end)
	if err != nil {
		log.Warn().Err(err).Msg("blockchain.com tx_count history failed")
	} else {
		fetchedAt := time.Now()
		for i, ts := range txTimes {
			if txValues[i] == 0 {
				continue
			}
			snaps = append(snaps, pluginrunner.Snapshot{
				MetricID:    "btc.ass.tx_count",
				Value:       txValues[i],
				Timestamp:   ts,
				FetchedAt:   fetchedAt,
				Provider:    providerBlockchainInfo,
				SourceClass: model.SourceClassReal,
				Grade:       "delayed",
			})
		}
	}

	if len(snaps) == 0 {
		return nil, fmt.Errorf("all crypto history sources failed for window [%s, %s]", start.Format("2006-01-02"), end.Format("2006-01-02"))
	}
	return snaps, nil
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
// and converts to EH/s (1 EH/s = 1e18 H/s).
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

// fetchHashRateHistory queries mempool.space hashrate endpoint for historical data.
func (r *RealCollector) fetchHashRateHistory(ctx context.Context, start, end time.Time) ([]time.Time, []float64, error) {
	url := fmt.Sprintf(
		"https://mempool.space/api/v1/mining/hashrate/%s?start=%s&end=%s",
		"1m",
		start.Format("2006-01-02"),
		end.Format("2006-01-02"),
	)
	body, err := r.doGet(ctx, url)
	if err != nil {
		return nil, nil, err
	}
	var resp struct {
		Hashrates []struct {
			Timestamp float64 `json:"timestamp"` // unix seconds
			AvgHashrate float64 `json:"avgHashrate"`
		} `json:"hashrates"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, nil, fmt.Errorf("decode mempool hashrate history: %w", err)
	}
	times := make([]time.Time, 0, len(resp.Hashrates))
	values := make([]float64, 0, len(resp.Hashrates))
	for _, h := range resp.Hashrates {
		if h.AvgHashrate == 0 {
			continue
		}
		times = append(times, time.Unix(int64(h.Timestamp), 0))
		values = append(values, h.AvgHashrate/1e18) // H/s to EH/s
	}
	return times, values, nil
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
