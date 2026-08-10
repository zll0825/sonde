# Coverage Research and Inventory

## Current Metrics Registry (10 metrics)

### ETF Plugin (3 metrics)
| Metric ID | Name | Source | Provider | Status | Research Dimension |
|-----------|------|--------|----------|--------|-------------------|
| gld.ass.price | GLD Price (USD) | Real | yahoo_finance | ✅ Real | Price |
| gld.ass.daily_flow | GLD Daily Flow (USD) | Mock | mock_etf | ⚠️ Synthetic | Capital Flow |
| eth.ass.daily_flow | ETH-P Daily Flow (USD) | Mock | mock_etf | ⚠️ Synthetic | Capital Flow |

### Crypto Plugin (3 metrics)
| Metric ID | Name | Source | Provider | Status | Research Dimension |
|-----------|------|--------|----------|--------|-------------------|
| btc.ass.price | BTC Price (USD) | Real | coingecko | ✅ Real | Price |
| btc.ass.hash_rate | BTC Network Hash Rate (EH/s) | Real | mempool_space | ✅ Real | Network Security |
| btc.ass.exchange_balance | BTC Exchange Balance | Mock | mock | ⚠️ Synthetic | Exchange Supply |

### Macro Plugin (4 metrics)
| Metric ID | Name | Source | Provider | Status | Research Dimension |
|-----------|------|--------|----------|--------|-------------------|
| fed.ins.balance_sheet | Fed Balance Sheet (USD) | Real | fred | ✅ Real | Liquidity |
| us.mkt.ten_year_yield | US 10Y Treasury Yield (%) | Real | fred | ✅ Real | Rates |
| us.mkt.dollar_index | US Dollar Index | Real | fred | ✅ Real | Dollar/FX |
| us.mkt.usd_cny | USD/CNY Exchange Rate | Real | fred | ✅ Real | Dollar/FX |

## Research Dimension Gap Analysis

### ETF Domain
Required dimensions per PRD: Price, Size/Liquidity, Capital Flow or meaningful proxy

| Dimension | Current Coverage | Gap | Action |
|-----------|-----------------|-----|--------|
| Price | ✅ gld.ass.price (Yahoo) | None | Retain |
| Size/Liquidity | ❌ None | Missing | Add: gld.ass.aum (Assets Under Management) |
| Capital Flow | ⚠️ gld.ass.daily_flow (mock) | No free source | Investigate + potential proxy or retire |

**Decision: GLD Daily Flow**
- **Free source investigation**: ETF flow data (daily creations/redemptions) is not available from free public APIs. Yahoo Finance only provides price/volume, not flow data.
- **Proxy option**: GLD daily trading volume from Yahoo Finance serves as a liquidity/interest proxy. It's not "flow" but indicates market activity around the ETF.
- **Decision**: Retire `gld.ass.daily_flow` (no meaningful proxy). Add `gld.ass.volume` as a new metric for liquidity/interest proxy via Yahoo Finance.

**Decision: ETH-P Daily Flow**
- **Free source investigation**: ETH ETF flow data is not available from free public APIs.
- **Decision**: Retire `eth.ass.daily_flow`. The ETH-P entity itself lacks any real metrics → retire the entity and its remaining metrics entirely.

**New ETF metrics to add:**
- `gld.ass.volume` - GLD daily trading volume (Yahoo Finance) - real source
- `gld.ass.aum` - GLD Assets Under Management (Yahoo Finance profile) - real source

### Crypto Domain
Required dimensions per PRD: Price, Network Security, On-chain Activity, Exchange Supply/Flow or meaningful proxy

| Dimension | Current Coverage | Gap | Action |
|-----------|-----------------|-----|--------|
| Price | ✅ btc.ass.price (CoinGecko) | None | Retain |
| Network Security | ✅ btc.ass.hash_rate (mempool.space) | None | Retain |
| On-chain Activity | ❌ None | Missing | Add: btc.ass.active_addresses or btc.ass.tx_volume |
| Exchange Supply | ⚠️ btc.ass.exchange_balance (mock) | No free source | Investigate + potential proxy or retire |

**Decision: BTC Exchange Balance**
- **Free source investigation**: Glassnode, CryptoQuant require paid subscriptions. CoinGecko does not provide exchange balance data. mempool.space provides some on-chain data but not aggregate exchange balances.
- **Proxy option**: Exchange net flow from CoinGecko exchange tickers (sum of top exchange 24h volume as activity proxy). However, this is traded volume, NOT balance. Semantic drift would be significant.
- **Better proxy**: BTC "market dominance" from CoinGecko (different semantic). Or use mempool.space's "hash rate" we already have for security dimension.
- **Decision**: Retire `btc.ass.exchange_balance` (no trustworthy public/free source). Rules dependent on it (`btc_exchange_drop`, `btc_outflow_trend`) must be retired or reassigned.

**New Crypto metrics to add:**
- `btc.ass.tx_count` - BTC daily transaction count (blockchain.com public API) - real source

### Macro Domain
Required dimensions per PRD: Liquidity, Rates, Dollar/FX, Inflation

| Dimension | Current Coverage | Gap | Action |
|-----------|-----------------|-----|--------|
| Liquidity | ✅ fed.ins.balance_sheet (FRED) | None | Retain |
| Rates | ✅ us.mkt.ten_year_yield (FRED) | None | Retain |
| Dollar/FX | ✅ us.mkt.dollar_index + us.mkt.usd_cny | None | Retain |
| Inflation | ❌ None | Missing | Add: us.mkt.cpi or us.mkt.inflation_rate |

**New Macro metrics to add:**
- `us.mkt.cpi` - Consumer Price Index (FRED series: CPIAUCSL) - real source
- `us.mkt.inflation_yoy` - YoY Inflation Rate (FRED series: CPIAUCSL_PCH) - real source (FRED-native percent change)

## Final Inventory Decision Matrix

### RETAIN (Continue using as-is)
| Metric ID | Provider | Notes |
|-----------|----------|-------|
| gld.ass.price | yahoo_finance | GLD spot price |
| btc.ass.price | coingecko | BTC spot price |
| btc.ass.hash_rate | mempool_space | Network hash rate |
| fed.ins.balance_sheet | fred | Fed total assets |
| us.mkt.ten_year_yield | fred | US 10Y Treasury yield |
| us.mkt.dollar_index | fred | DXY index |
| us.mkt.usd_cny | fred | USD/CNY rate |

### ADD (New real-source metrics)
| Metric ID | Name | Provider | Entity | Research Dimension | Source URL |
|-----------|------|----------|--------|-------------------|------------|
| gld.ass.volume | GLD Trading Volume | yahoo_finance | GLD | Size/Liquidity Proxy | v8/finance/chart |
| gld.ass.aum | GLD AUM | yahoo_finance | GLD | Size/Liquidity | v8/finance/chart (marketCap analog) |
| btc.ass.tx_count | BTC Transaction Count | blockchain.com | BTC | On-chain Activity | charts/myTransactionsPerDay?format=json |
| us.mkt.cpi | US CPI Index | fred | US | Inflation | FRED CPIAUCSL |
| us.mkt.inflation_yoy | US YoY Inflation | fred | US | Inflation Rate | FRED CPIAUCSL_PCH |

### RETIRE (Remove from production inventory)
| Metric ID | Reason | Dependent Rules |
|-----------|--------|-----------------|
| gld.ass.daily_flow | No free trustworthy source | gld_flow_spike (retire rule) |
| eth.ass.daily_flow | No free trustworthy source; ETH-P entity has no real metrics | N/A (retire entity too) |
| btc.ass.exchange_balance | No free trustworthy source; paid sources only | btc_exchange_drop, btc_outflow_trend (reassign or retire) |

### REPLACEMENT PROXIES (Not applicable - retiring instead)
No proxy metrics will be introduced in this phase. The retiring metrics are removed entirely per PRD requirement: "when no meaningful proxy exists, the synthetic metric is retired."

## API Provider Documentation

### Yahoo Finance (ETF Plugin)
- **Base URL**: `https://query1.finance.yahoo.com/v8/finance/chart/{symbol}`
- **Free tier**: No API key required
- **Rate limits**: Unofficially ~2000 requests/hour per IP; conservative 15min+ interval recommended for stable access
- **Terms**: Public chart API, not officially documented but stable since 2017
- **Historical availability**: Daily data back to 1962 (symbol-dependent)
- **User-Agent**: Must present browser-like UA to avoid blocking
- **Notes**: GLD volume and market cap / AUM available via the same chart API's `meta` field

### CoinGecko (Crypto Plugin)
- **Base URL**: `https://api.coingecko.com/api/v3/`
- **Free tier**: 10-30 calls/min (varies by load), no API key required
- **Terms**: Free for non-commercial use; attribution required
- **Historical availability**: `/coins/{id}/market_chart/range` returns up to 365 days (daily), unlimited range with 5-min granularity clamped to >90 day windows
- **Rate limits**: 429 with Retry-After header; backoff required
- **Notes**: No exchange balance or active addresses in free tier; only price/market_cap/volume/total_supply

### mempool.space (Crypto Plugin)
- **Base URL**: `https://mempool.space/api/v1/`
- **Free tier**: Unlimited (self-hostable), no key required
- **Data**: Hash rate, difficulty, fees, mempool state, block heights
- **Historical**: Hash rate history available via hashrate/3d or hashrate/1m endpoints
- **Recommendation**: Conservative polling at 15min+ intervals

### FRED (Macro Plugin - existing)
- **Base URL**: `https://api.stlouisfed.org/fred/series/observations`
- **Free tier**: Requires free API key, ~120 requests/hour
- **Data**: 800,000+ economic time series
- **Historical**: Varies by series; CPI back to 1913
- **Publishing lag**: 1-7 days for most series

### blockchain.com (Crypto Plugin - new)
- **Base URL**: `https://api.blockchain.info/charts/{chartName}?format=json`
- **Free tier**: No key required, conservative rate limiting
- **Data**: Transaction count, hash rate, block size, difficulty, etc.
- **Historical**: Full history available
- **Charts**: `n-transactions`, `avg-block-size`, `hash-rate`, etc.
- **Rate limits**: Unknown; conservative 30min+ polling recommended

## Implementation Sequence

1. **Phase 1.1**: Remove synthetic metrics (gld.ass.daily_flow, eth.ass.daily_flow, btc.ass.exchange_balance) and dependent rules
2. **Phase 1.2**: Add new real-source metrics to existing collectors
3. **Phase 1.3**: Implement provider safety (rate limiting, circuit breaker, retry with backoff)
4. **Phase 1.4**: Historical backfill for new metrics
5. **Phase 1.5**: Update registration proto to reflect new inventory
