-- Migration 009: research feedback, rule audit trail, provider quota,
--                 metric-purpose registry for representative metric,
--                 historical-analog references.
--
-- These tables fix the remaining product-contract gaps identified in the
-- Post-MVP roadmap review: (P1#4) research feedback no longer 501,
-- (P1#6) rule changes carry an immutable audit trail, (P0#1) provider quota
-- is shared across processes, (P1#9) representative metric is configurable.

-- -----------------------------------------------------------------------------
-- P0#1 — Cross-process provider quota (Macro + Commodities share one budget).
-- Each row is a rolling 24h window for one provider, credited on every fetch.
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS provider_quota (
    provider    TEXT NOT NULL,                 -- 'fred' | 'commodities' | ...
    window_start TIMESTAMPTZ NOT NULL,         -- start of the 24h window
    window_end   TIMESTAMPTZ NOT NULL,
    used         INT NOT NULL DEFAULT 0,       -- successful fetches in window
    failures     INT NOT NULL DEFAULT 0,       -- failed fetches (do not count)
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (provider, window_start)
);
CREATE INDEX IF NOT EXISTS idx_provider_quota_window
    ON provider_quota (provider, window_end);

-- -----------------------------------------------------------------------------
-- P1#4 — Research feedback: worth_researching / irrelevant / duplicate.
-- Each feedback row is immutable and tied to one snapshot.
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS research_feedbacks (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    alert_id        TEXT NOT NULL REFERENCES research_snapshots(alert_id),
    cluster_id      TEXT,
    verdict         TEXT NOT NULL CHECK (verdict IN ('worth_researching','irrelevant','duplicate')),
    rationale       TEXT,
    user_id         TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_research_feedbacks_alert
    ON research_feedbacks (alert_id);
CREATE INDEX IF NOT EXISTS idx_research_feedbacks_verdict
    ON research_feedbacks (verdict);

-- -----------------------------------------------------------------------------
-- P1#6 — Immutable, append-only audit trail for rule mutations.
-- Every PATCH/override creates a new row; the active config is the latest
-- row per (rule_id, scope). Lets operators reconstruct who changed what,
-- when, from what previous value — and restores to any prior revision.
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS rule_audit_log (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    rule_id         INT NOT NULL REFERENCES rules(id),
    scope           TEXT NOT NULL DEFAULT 'global',  -- 'global' | 'override' | 'suggested'
    field           TEXT NOT NULL,                    -- 'enabled' | 'param_override' | ...
    old_value       JSONB NOT NULL,
    new_value       JSONB NOT NULL,
    actor           TEXT NOT NULL DEFAULT 'system',  -- API caller / plugin id
    reason          TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_rule_audit_log_rule
    ON rule_audit_log (rule_id, created_at DESC);

-- -----------------------------------------------------------------------------
-- P1#9 — Representative metric configuration per entity.
-- Rows declare which metric_id represents the entity for research context,
-- overlay, and candidate purposes. Without a row the store falls back to the
-- legacy deterministic-but-possibly-misleading heuristic (sort-first).
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS entity_representative_metric (
    entity_id       TEXT NOT NULL,
    purpose         TEXT NOT NULL DEFAULT 'default',  -- 'default'|'research'|'overlay'
    metric_id       TEXT NOT NULL,
    source          TEXT NOT NULL DEFAULT 'manual',   -- 'manual'|'auto'|'plugin_suggested'
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (entity_id, purpose)
);
