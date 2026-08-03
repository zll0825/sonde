# Real-data Onboarding Guide

After code compiles (`go build ./...` from root + each plugin dir), the next step
is to point the system at **real upstream sources** and let it soak for 1-2 weeks
so the noise budget calibration reflects *actual* false-positive rates.

## Prerequisites

1. **FRED API key** (free, required for macro plugin):
   - Register at https://fred.stlouisfed.org
   - Request an API key — it arrives instantly.
   - Never hardcode it. Export as `export FRED_API_KEY=...` or define in a
     `.env` file picked up by docker-compose.

2. **Telegram bot** (optional but recommended):
   - Export TELEGRAM_BOT_TOKEN + TELEGRAM_CHAT_ID in the **core** service env
     to receive real alerts on your phone. Without this, alerts still land in
     `alerts` table; they just aren't pushed to a notification channel.

3. No key needed for ETF (Yahoo Finance) or Crypto (CoinGecko + mempool.space).

## Setup Steps

### 1. Clean old mock data (destructive)

```bash
make clean-data
```

This drops the database volume. Old mock-style observations, orphan rules, and
alerts are gone; they'd otherwise contaminate the real-data noise-budget signal.

### 2. Start the full stack

```bash
make dev-up
```

Compose starts: timescaledb -> core -> api -> etf-plugin -> macro-plugin -> crypto-plugin.

The macro plugin **fails fast** if `FRED_API_KEY` is not provided — this is
intentional. It's louder and more correct than silently emitting mock data under
the `fred` brand.

### 3. Verify the pipes

```bash
make dev-logs        # watch plugin connect + initial poll
```

Expected logs:
- `macro connected`, `FRED fetch: WALCL=...`, `[others]`
- `crypto connected`, `CoinGecko price=...`, `mempool hash rate=...`
- `ETF connected`, `Yahoo price=...`

### 4. Wait & observe (calendar time — 1-2 weeks)

After ~48h:
```sql
SELECT metric_id, COUNT(*) FROM observations
  WHERE ingested_at > NOW() - INTERVAL '48h'
  GROUP BY metric_id;
-- Expects: 4 macro metrics x 1/day + 1 ETF metric + 2-3 crypto metrics
```

After 1 week:
```sql
SELECT * FROM alerts;
-- Empty is best: real sources aren't spiking under normal conditions.
-- If populated, that's signal you can interrogate for tuning.
```

After 2 weeks against the noise budget:
```sql
SELECT severity, COUNT(*) FROM alerts
  WHERE triggered_at > NOW() - INTERVAL '14d'
  GROUP BY severity;
-- Compare to <=10/day budget. If exceeded, interrogate offenders:
SELECT title, COUNT(*) FROM alerts
  WHERE triggered_at > NOW() - INTERVAL '14d'
  GROUP BY title ORDER BY COUNT(*) DESC LIMIT 5;
```

### 5. Tune if needed

If a rule fires too often, raise its threshold by editing the rule config for
that plugin in `plugins/<name>/cmd/<name>/main.go` -> Rules array. Rebuild/redeploy.

Current thresholds explicitly **loose** to catch early signal — tighten after
you've seen 1 week of real data.

## Useful Tips

- **Restart without losing history**: `docker compose restart <service>` does
  not delete volumes; only `make clean-data` does.
- **Mock fallback for dev work**: run any plugin with `PROVIDER=mock` to skip the
  real network. Recompile only when you've changed collector code.
- **CoinGecko rate-limits** at ~10-50 req/min (free). The 1-hour cadence stays
  far below that. Don't lower `COLLECTION_INTERVAL` without reason.
- **FRED rate-limits** at 120 req/min. You'll need at most 4 series x 1 poll
  per hour = 4 req/h — plenty of headroom.
