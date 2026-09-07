// Package collector 提供 crypto 插件的数据采集器：CoinGecko / mempool.space /
// blockchain.com 真实源与离线 mock。使用 SafeHTTPClient 进行 API 保护。
package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/rs/zerolog/log"

	"sonde/pkg/model"
	"sonde/pkg/pluginrunner"
	"sonde/pkg/provider"
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

// historicalWindowThreshold is the span beyond which GetSnapshotsForWindow uses the
// CoinGecko market_chart/range historical path instead of the real-time /simple/price.
const historicalWindowThreshold = 90 * 24 * time.Hour

// maxHistoricalWindow is the safe per-request span for a single market_chart/range call.
// The free CoinGecko API can return up to ~365 days per request; we conservatively
// chunk to avoid being rate-limited.
const maxHistoricalWindow = 365 * 24 * time.Hour

// interWindowDelay is the pause between consecutive historical window requests
// to stay within CoinGecko's free-tier rate limit.
const interWindowDelay = 1500 * time.Millisecond

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

// marketChartRangeResponse is the CoinGecko /coins/{id}/market_chart/range payload.
// We only unpack "prices"; market_caps and total_volumes are ignored for now.
type marketChartRangeResponse struct {
	Prices [][2]float64 `json:"prices"` // [][timestamp_ms, price]
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

// GetSnapshotsForWindow serves the backfill for price, hash-rate, and tx_count.
//
// When the requested range exceeds historicalWindowThreshold (90 days), the CoinGecko
// price branch automatically switches to the market_chart/range endpoint with
// window-splitting so that BTC price history is fully backfilled across multi-year spans.
//
// The function signature is preserved for compatibility: returns ([]Snapshot, error).
func (r *RealCollector) GetSnapshotsForWindow(ctx context.Context, start, end time.Time) ([]pluginrunner.Snapshot, error) {
	if end.Before(start) {
		end = start
	}
	snaps := make([]pluginrunner.Snapshot, 0, 90)

	// Historical BTC price when the window exceeds 90 days
	if end.Sub(start) > historicalWindowThreshold {
		priceSnaps, err := r.fetchHistoricalPrices(ctx, "bitcoin", start, end)
		if err != nil {
			log.Warn().Err(err).Str("metric", "btc.ass.price").
				Str("window_start", start.Format("2006-01-02")).
				Str("window_end", end.Format("2006-01-02")).
				Msg("CoinGecko historical price backfill failed")
		} else {
			log.Info().Str("metric", "btc.ass.price").
				Int("samples", len(priceSnaps)).
				Str("window_start", start.Format("2006-01-02")).
				Str("window_end", end.Format("2006-01-02")).
				Msg("CoinGecko historical price backfill complete")
			// Backfill gap detection
			detectGaps("btc.ass.price", priceSnaps, start, end, 48*time.Hour)
			snaps = append(snaps, priceSnaps...)
		}
	}

	// Hash rate history from mempool.space
	hashTimes, hashValues, err := r.fetchHashRateHistory(ctx, start, end)
	if err != nil {
		log.Warn().Err(err).Msg("mempool hashrate history fetch failed")
	} else {
		// Build gap-detection-enabled snapshot slice
		hashSnaps := make([]pluginrunner.Snapshot, 0, len(hashTimes))
		fetchedAt := time.Now()
		for i, ts := range hashTimes {
			if hashValues[i] == 0 {
				continue
			}
			hashSnaps = append(hashSnaps, pluginrunner.Snapshot{
				MetricID:    "btc.ass.hash_rate",
				Value:       hashValues[i],
				Timestamp:   ts,
				FetchedAt:   fetchedAt,
				Provider:    providerMempool,
				SourceClass: model.SourceClassReal,
				Grade:       "delayed",
			})
		}
		detectGaps("btc.ass.hash_rate", hashSnaps, start, end, 12*time.Hour)
		snaps = append(snaps, hashSnaps...)
	}

	// Transaction count history from blockchain.com
	txTimes, txValues, err := r.blockchainInfo.GetTransactionHistory(ctx, start, end)
	if err != nil {
		log.Warn().Err(err).Msg("blockchain.com tx_count history failed")
	} else {
		txSnaps := make([]pluginrunner.Snapshot, 0, len(txTimes))
		fetchedAt := time.Now()
		for i, ts := range txTimes {
			if txValues[i] == 0 {
				continue
			}
			txSnaps = append(txSnaps, pluginrunner.Snapshot{
				MetricID:    "btc.ass.tx_count",
				Value:       txValues[i],
				Timestamp:   ts,
				FetchedAt:   fetchedAt,
				Provider:    providerBlockchainInfo,
				SourceClass: model.SourceClassReal,
				Grade:       "delayed",
			})
		}
		detectGaps("btc.ass.tx_count", txSnaps, start, end, 48*time.Hour)
		snaps = append(snaps, txSnaps...)
	}

	if len(snaps) == 0 {
		return nil, fmt.Errorf("all crypto history sources failed for window [%s, %s]",
			start.Format("2006-01-02"), end.Format("2006-01-02"))
	}
	return snaps, nil
}

// fetchHistoricalPrices gracefully paginates through [from, to] using the CoinGecko
// /coins/{id}/market_chart/range endpoint, splitting the range into maxHistoricalWindow
// chunks. Between windows it yields to context cancellation and applies a rate-limit delay.
func (r *RealCollector) fetchHistoricalPrices(ctx context.Context, coinID string, from, to time.Time) ([]pluginrunner.Snapshot, error) {
	var all []pluginrunner.Snapshot
	cur := from

	for cur.Before(to) {
		end := cur.Add(maxHistoricalWindow)
		if end.After(to) {
			end = to
		}

		snaps, err := r.fetchSingleWindow(ctx, coinID, cur, end)
		if err != nil {
			return all, fmt.Errorf("window %s..%s: %w", cur.Format("2006-01-02"), end.Format("2006-01-02"), err)
		}
		all = append(snaps, snaps...)
		cur = end

		// Rate-limit-friendly delay — respects ctx cancellation
		if cur.Before(to) {
			timer := time.NewTimer(interWindowDelay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return all, ctx.Err()
			case <-timer.C:
			}
		}
	}

	// Deduplicate by timestamp (ascending, sorted by time from API)
	all = dedupeSnapshots(all)
	return all, nil
}

// fetchSingleWindow calls the CoinGecko market_chart/range endpoint for a single time chunk.
func (r *RealCollector) fetchSingleWindow(ctx context.Context, coinID string, start, end time.Time) ([]pluginrunner.Snapshot, error) {
	// market_chart/range uses Unix seconds
	url := fmt.Sprintf(
		"https://api.coingecko.com/api/v3/coins/%s/market_chart/range?vs_currency=usd&from=%d&to=%d",
		coinID, start.Unix(), end.Unix(),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := r.coingeckoClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := provider.ReadAll(resp, 512)
		return nil, fmt.Errorf("coingecko market_chart/range returned %d: %s", resp.StatusCode, string(body))
	}

	body, err := provider.ReadAll(resp, 10<<20) // 10 MiB for potentially large payloads
	if err != nil {
		return nil, err
	}

	var mr marketChartRangeResponse
	if err := json.Unmarshal(body, &mr); err != nil {
		return nil, fmt.Errorf("decode coingecko market_chart/range: %w", err)
	}

	fetchedAt := time.Now()
	snaps := make([]pluginrunner.Snapshot, 0, len(mr.Prices))
	for _, p := range mr.Prices {
		if len(p) < 2 {
			continue
		}
		price := p[1]
		if price == 0 {
			continue
		}
		ts := time.Unix(int64(p[0])/1000, 0) // ms -> s
		snaps = append(snaps, pluginrunner.Snapshot{
			MetricID:    "btc.ass.price",
			Value:       price,
			Timestamp:   ts,
			FetchedAt:   fetchedAt,
			Provider:    providerCoinGecko,
			SourceClass: model.SourceClassReal,
			Grade:       "delayed",
		})
	}
	return snaps, nil
}

// dedupeSnapshots removes duplicate timestamps (keeps earliest) and returns them sorted ascending.
func dedupeSnapshots(snaps []pluginrunner.Snapshot) []pluginrunner.Snapshot {
	if len(snaps) == 0 {
		return snaps
	}
	// Sort by timestamp (already mostly sorted from API)
	sortSnapshotsByTime(snaps)

	seen := make(map[int64]struct{}, len(snaps))
	out := make([]pluginrunner.Snapshot, 0, len(snaps))
	for _, s := range snaps {
		key := s.Timestamp.Unix()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, s)
	}
	return out
}

// sortSnapshotsByTime sorts snapshots ascending by Timestamp using insertion sort (small N) or built-in sort.
func sortSnapshotsByTime(snaps []pluginrunner.Snapshot) {
	// Insertion sort is fine for small N, but use built-in for robustness
	sort.Slice(snaps, func(i, j int) bool {
		return snaps[i].Timestamp.Before(snaps[j].Timestamp)
	})
}

// detectGaps checks the snapshot sequence for timeline gaps exceeding maxGap
// and logs a warning for each gap found. This supports backfill quality monitoring.
func detectGaps(metricID string, snaps []pluginrunner.Snapshot, reqStart, reqEnd time.Time, maxGap time.Duration) {
	if len(snaps) == 0 {
		log.Warn().Str("metric", metricID).
			Str("requested_start", reqStart.Format("2006-01-02")).
			Str("requested_end", reqEnd.Format("2006-01-02")).
			Msg("backfill returned zero samples")
		return
	}

	// Sort ascending
	sortSnapshotsByTime(snaps)

	// Check coverage of requested range
	coverageStart := snaps[0].Timestamp
	coverageEnd := snaps[len(snaps)-1].Timestamp

	if coverageStart.After(reqStart.Add(maxGap)) {
		log.Warn().Str("metric", metricID).
			Str("requested_start", reqStart.Format("2006-01-02")).
			Str("actual_start", coverageStart.Format("2006-01-02")).
			Msg("backfill misses early data at window start")
	}
	if coverageEnd.Before(reqEnd.Add(-maxGap)) {
		log.Warn().Str("metric", metricID).
			Str("requested_end", reqEnd.Format("2006-01-02")).
			Str("actual_end", coverageEnd.Format("2006-01-02")).
			Msg("backfill misses recent data at window end")
	}

	// Check internal gaps
	for i := 1; i < len(snaps); i++ {
		gap := snaps[i].Timestamp.Sub(snaps[i-1].Timestamp)
		if gap > maxGap {
			log.Warn().Str("metric", metricID).
				Str("gap_start", snaps[i-1].Timestamp.Format("2006-01-02")).
				Str("gap_end", snaps[i].Timestamp.Format("2006-01-02")).
				Dur("gap_duration", gap).
				Int("total_samples", len(snaps)).
				Msg("backfill timeline gap detected")
		}
	}
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
