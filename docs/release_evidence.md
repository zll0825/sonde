# Release Evidence — Post-MVP Product Contract

Operational verification checklist for the Post-MVP Product Contract release.
Every command is self-contained. Adjust `$DATABASE_URL` as needed.

## Environment

```bash
export DATABASE_URL="postgres://capital:${PGPASS}@localhost:5432/capital_observatory?sslmode=disable"
```

## 1. Database Migrations

### Verify version

```bash
migrate -path migrations -database "$DATABASE_URL" version
```
Expected: `010` (post-apply) or `008` (pre-apply).

### Apply migrations 009 + 010 (idempotent)

```bash
migrate -path migrations -database "$DATABASE_URL" up
```
Migration 009 creates `provider_quota`, `research_feedbacks`, `rule_audit_log`,
`entity_representative_metric` + indexes. Migration 010 adds `extension JSONB`
+ GIN index to `manual_relations`.

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
BACKUP_DIR="${BACKUP_DIR:-/var/backups/capital_observatory}"
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
STAGING_URL="postgres://capital@localhost:5432/capital_observatory_staging"
dropdb --if-exists -h localhost -p 5432 -U capital capital_observatory_staging
createdb  -h localhost -p 5432 -U capital capital_observatory_staging
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

## 4. Provider Quota Verification

### Latest windows per provider

```bash
psql "$DATABASE_URL" -c "
SELECT provider, window_start, window_end,
       used AS successes, failures, updated_at
  FROM provider_quota
 WHERE window_end > NOW() - INTERVAL '24 hours'
 ORDER BY provider, window_start DESC LIMIT 20;
"
```
Expected: >= 1 row per active provider (`fred`, `yahoo_finance`, `coingecko`,
`mempool_space`, `blockchain_com`).

### Health probe

```bash
psql "$DATABASE_URL" -t -c "
SELECT CASE WHEN count(*)>0 THEN 'quota_ok'
            ELSE 'quota_stale: no rows in provider_quota' END AS probe
  FROM provider_quota WHERE window_end > NOW() - INTERVAL '24 hours';
"
```
Expected: `quota_ok`.

## 5. Rollback Playbook

### Step 1 — Stop the worker

```bash
sudo systemctl stop capital-core
# or: docker compose stop core
```

### Step 2 — Run DOWN migrations

```bash
migrate -path migrations -database "$DATABASE_URL" down 2
```
Executes `010_manual_relations_extension.down.sql` then
`009_research_feedback.down.sql`. To step one at a time:

```bash
migrate -path migrations -database "$DATABASE_URL" down 1
migrate -path migrations -database "$DATABASE_URL" down 1
```

### Step 3 — Confirm version 008

```bash
migrate -path migrations -database "$DATABASE_URL" version
```
Expected: `008`.

### Step 4 — Revert binary

```bash
# Container:
docker tag capital_observatory/core:v0.8.0 capital_observatory/core:latest
docker compose up -d core

# Or systemd symlink:
sudo ln -sf /opt/capital/core-v0.8.0 /opt/capital/core
sudo systemctl start capital-core
```

### Step 5 — Post-rollback verification

```bash
# No stale tables from migration 009
psql "$DATABASE_URL" -c "
SELECT count(*) AS stale_tables FROM information_schema.tables
 WHERE table_schema='public' AND table_name IN (
   'provider_quota','research_feedbacks','rule_audit_log',
   'entity_representative_metric');"
# Expected: 0

# No stale column from migration 010
psql "$DATABASE_URL" -c "
SELECT count(*) AS stale_column FROM information_schema.columns
 WHERE table_name='manual_relations' AND column_name='extension';"
# Expected: 0

# Process health
curl -sf http://localhost:8080/healthz || echo "HEALTHZ_FAIL"
```

If `/healthz` returns 200 and both SQL probes return `0`, rollback is complete.
Monitor `event_outbox` for minutes to confirm the reverted binary does not
crash on pending events.

## Sign-off

| Check | Command | Expected |
|-------|---------|----------|
| Migration version | `migrate ... version` | `010` |
| provider_quota rows | §4.1 | >= 1 per active provider |
| event_outbox stuck | §3.2 | 0 rows or known reason |
| Backup integrity | §2.2 | `BACKUP_OK` |
| Health endpoint | `curl /healthz` | HTTP 200 |
