# Real-Data Release Checklist (Phase 3)

Phase 3 acceptance criteria for the domain-complete real-data coverage release.

## Metric Inventory (Final)

### ETF Plugin (2 metrics, all real)
| Metric ID | Unit | Frequency | Source | Provider |
|-----------|------|-----------|--------|----------|
| gld.ass.price | USD | hourly* | Yahoo Finance | yahoo_finance |
| gld.ass.volume | shares | hourly* | Yahoo Finance | yahoo_finance |

### Crypto Plugin (3 metrics, all real)
| Metric ID | Unit | Frequency | Source | Provider |
|-----------|------|-----------|--------|----------|
| btc.ass.price | USD | hourly* | CoinGecko | coingecko |
| btc.ass.hash_rate | EH/s | hourly* | mempool.space | mempool_space |
| btc.ass.tx_count | transactions | daily* | blockchain.com | blockchain_com |

### Macro Plugin (6 metrics, all real)
| Metric ID | Unit | Frequency | Source | Provider |
|-----------|------|-----------|--------|----------|
| fed.ins.balance_sheet | USD | weekly* | FRED WALCL | fred |
| us.mkt.ten_year_yield | % | daily* | FRED DGS10 | fred |
| us.mkt.dollar_index | index | daily* | FRED DTWEXBGS | fred |
| us.mkt.usd_cny | CNY/USD | daily* | FRED DEXCHUS | fred |
| us.mkt.cpi | index | monthly* | FRED CPIAUCSL | fred |
| us.mkt.inflation_yoy | % | monthly* | FRED CPIAUCSL_PCH | fred |

*\*Collection interval = `DefaultInterval` (1h for all). Actual data freshness depends on publisher cadence. Metrics continue to report even if source data is stale (lag measured by `fetched_at - timestamp`).*

## Dimension Coverage Matrix

| Dimension | ETF | Crypto | Macro |
|-----------|-----|--------|-------|
| Price | ✅ gld.ass.price | ✅ btc.ass.price | — |
| Size/Liquidity | ✅ gld.ass.volume (proxy) | — | — |
| Capital Flow | ❌ (retired: no free source) | — | — |
| Network Security | — | ✅ btc.ass.hash_rate | — |
| On-chain Activity | — | ✅ btc.ass.tx_count | — |
| Exchange Supply | — | ❌ (retired: no free source) | — |
| Liquidity | — | — | ✅ fed.ins.balance_sheet |
| Rates | — | — | ✅ us.mkt.ten_year_yield |
| Dollar/FX | — | — | ✅ us.mkt.dollar_index + us.mkt.usd_cny |
| Inflation | — | — | ✅ us.mkt.cpi + us.mkt.inflation_yoy |

**Coverage: 9/12 theoretical dimensions (3 retired due to no free trustworthy source).**

## Provider Safety Configuration

| Provider | RPS | Burst | Max Retries | Circuit Threshold |
|----------|-----|-------|-------------|-------------------|
| yahoo_finance | 0.2 | 3 | 3 | 5 |
| coingecko | 0.5 | 2 | 3 | 5 |
| mempool_space | 0.25 | 2 | 3 | 5 |
| blockchain_com | 0.2 | 2 | 3 | 5 |
| fred | 0.03 | 2 | 3 | 3 |

## Rules (Active)

| Rule | Metric | Detector | Severity |
|------|--------|----------|----------|
| btc_price_change | btc.ass.price | percentile(95, 1) | warning |
| btc_hashrate_drop | btc.ass.hash_rate | trend(down, 3) | warning |
| btc_tx_surge | btc.ass.tx_count | percentile(90, 1) | info |
| fed_balance_drop | fed.ins.balance_sheet | trend(down, 4) | info |
| yield_spike_percentile | us.mkt.ten_year_yield | percentile(90, 2) | warning |
| usd_index_extreme | us.mkt.dollar_index | threshold(>105) | info |
| inflation_above_target | us.mkt.inflation_yoy | threshold(>3%, consec=2) | warning |

## Pre-Release Verification

- [x] All retired synthetic metrics removed from production inventory
- [x] No orphaned rules or entity references
- [x] All collectors use SafeHTTPClient (rate limit + circuit breaker)
- [x] Mock collectors preserved for dev/CI fallback
- [x] Registration declarations match Go structs
- [x] Source IDs (idempotency keys) unchanged for retained metrics
- [x] FRED collector hard-fails on missing FRED_API_KEY (no silent mock)
- [ ] End-to-end pipeline test with real data (manual, see below)
- [ ] Soak qualification for sub-MVP stability gate (72h recommended)

## Known Limitations

1. **No Gold ETF flow data**: GLD daily creation/redemption data is not available from free sources. Volume serves as proxy.
2. **No ETH exposure**: ETH-P entity retired due to no free real source for any metric.
3. **No BTC exchange balance**: Exchange balance data is only available via paid Glassnode/CryptoQuant subscriptions.
4. **FRED publishing lag**: CPI and inflation metrics lag 2-4 weeks behind reference month.
5. **Dollar index**: DTWEXBGS vs DXY (ICE); the former is a Fed broad index, not the widely-quoted DXY spot.

## Post-Release Monitoring

- [ ] Collection success rate > 95% over first 7 days
- [ ] Circuit events < 1 per provider per week (adjust RPS if exceeded)
- [ ] Metric staleness alerts fire as designed
- [ ] Backfill window < 5 min for 90-day lookback
