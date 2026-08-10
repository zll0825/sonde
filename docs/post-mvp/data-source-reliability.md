# Data Source Reliability & Freshness

This document records the verified freshness characteristics and reliability
patterns for each data source in the capital_observatory production inventory.

## Freshness Characteristics

| Provider | Metric IDs | Typical Lag | Update Frequency | Notes |
|----------|-----------|-------------|------------------|-------|
| yahoo_finance | gld.ass.price, gld.ass.volume | 15-20 min (delayed) | Real-time (market hours) | Free delayed feed; volume reflects daily cumulative |
| coingecko | btc.ass.price | 1-2 min | ~30-60 sec | Free tier refreshes every 30-60 sec |
| mempool_space | btc.ass.hash_rate | 10-30 min | Per-block (~10 min) | Hash rate is estimated; lags block discovery |
| blockchain_com | btc.ass.tx_count | 1-3 hours | Per-block | Daily aggregates available after block confirmation |
| fred | fed.ins.balance_sheet, us.mkt.ten_year_yield, us.mkt.dollar_index, us.mkt.usd_cny, us.mkt.cpi, us.mkt.inflation_yoy | 1-7 days | Weekly (Fed), Daily (yields), Monthly (CPI) | See FRED series-specific release calendar |

## FRED Release Calendar

| FRED Series | Metric ID | Release Frequency | Typical Release Time | Notes |
|-------------|-----------|-------------------|---------------------|-------|
| WALCL | fed.ins.balance_sheet | Weekly (Thursday) | ~4:30 PM ET | Fed total assets; revised weekly |
| DGS10 | us.mkt.ten_year_yield | Daily | ~3:30 PM ET | Constant maturity; previous close |
| DTWEXBGS | us.mkt.dollar_index | Daily | ~3:30 PM ET | Nominal broad dollar index |
| DEXCHUS | us.mkt.usd_cny | Daily | ~3:30 PM ET | Official rate; may be stale on CN holidays |
| CPIAUCSL | us.mkt.cpi | Monthly | ~8:30 AM ET (2nd week) | Reference month = previous month |
| CPIAUCSL_PCH | us.mkt.inflation_yoy | Monthly | ~8:30 AM ET (2nd week) | Native FRED % change transformation |

## Seasonal/Holiday Schedules

- **Yahoo Finance**: Closed weekends + US market holidays (Thanksgiving, Christmas, New Year, etc.)
- **CoinGecko**: 24/7 (crypto markets never close)
- **mempool.space**: 24/7 (blockchain continues)
- **blockchain.com**: 24/7
- **FRED**: Follows US federal calendar; quarterly data follows BLS/FA calendar

## Quality Monitoring Recommendations

1. **Stale data detection**: Flag metrics older than 3x the expected update interval
2. **Heartbeats**: Each collector should report `last_successful_fetch_at` for monitoring
3. **Circuit breaker metrics**: Expose circuit state and failure count for alerting
4. **Backfill gap detection**: Check for missing bars on daily-ish cadence

## Alert Routing for Source Degradation

| Degradation | Trigger | Action |
|-------------|---------|--------|
| Stale data | metric_age > 7d | info: data source stale |
| Partial failure | X of Y sources down | warn: partial collection |
| Full failure | all sources down | error: complete outage |
| Circuit open | any provider circuit open | warn: provider circuit open |
| Repeated 429 | > 5 in 15 min |.warn: rate limit sustained |
