# FRED replay fixtures for `TestE2E_MacroFREDReplay`

One file per series in FRED's own `series/observations` response format
(`<SERIES>.json`, or `<SERIES>.<units>.json` for a units transform).
`internal/e2e.FREDReplay` serves them in place of `api.stlouisfed.org`,
honouring `observation_start/end`, `sort_order`, `limit` and `offset`, and
shifts every date forward by whole weeks so the anchor date (Wednesday
2026-09-23) reads as the current week.

The values are shaped, not recorded: the test sandbox cannot reach FRED, and a
regression test needs a fixed outcome. Levels and cadence follow the real
series (business days for daily series with a Labor Day `"."` gap, Wednesdays
for H.4.1 series, first-of-month for CPI).

| Series | Metric | Shape | Rule outcome |
| --- | --- | --- | --- |
| WALCL | fed.ins.balance_sheet | flat, then five straight weekly declines | `fed_balance_drop` fires |
| DGS10 | us.mkt.ten_year_yield | ~4.20, spikes to 4.62 / 4.71 on the last two days | `yield_spike_percentile` silent (see test comment) |
| DTWEXBGS | us.mkt.dollar_index | ~120.5, last day 123.0 (> SMA20 + 1.5) | `usd_index_extreme` fires |
| CPIAUCSL (pc1) | us.mkt.inflation_yoy | 2.5–2.9, last two months 3.2 / 3.4 | `inflation_above_target` fires |
| CPIAUCSL | us.mkt.cpi | steady climb | no rule |
| DEXCHUS, IORB, EFFR, DFII10, T10Y3M, WRBWFRBL, NFCI | collect-only metrics | flat or gentle wiggle | no rule |

To change a scenario, edit the values and update the expected alert list in
`plugins/macro/e2e_test.go`.
