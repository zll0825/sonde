# Release Evidence — Post-MVP Product Contract

Operational verification checklist for the Post-MVP Product Contract candidate.
The command blocks assume the environment variables shown below and disposable
databases for destructive checks. Never point these commands at the live MVP
soak database unless the step is explicitly read-only.

## Environment

```bash
export DATABASE_URL="postgres://sonde:PGPASS@localhost:55433/sonde_candidate?sslmode=disable"
```

## 1. Database Migrations

### Verify version

```bash
migrate -path migrations -database "$DATABASE_URL" version
```
Expected: `011` with `dirty=false` after apply. Version `006` is the schema
boundary for the MVP `v0.1.0` rollback commit cited below; migrations 007-011
belong to the post-MVP candidate.

### Apply pending migrations (idempotent)

```bash
migrate -path migrations -database "$DATABASE_URL" up
```
Migration 009 creates `provider_quota`, `research_feedbacks`, `rule_audit_log`,
and `entity_representative_metric` plus indexes. Migration 010 adds the
`extension JSONB` column and GIN index to `manual_relations`.
Migration 011 adds `plugins.last_collect_duration_ms` for persisted collection
health evidence.

### Verify tables

```bash
psql "$DATABASE_URL" -c "
SELECT table_name FROM information_schema.tables
 WHERE table_schema='public' AND table_name IN (
   'provider_quota','research_feedbacks','rule_audit_log',
   'entity_representative_metric','manual_relations')
 ORDER BY table_name;
"
```

### Verify migration 010 column

```bash
psql "$DATABASE_URL" -c "
SELECT column_name, data_type, is_nullable, column_default
  FROM information_schema.columns
 WHERE table_name='manual_relations' AND column_name='extension';
"
```
Expected: `extension | jsonb | NO | '{}'::jsonb`

## 2. Backup / Restore

### Full logical backup

```bash
BACKUP_DIR="${BACKUP_DIR:-/var/backups/sonde}"
mkdir -p "$BACKUP_DIR"
TS=$(date +%Y%m%d_%H%M%S)
pg_dump -Fc --no-owner --no-privileges \
  -f "$BACKUP_DIR/release_${TS}.dump" "$DATABASE_URL"
```

### Integrity check

```bash
pg_restore --list "$BACKUP_DIR/release_${TS}.dump" > /dev/null \
  && echo "BACKUP_OK" || echo "BACKUP_CORRUPT"
```

### Restore to staging and verify

```bash
STAGING_URL="postgres://sonde:PGPASS@localhost:55434/sonde_restore?sslmode=disable"
# Start a disposable second Postgres/TimescaleDB instance on port 55434.
# Do not use dropdb/createdb against the live MVP port 5432.
dropdb --if-exists --maintenance-db=postgres -h localhost -p 55434 -U sonde sonde_restore
createdb  -h localhost -p 55434 -U sonde sonde_restore
pg_restore --no-owner --no-privileges --dbname "$STAGING_URL" \
  "$BACKUP_DIR/release_${TS}.dump"
psql "$STAGING_URL" -c "
SELECT (SELECT count(*) FROM provider_quota)        AS quota_rows,
       (SELECT count(*) FROM research_feedbacks)     AS feedback_rows,
       (SELECT count(*) FROM rule_audit_log)         AS audit_rows,
       (SELECT count(*) FROM entity_representative_metric) AS rep_metric_rows;
"
```

## 3. Outbox Recovery

Table: `event_outbox` in Postgres.

### Pending events diagnostic

```bash
psql "$DATABASE_URL" -c "
SELECT id, event_type, dedup_key, status, attempts, last_error,
       next_attempt_at, created_at
  FROM event_outbox WHERE status='pending'
 ORDER BY next_attempt_at ASC, id ASC LIMIT 50;
"
```

### Stuck events (status='failed')

```bash
psql "$DATABASE_URL" -c "
SELECT id, event_type, attempts, last_error, created_at
  FROM event_outbox WHERE status='failed' ORDER BY id LIMIT 50;
"
```

### Requeue stale pending events

```bash
psql "$DATABASE_URL" -c "
UPDATE event_outbox SET next_attempt_at=NOW(), attempts=attempts+1
 WHERE status='pending' AND next_attempt_at < NOW() - INTERVAL '5 minutes';
"
```

### Requeue failed events (operator override, 24h window)

```bash
psql "$DATABASE_URL" -c "
UPDATE event_outbox
   SET status='pending', next_attempt_at=NOW(), attempts=0, last_error=NULL
 WHERE status='failed' AND created_at > NOW() - INTERVAL '24 hours';
"
```
Caution: only for transient failures.

## 4. Provider Access Safety

### FRED and Alpha Vantage shared quota windows

```bash
psql "$DATABASE_URL" -c "
SELECT provider, window_start, window_end,
       used AS reservations, failures, updated_at
  FROM provider_quota
 WHERE window_end > NOW() - INTERVAL '24 hours'
 ORDER BY provider, window_start DESC LIMIT 20;
"
```
`provider_quota` is the cross-process persistent budget used by FRED and Alpha
Vantage when `PROVIDER_QUOTA_DB_URL` is configured. Alpha Vantage is capped at
25 reservations per rolling 24-hour window; without the database setting, its
shared process-local limiter enforces the same cap across reconnects. Yahoo
Finance, CoinGecko, mempool.space, and other providers use the shared safe HTTP
client's in-process limiter/circuit/retry controls and must be verified from
redacted service logs and provider-specific counters instead.

### Persistent provider quota health probe

```bash
psql "$DATABASE_URL" -t -c "
SELECT CASE WHEN count(*)>0 THEN 'quota_ok'
            ELSE 'quota_stale: no rows in provider_quota' END AS probe
  FROM provider_quota WHERE window_end > NOW() - INTERVAL '24 hours';
"
```
Expected: `quota_ok` only when FRED or Alpha Vantage is enabled with
`PROVIDER_QUOTA_DB_URL` and has made a request. An empty table is valid when
neither provider ran with persistent quota enabled; it is not evidence about
other providers.

## 5. Rollback Playbook

### Step 1 — Stop the worker

```bash
sudo systemctl stop sonde-core
# or: docker compose stop core
```

### Step 2 — Run DOWN migrations

```bash
migrate -path migrations -database "$DATABASE_URL" down 5
```
Executes migrations 011, 010, 009, 008, and 007 in reverse order. This matches the
schema at the MVP rollback commit. To step one at a time:

```bash
migrate -path migrations -database "$DATABASE_URL" down 1
migrate -path migrations -database "$DATABASE_URL" down 1
migrate -path migrations -database "$DATABASE_URL" down 1
migrate -path migrations -database "$DATABASE_URL" down 1
migrate -path migrations -database "$DATABASE_URL" down 1
```

### Step 3 — Confirm version 006

```bash
migrate -path migrations -database "$DATABASE_URL" version
```
Expected: `006`.

### Step 4 — Revert binary

```bash
# The repository's MVP release baseline is commit `3d9472c`.
# Build/deploy an artifact from that commit in a separate worktree or registry;
# no `v0.8.0` image is part of this repository.
ROLLBACK_COMMIT=3d9472c
git worktree add /private/tmp/sonde-mvp-rollback "$ROLLBACK_COMMIT"
# Deploy the artifact built from $ROLLBACK_COMMIT using the environment's
# normal process/container mechanism, then remove the worktree after rollback.
```

### Step 5 — Post-rollback verification

```bash
# No stale tables from migrations 007 and 009
psql "$DATABASE_URL" -c "
SELECT count(*) AS stale_tables FROM information_schema.tables
 WHERE table_schema='public' AND table_name IN (
   'provider_quota','research_feedbacks','rule_audit_log',
   'entity_representative_metric','manual_relations');"
# Expected: 0

# No stale relation_suggestions column from migration 008
psql "$DATABASE_URL" -c "
SELECT count(*) AS stale_column FROM information_schema.columns
 WHERE table_name='relation_suggestions' AND column_name='source';"
# Expected: 0

# Process health (the implemented route is /api/health)
curl -sf http://localhost:8080/api/health || echo "HEALTH_FAIL"
```

If the migration version is `006`, `/api/health` returns 200, and both SQL
probes return `0`, rollback is complete.
Monitor `event_outbox` for minutes to confirm the reverted binary does not
crash on pending events.

## Sign-off

| Check | Command | Expected |
|-------|---------|----------|
| Migration version | `migrate ... version` | `011` |
| Provider quota rows | §4.1 | >= 1 when FRED or Alpha Vantage is enabled with persistent quota |
| event_outbox stuck | §3.2 | 0 rows or known reason |
| Backup integrity | §2.2 | `BACKUP_OK` |
| Health endpoint | `curl /api/health` | HTTP 200 |
