-- M0 Baseline Migration
-- Full DDL from docs/database-schema.md (frozen v1.0)
-- Tool: golang-migrate (file-based, raw SQL)

-- ============================================================
-- 1. plugins
-- ============================================================
CREATE TABLE plugins (
    id                  TEXT PRIMARY KEY,
    name                TEXT NOT NULL,
    version             TEXT NOT NULL,
    description         TEXT,
    state               TEXT DEFAULT 'starting',
    healthy             BOOLEAN DEFAULT FALSE,
    last_heartbeat      TIMESTAMPTZ,
    last_collect_at     TIMESTAMPTZ,
    last_collect_count  INT DEFAULT 0,
    last_collect_error  TEXT,
    consecutive_errors  INT DEFAULT 0,
    registration_version INT DEFAULT 0,
    created_at          TIMESTAMPTZ DEFAULT NOW(),
    updated_at          TIMESTAMPTZ DEFAULT NOW()
);

-- ============================================================
-- 2. entities
-- ============================================================
CREATE TABLE entities (
    id              TEXT NOT NULL,
    version         INT NOT NULL DEFAULT 1,
    name            TEXT NOT NULL,
    namespace       TEXT NOT NULL,
    entity_type     TEXT NOT NULL,
    plugin_id       TEXT NOT NULL REFERENCES plugins(id),
    tags            JSONB DEFAULT '[]',
    metadata        JSONB DEFAULT '{}',
    effective_from  TIMESTAMPTZ NOT NULL,
    effective_to    TIMESTAMPTZ,
    supersedes      INT,
    change_log      TEXT,
    created_at      TIMESTAMPTZ DEFAULT NOW(),

    PRIMARY KEY (id, version)
);

CREATE INDEX idx_entities_current ON entities(id, version)
    WHERE effective_to IS NULL;
CREATE INDEX idx_entities_plugin ON entities(plugin_id);
CREATE INDEX idx_entities_type ON entities(entity_type);

-- ============================================================
-- 3. metric_definitions
-- ============================================================
CREATE TABLE metric_definitions (
    id              TEXT NOT NULL,
    uid             TEXT NOT NULL,
    version         INT NOT NULL DEFAULT 1,
    name            TEXT NOT NULL,
    description     TEXT,
    unit            TEXT NOT NULL,
    frequency       TEXT NOT NULL,
    entity_id       TEXT NOT NULL,
    plugin_id       TEXT NOT NULL REFERENCES plugins(id),
    tags            JSONB DEFAULT '{}',
    active          BOOLEAN DEFAULT TRUE,
    effective_from  TIMESTAMPTZ NOT NULL,
    effective_to    TIMESTAMPTZ,
    supersedes      INT,
    change_log      TEXT,
    created_at      TIMESTAMPTZ DEFAULT NOW(),

    PRIMARY KEY (id, version)
);

CREATE INDEX idx_metric_def_current ON metric_definitions(id, version)
    WHERE effective_to IS NULL;
CREATE INDEX idx_metric_def_uid ON metric_definitions(uid);
CREATE INDEX idx_metric_def_plugin ON metric_definitions(plugin_id);

-- ============================================================
-- 4. relation_suggestions
-- ============================================================
CREATE TABLE relation_suggestions (
    id              SERIAL PRIMARY KEY,
    source_id       TEXT NOT NULL,
    target_id       TEXT NOT NULL,
    relation_type   TEXT NOT NULL,
    direction       TEXT NOT NULL DEFAULT 'forward',
    confidence      FLOAT DEFAULT 0.5,
    typical_lag     INTERVAL,
    description     TEXT,
    evidence        TEXT,
    plugin_id       TEXT NOT NULL REFERENCES plugins(id),
    status          TEXT NOT NULL DEFAULT 'pending',
    merged_into_id  INT,
    review_reason   TEXT,
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW(),

    UNIQUE(source_id, target_id, relation_type, plugin_id)
);

-- ============================================================
-- 5. relations
-- ============================================================
CREATE TABLE relations (
    id              SERIAL PRIMARY KEY,
    source_id       TEXT NOT NULL,
    target_id       TEXT NOT NULL,
    relation_type   TEXT NOT NULL,
    layer           TEXT NOT NULL,
    direction       TEXT NOT NULL DEFAULT 'forward',
    confidence      FLOAT DEFAULT 0.5,
    typical_lag     INTERVAL,
    description     TEXT,
    source          TEXT NOT NULL DEFAULT 'plugin_suggested',
    version         INT NOT NULL DEFAULT 1,
    effective_from  TIMESTAMPTZ NOT NULL,
    effective_to    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW(),

    UNIQUE(source_id, target_id, relation_type, version)
);

CREATE INDEX idx_relations_current ON relations(source_id, target_id, relation_type, version)
    WHERE effective_to IS NULL;
CREATE INDEX idx_relations_source ON relations(source_id);
CREATE INDEX idx_relations_target ON relations(target_id);

-- ============================================================
-- 6. rule_suggestions
-- ============================================================
CREATE TABLE rule_suggestions (
    id              SERIAL PRIMARY KEY,
    name            TEXT NOT NULL,
    metric_id       TEXT NOT NULL,
    detector_name   TEXT NOT NULL,
    severity        TEXT NOT NULL DEFAULT 'warning',
    config          JSONB NOT NULL,
    description     TEXT,
    plugin_id       TEXT NOT NULL REFERENCES plugins(id),
    status          TEXT NOT NULL DEFAULT 'pending',
    review_reason   TEXT,
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW(),

    UNIQUE(name, metric_id, detector_name, plugin_id)
);

-- ============================================================
-- 7. rules
-- ============================================================
CREATE TABLE rules (
    id              SERIAL PRIMARY KEY,
    name            TEXT NOT NULL,
    metric_id       TEXT NOT NULL,
    detector_name   TEXT NOT NULL,
    severity        TEXT NOT NULL DEFAULT 'warning',
    config          JSONB NOT NULL,
    description     TEXT,
    enabled         BOOLEAN DEFAULT TRUE,
    source          TEXT NOT NULL DEFAULT 'plugin_suggested',
    is_override     BOOLEAN DEFAULT FALSE,
    version         INT NOT NULL DEFAULT 1,
    effective_from  TIMESTAMPTZ NOT NULL,
    effective_to    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW(),

    UNIQUE(name, metric_id, detector_name, version)
);

CREATE INDEX idx_rules_current ON rules(name, metric_id, detector_name, version)
    WHERE effective_to IS NULL AND enabled = TRUE;

-- ============================================================
-- 8. observations (TimescaleDB hypertable)
-- ============================================================
CREATE TABLE observations (
    time                    TIMESTAMPTZ NOT NULL,
    metric_id               TEXT NOT NULL,
    metric_uid              TEXT NOT NULL,
    value                   DOUBLE PRECISION NOT NULL,
    labels                  JSONB DEFAULT '{}',
    labels_hash             TEXT NOT NULL DEFAULT '',

    source_plugin           TEXT NOT NULL,
    source_plugin_version   TEXT NOT NULL,
    source_provider         TEXT NOT NULL,
    source_class            TEXT NOT NULL DEFAULT 'unknown'
        CONSTRAINT observations_source_class_check
        CHECK (source_class IN ('real', 'mock', 'test', 'unknown')),
    source_fetched_at       TIMESTAMPTZ NOT NULL,

    quality_grade           TEXT DEFAULT 'delayed',
    quality_confidence      FLOAT DEFAULT 0.8,
    system_quality_score    FLOAT DEFAULT NULL,

    ingested_at             TIMESTAMPTZ DEFAULT NOW()
);

SELECT create_hypertable('observations', 'time');
CREATE INDEX idx_obs_metric_uid_time ON observations(metric_uid, time DESC);
CREATE INDEX idx_obs_source ON observations(source_plugin, source_provider);
CREATE UNIQUE INDEX idx_obs_idempotency
    ON observations(metric_uid, time, source_plugin, source_provider, labels_hash);

-- ============================================================
-- 9. pending_metrics
-- ============================================================
CREATE TABLE pending_metrics (
    metric_id               TEXT PRIMARY KEY,
    first_seen_at           TIMESTAMPTZ DEFAULT NOW(),
    first_seen_from_plugin  TEXT NOT NULL,
    status                  TEXT DEFAULT 'unknown_source'
);

-- ============================================================
-- 10. source_preferences
-- ============================================================
CREATE TABLE source_preferences (
    metric_id           TEXT NOT NULL,
    source_plugin       TEXT NOT NULL,
    source_provider     TEXT NOT NULL,
    priority            INT NOT NULL DEFAULT 0,
    effective_from      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    is_manual_override  BOOLEAN DEFAULT FALSE,
    reason              TEXT,
    set_by              TEXT DEFAULT 'system',
    created_at          TIMESTAMPTZ DEFAULT NOW(),
    updated_at          TIMESTAMPTZ DEFAULT NOW(),

    PRIMARY KEY (metric_id, source_plugin, source_provider)
);

-- ============================================================
-- 11. alerts
-- ============================================================
CREATE TABLE alerts (
    id              TEXT PRIMARY KEY,
    title           TEXT NOT NULL,
    summary         TEXT NOT NULL,
    severity        TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'active',
    metric_id       TEXT NOT NULL,
    rule_id         INT NOT NULL,
    rule_version    INT NOT NULL,
    rule_effective_from TIMESTAMPTZ NOT NULL,
    detector_name   TEXT NOT NULL,
    dedup_key       TEXT NOT NULL,
    window_start    TIMESTAMPTZ,
    window_end      TIMESTAMPTZ,
    evidence        JSONB DEFAULT '{}',
    plugin_id       TEXT NOT NULL REFERENCES plugins(id),
    source_provider TEXT NOT NULL DEFAULT '',
    source_class    TEXT NOT NULL DEFAULT 'unknown'
        CONSTRAINT alerts_source_class_check
        CHECK (source_class IN ('real', 'mock', 'test', 'unknown')),
    dedup_count     INT NOT NULL DEFAULT 0
        CONSTRAINT alerts_dedup_count_check CHECK (dedup_count >= 0),
    last_deduplicated_at TIMESTAMPTZ,
    triggered_at    TIMESTAMPTZ DEFAULT NOW(),
    resolved_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_alerts_status ON alerts(status);
CREATE INDEX idx_alerts_severity ON alerts(severity);
CREATE INDEX idx_alerts_triggered ON alerts(triggered_at DESC);
CREATE INDEX idx_alerts_source_class_triggered ON alerts(source_class, triggered_at DESC);
CREATE UNIQUE INDEX idx_alerts_active_dedup ON alerts(dedup_key) WHERE status = 'active';

-- ============================================================
-- 12. research_snapshots
-- ============================================================
CREATE TABLE research_snapshots (
    alert_id              TEXT PRIMARY KEY REFERENCES alerts(id),
    context               JSONB NOT NULL,
    ontology_frozen_at    TIMESTAMPTZ NOT NULL,
    created_at            TIMESTAMPTZ DEFAULT NOW()
);

-- ============================================================
-- 13. event_outbox
-- ============================================================
CREATE TABLE event_outbox (
    id              SERIAL PRIMARY KEY,
    event_type      TEXT NOT NULL,
    payload         JSONB NOT NULL,
    dedup_key       TEXT,
    status          TEXT DEFAULT 'pending',
    attempts        INT DEFAULT 0,
    last_error      TEXT,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW()
);

CREATE UNIQUE INDEX idx_event_outbox_dedup
    ON event_outbox(event_type, dedup_key) WHERE dedup_key IS NOT NULL;
CREATE INDEX idx_event_outbox_pending
    ON event_outbox(next_attempt_at, id) WHERE status = 'pending';

-- ============================================================
-- 14. command_log
-- ============================================================
CREATE TABLE command_log (
    command_id      TEXT PRIMARY KEY,
    command_type    TEXT NOT NULL,
    target_plugin   TEXT NOT NULL,
    requested_by    TEXT NOT NULL,
    status          TEXT DEFAULT 'pending',
    reason          TEXT,
    metric_ids      JSONB DEFAULT '[]',
    window_start    TIMESTAMPTZ,
    window_end      TIMESTAMPTZ,
    collected_count INT,
    error           TEXT,
    requested_at    TIMESTAMPTZ DEFAULT NOW(),
    accepted_at     TIMESTAMPTZ,
    completed_at    TIMESTAMPTZ,
    attempts        INT NOT NULL DEFAULT 0,
    last_dispatched_at TIMESTAMPTZ,
    lease_expires_at TIMESTAMPTZ,
    last_error      TEXT,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_command_log_dispatchable
    ON command_log(status, lease_expires_at, requested_at)
    WHERE status IN ('pending', 'dispatched');
