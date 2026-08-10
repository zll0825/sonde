// Package collector 提供 crypto 插件的数据采集器：CoinGecko / mempool.space /
// blockchain.com 真实源与离线 mock。使用 SafeHTTPClient 进行 API 保护。
package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"

	"capital_observatory/pkg/model"
	"capital_observatory/pkg/pluginrunner"
	"capital_observatory/pkg/provider"
)

const (
	providerCoinGecko      = "coingecko"
	providerMempool        = "mempool_space"
	providerBlockchainInfo = "blockchain_com"
)

// priceURLCoinGecko is the /simple/price endpoint.
const priceURLCoinGecko = "https://api.coingecko.com/api/v3/simple/price?ids=bitcoin&vs_currencies=usd"

// mempoolHashrateURL returns the latest network hash rate in H/s.
const mempoolHashrateURL = "https://mempool.space/api/v1/mining/hashrate/3d"

// RealCollector pulls price, hash-rate, and on-chain activity from free public
// sources with rate limiting and circuit breaker protection.
type RealCollector struct {
	coingeckoClient *provider.SafeHTTPClient
	mempoolClient   *provider.SafeHTTPClient
	blockchainInfo  *BlockchainInfoCollector
}

// NewRealCollector creates a crypto collector with safe HTTP clients.
func NewRealCollector() *RealCollector {
	return &RealCollector{
		coingeckoClient: provider.NewSafeHTTPClient(provider.CoinGeckoConfig()),
		mempoolClient:   provider.NewSafeHTTPClient(provider.MempoolConfig()),
		blockchainInfo:  NewBlockchainInfoCollector(),
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

// mempoolHashrateHistoryResponse for historical hashrate data.
type mempoolHashrateHistoryResponse struct {
	Hashrates []struct {
		Timestamp   float64 `json:"timestamp"`
		AvgHashrate float64 `json:"avgHashrate"`
	} `json:"hashrates"`
}

// GetSnapshots fetches real price + hash rate + transaction count.
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
	if txCount, txTime, err := r.blockchainInfo.GetTransactionCount(ctx); err != nil {
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

// GetSnapshotsForWindow serves the backfill for hash-rate and tx_count.
func (r *RealCollector) GetSnapshotsForWindow(ctx context.Context, start, end time.Time) ([]pluginrunner.Snapshot, error) {
	if end.Before(start) {
		end = start
	}
	snaps := make([]pluginrunner.Snapshot, 0, 90)

	// Hash rate history from mempool.space
	hashTimes, hashValues, err := r.fetchHashRateHistory(ctx, start, end)
	if err != nil {
		log.Warn().Err(err).Msg("mempool hashrate history fetch failed")
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
	txTimes, txValues, err := r.blockchainInfo.GetTransactionHistory(ctx, start, end)
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
		return nil, fmt.Errorf("all crypto history sources failed for window [%s, %s]",
			start.Format("2006-01-02"), end.Format("2006-01-02"))
	}
	return snaps, nil
}

// fetchPrice queries CoinGecko /simple/price for the current BTC/USD spot.
func (r *RealCollector) fetchPrice(ctx context.Context) (float64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, priceURLCoinGecko, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := r.coingeckoClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := provider.ReadAll(resp, 512)
		return 0, fmt.Errorf("coingecko returned %d: %s", resp.StatusCode, string(body))
	}

	body, err := provider.ReadAll(resp, 1<<20)
	if err != nil {
		return 0, err
	}

	var p priceResponseCoinGecko
	if err := json.Unmarshal(body, &p); err != nil {
		return 0, fmt.Errorf("decode coingecko: %w", err)
	}
	if p.Bitcoin.USD == 0 {
		return 0, fmt.Errorf("zero price")
	}
	return p.Bitcoin.USD, nil
}

// fetchHashRate queries mempool.space for current network hash rate in EH/s.
func (r *RealCollector) fetchHashRate(ctx context.Context) (float64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mempoolHashrateURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := r.mempoolClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := provider.ReadAll(resp, 512)
		return 0, fmt.Errorf("mempool returned %d: %s", resp.StatusCode, string(body))
	}

	body, err := provider.ReadAll(resp, 1<<20)
	if err != nil {
		return 0, err
	}

	var h mempoolHashrateResponse
	if err := json.Unmarshal(body, &h); err != nil {
		return 0, fmt.Errorf("decode mempool: %w", err)
	}
	if h.CurrentHashrate == 0 {
		return 0, fmt.Errorf("zero hashrate")
	}
	return h.CurrentHashrate / 1e18, nil
}

// fetchHashRateHistory queries mempool.space for historical hashrate data.
func (r *RealCollector) fetchHashRateHistory(ctx context.Context, start, end time.Time) ([]time.Time, []float64, error) {
	url := fmt.Sprintf(
		"https://mempool.space/api/v1/mining/hashrate/1m?start=%s&end=%s",
		start.Format("2006-01-02"),
		end.Format("2006-01-02"),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := r.mempoolClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := provider.ReadAll(resp, 512)
		return nil, nil, fmt.Errorf("mempool history returned %d: %s", resp.StatusCode, string(body))
	}

	body, err := provider.ReadAll(resp, 1<<20)
	if err != nil {
		return nil, nil, err
	}

	var h mempoolHashrateHistoryResponse
	if err := json.Unmarshal(body, &h); err != nil {
		return nil, nil, fmt.Errorf("decode mempool history: %w", err)
	}

	times := make([]time.Time, 0, len(h.Hashrates))
	values := make([]float64, 0, len(h.Hashrates))
	for _, entry := range h.Hashrates {
		if entry.AvgHashrate == 0 {
			continue
		}
		times = append(times, time.Unix(int64(entry.Timestamp), 0))
		values = append(values, entry.AvgHashrate/1e18)
	}
	return times, values, nil
}
