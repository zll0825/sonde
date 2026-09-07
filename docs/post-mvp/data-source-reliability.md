# Data Source Reliability & Freshness

This document records the verified freshness characteristics and reliability
patterns for each data source in the sonde production inventory.

## Freshness Characteristics

> **2026-09-07 起已过时。** `gld.ass.flow_proxy`、`btc.ass.flow_proxy`、
> `metal.precious.gold` 三个指标已退役（分别与 `gld.ass.volume`、
> `btc.ass.tx_count` 同源，以及与 `gld.ass.price` 重复跟踪黄金）。
> Alpha Vantage 随之无使用者。下表保留为当时的核查记录，**不是现行清单**；
> 现行申报面以四个插件的 catalog 为准。

| Provider | Metric IDs | Typical Lag | Update Frequency | Notes |
|----------|-----------|-------------|------------------|-------|
| yahoo_finance | gld.ass.price, gld.ass.volume | 15-20 min (delayed) | Real-time (market hours) | Free delayed feed; volume reflects daily cumulative |
| coingecko | btc.ass.price | 1-2 min | ~30-60 sec | Free tier refreshes every 30-60 sec |
| mempool_space | btc.ass.hash_rate | 10-30 min | Per-block (~10 min) | Hash rate is estimated; lags block discovery |
| blockchain_com | btc.ass.tx_count | 1-3 hours | Per-block | Daily aggregates available after block confirmation |
| fred | fed.ins.balance_sheet, us.mkt.ten_year_yield, us.mkt.dollar_index, us.mkt.usd_cny, us.mkt.cpi, us.mkt.inflation_yoy, oil.energy.wti, metal.industrial.copper | 1-7 days | Weekly (Fed), Daily (yields/WTI), Monthly (CPI/copper) | See FRED series-specific release calendar |
| alpha_vantage | metal.precious.gold | Live quote timestamp; daily history closes | Spot polled every 2h; history explicit only | `GOLD_SILVER_SPOT` / `GOLD_SILVER_HISTORY`, `nominal=XAUUSD`, USD per troy ounce; free tier 25 calls/day |

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
- **Alpha Vantage XAUUSD**: Spot follows the global precious-metals market; daily history can omit non-trading dates

## Alpha Vantage Gold Contract

- `metal.precious.gold` remains physical/spot gold, not COMEX futures or an ETF proxy.
- Current collection requires `GOLD_SILVER_SPOT&symbol=GOLD` with `nominal=XAUUSD`; history uses `GOLD_SILVER_HISTORY`, `symbol=GOLD`, `interval=daily`.
- Spot timestamps are normalized from provider UTC; daily history dates are UTC midnight. Values must be finite and positive.
- The default two-hour interval schedules at most 12 spot calls/day. Retries, reconnects, operator checks, and explicit history calls consume the remaining 13-call free-tier headroom.
- A shared limiter hard-caps Alpha Vantage at 25 wire attempts per rolling 24 hours. `PROVIDER_QUOTA_DB_URL` makes the cap persistent across process restarts; without it, the cap remains process-local across session reconnects.
- Missing credentials, informational/rate-limit JSON, stale spot observations, invalid instruments/values, HTTP failures, oversized responses, and empty requested history windows remain visible as collection failures. Real mode never falls back to mock.

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
