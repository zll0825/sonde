# Capital Observatory — Database Schema

版本：1.0
状态：Frozen（Sprint 0.3）
引擎：PostgreSQL 16 + TimescaleDB 2.16

---

## 说明

本文档整合系统架构中的所有 DDL，形成完整的数据库 Schema。每张表对应 Domain Model 的一个对象。

迁移工具（选择其一）：golang-migrate 或 atlas

---

## 1. plugins

Domain: Plugin

```sql
CREATE TABLE plugins (
    id                  TEXT PRIMARY KEY,           -- "plg_etf"
    name                TEXT NOT NULL,              -- "etf"
    version             TEXT NOT NULL,              -- "1.2.0"
    description         TEXT,
    state               TEXT DEFAULT 'starting',    -- starting | running | degraded | error
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
```

---

## 2. entities

Domain: Entity（版本化）

```sql
CREATE TABLE entities (
    id              TEXT NOT NULL,              -- "ent_gold_etf_flow"
    version         INT NOT NULL DEFAULT 1,
    name            TEXT NOT NULL,              -- "黄金ETF资金流"
    namespace       TEXT NOT NULL,              -- "etf"
    entity_type     TEXT NOT NULL,              -- asset | instrument | flow | institution | indicator | index | market
    plugin_id       TEXT NOT NULL REFERENCES plugins(id),
    tags            JSONB DEFAULT '[]',
    metadata        JSONB DEFAULT '{}',
    effective_from  TIMESTAMPTZ NOT NULL,
    effective_to    TIMESTAMPTZ,
    supersedes      INT,                        -- 前一版本号
    change_log      TEXT,
    created_at      TIMESTAMPTZ DEFAULT NOW(),

    PRIMARY KEY (id, version)
);

CREATE INDEX idx_entities_current ON entities(id, version)
    WHERE effective_to IS NULL;
CREATE INDEX idx_entities_plugin ON entities(plugin_id);
CREATE INDEX idx_entities_type ON entities(entity_type);
```

---

## 3. metric_definitions

Domain: Metric（版本化）

```sql
CREATE TABLE metric_definitions (
    id              TEXT NOT NULL,              -- "gold.etf.net_inflow"
    uid             TEXT NOT NULL,              -- "mtr_abc123def456" (Core assigned, immutable)
    version         INT NOT NULL DEFAULT 1,
    name            TEXT NOT NULL,              -- "黄金ETF净流入"
    description     TEXT,
    unit            TEXT NOT NULL,              -- "USD", "%", "count", "bps"
    frequency       TEXT NOT NULL,              -- "daily", "hourly", "realtime", "weekly", "quarterly"
    entity_id       TEXT NOT NULL,              -- 字符串，不做 FK（Isolation Principle）
    plugin_id       TEXT REFERENCES plugins(id),       -- NULL for Core-inferred candidates
    source          TEXT NOT NULL DEFAULT 'plugin_suggested', -- plugin_suggested | system_inferred
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
```

---

## 4. relation_suggestions

Domain: Relation（建议阶段）

```sql
CREATE TABLE relation_suggestions (
    id              SERIAL PRIMARY KEY,
    source_id       TEXT NOT NULL,
    target_id       TEXT NOT NULL,
    relation_type   TEXT NOT NULL,              -- tracks | causes | leads | correlates | ...
    direction       TEXT NOT NULL DEFAULT 'forward',
    confidence      FLOAT DEFAULT 0.5,
    typical_lag     INTERVAL,
    description     TEXT,
    evidence        TEXT,
    plugin_id       TEXT REFERENCES plugins(id),       -- NULL for Core-inferred candidates
    source          TEXT NOT NULL DEFAULT 'plugin_suggested', -- plugin_suggested | system_inferred
    status          TEXT NOT NULL DEFAULT 'pending',  -- pending | accepted | rejected | merged
    merged_into_id  INT,                        -- → relations.id
    review_reason   TEXT,
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW(),

    UNIQUE(source_id, target_id, relation_type, plugin_id)
);

CREATE UNIQUE INDEX uq_relation_suggestions_system_candidate
    ON relation_suggestions (source_id, target_id, relation_type)
    WHERE plugin_id IS NULL AND source = 'system_inferred';
```

---

## 5. relations

Domain: Relation（生效阶段，版本化）

```sql
CREATE TABLE relations (
    id              SERIAL PRIMARY KEY,
    source_id       TEXT NOT NULL,
    target_id       TEXT NOT NULL,
    relation_type   TEXT NOT NULL,
    layer           TEXT NOT NULL,              -- structural | semantic | statistical | causal
    direction       TEXT NOT NULL DEFAULT 'forward',
    confidence      FLOAT DEFAULT 0.5,
    typical_lag     INTERVAL,
    description     TEXT,
    source          TEXT NOT NULL DEFAULT 'plugin_suggested',  -- plugin_suggested | system_inferred | user_defined
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
```

---

## 6. rule_suggestions

Domain: Rule（建议阶段）

```sql
CREATE TABLE rule_suggestions (
    id              SERIAL PRIMARY KEY,
    name            TEXT NOT NULL,
    metric_id       TEXT NOT NULL,
    detector_name   TEXT NOT NULL,              -- threshold | percentile | trend | volatility | moving_average
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
```

---

## 7. rules

Domain: Rule（生效阶段，版本化）

```sql
CREATE TABLE rules (
    id              SERIAL PRIMARY KEY,
    name            TEXT NOT NULL,
    metric_id       TEXT NOT NULL,
    detector_name   TEXT NOT NULL,
    severity        TEXT NOT NULL DEFAULT 'warning',
    config          JSONB NOT NULL,
    description     TEXT,
    enabled         BOOLEAN DEFAULT TRUE,
    source          TEXT NOT NULL DEFAULT 'plugin_suggested',  -- plugin_suggested | user_override | system_default
    is_override     BOOLEAN DEFAULT FALSE,    -- true = user explicitly changed
    version         INT NOT NULL DEFAULT 1,
    effective_from  TIMESTAMPTZ NOT NULL,
    effective_to    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW(),

    UNIQUE(name, metric_id, detector_name, version)
);

CREATE INDEX idx_rules_current ON rules(name, metric_id, detector_name, version)
    WHERE effective_to IS NULL AND enabled = TRUE;
```

---

## 8. observations

Domain: Observation（不作 FK 到 Ontology 表，隔离原则）

```sql
CREATE TABLE observations (
    time                    TIMESTAMPTZ NOT NULL,
    metric_id               TEXT NOT NULL,          -- "gold.etf.net_inflow"
    metric_uid              TEXT NOT NULL,          -- "mtr_abc123def456"
    value                   DOUBLE PRECISION NOT NULL,
    labels                  JSONB DEFAULT '{}',
    labels_hash             TEXT NOT NULL DEFAULT '',  -- md5(sorted k=v)

    -- 溯源
    source_plugin           TEXT NOT NULL,
    source_plugin_version   TEXT NOT NULL,
    source_provider         TEXT NOT NULL,
    source_class            TEXT NOT NULL DEFAULT 'unknown'
        CONSTRAINT observations_source_class_check
        CHECK (source_class IN ('real', 'mock', 'test', 'unknown')),
    source_fetched_at       TIMESTAMPTZ NOT NULL,

    -- 质量
    quality_grade           TEXT DEFAULT 'delayed',   -- realtime | delayed | estimated | preliminary | revised
    quality_confidence      FLOAT DEFAULT 0.8,
    system_quality_score    FLOAT DEFAULT NULL,       -- computed by Core on insert

    ingested_at             TIMESTAMPTZ DEFAULT NOW()
);

SELECT create_hypertable('observations', 'time');
CREATE INDEX idx_obs_metric_uid_time ON observations(metric_uid, time DESC);
CREATE INDEX idx_obs_source ON observations(source_plugin, source_provider);

-- 幂等保护
CREATE UNIQUE INDEX idx_obs_idempotency
ON observations(metric_uid, time, source_plugin, source_provider, labels_hash);
```

### 8.1 写入 SQL（固化）

```sql
INSERT INTO observations (
    metric_id, metric_uid, time, value, source_plugin,
    source_plugin_version, source_provider, source_class, source_fetched_at,
    quality_grade, quality_confidence, labels_hash, labels
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
ON CONFLICT (metric_uid, time, source_plugin, source_provider, labels_hash)
DO UPDATE SET
    value = EXCLUDED.value,
    quality_grade = EXCLUDED.quality_grade,
    quality_confidence = EXCLUDED.quality_confidence,
    source_fetched_at = EXCLUDED.source_fetched_at,
    source_class = EXCLUDED.source_class,
    ingested_at = NOW()
WHERE EXCLUDED.quality_grade = 'revised'
  AND observations.quality_grade != 'revised';
```

---

## 9. pending_metrics

```sql
CREATE TABLE pending_metrics (
    metric_id               TEXT PRIMARY KEY,
    first_seen_at           TIMESTAMPTZ DEFAULT NOW(),
    first_seen_from_plugin  TEXT NOT NULL,
    status                  TEXT DEFAULT 'unknown_source'  -- unknown_source | registered
);
```

---

## 10. source_preferences

Domain: Metric 的多数据源优先级

```sql
CREATE TABLE source_preferences (
    metric_id           TEXT NOT NULL,
    source_plugin       TEXT NOT NULL,
    source_provider     TEXT NOT NULL,
    priority            INT NOT NULL DEFAULT 0,      -- 0 = primary
    effective_from      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    is_manual_override  BOOLEAN DEFAULT FALSE,      -- true = won't be auto-updated
    reason              TEXT,
    set_by              TEXT DEFAULT 'system',       -- "system" | "user:{id}"
    created_at          TIMESTAMPTZ DEFAULT NOW(),
    updated_at          TIMESTAMPTZ DEFAULT NOW(),

    PRIMARY KEY (metric_id, source_plugin, source_provider)
);
```

---

## 11. alerts

Domain: Alert

```sql
CREATE TABLE alerts (
    id              TEXT PRIMARY KEY,               -- "alt_20260730_abc123"
    title           TEXT NOT NULL,
    summary         TEXT NOT NULL,
    severity        TEXT NOT NULL,                  -- critical | warning | info
    status          TEXT NOT NULL DEFAULT 'active', -- active | resolved
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

-- 同一 dedup_key 只能有一个 active alert
CREATE UNIQUE INDEX idx_alerts_active_dedup ON alerts(dedup_key) WHERE status = 'active';
```

---

## 12. research_snapshots

Domain: Research Context（Alert 触发时冻结）

```sql
CREATE TABLE research_snapshots (
    alert_id              TEXT PRIMARY KEY REFERENCES alerts(id),
    context               JSONB NOT NULL,       -- frozen ResearchContext
    ontology_frozen_at    TIMESTAMPTZ NOT NULL,
    created_at            TIMESTAMPTZ DEFAULT NOW()
);
```

---

## 13. event_outbox

```sql
CREATE TABLE event_outbox (
    id              SERIAL PRIMARY KEY,
    event_type      TEXT NOT NULL,              -- "MetricUpdated"
    payload         JSONB NOT NULL,             -- {metric_id, metric_uid, timestamp}
    status          TEXT DEFAULT 'pending',     -- pending | processing | done | failed
    attempts        INT DEFAULT 0,
    created_at      TIMESTAMPTZ DEFAULT NOW()
);
```

---

## 14. command_log

Domain: Command

```sql
CREATE TABLE command_log (
    command_id      TEXT PRIMARY KEY,               -- "cmd_sync_20260730_001"
    command_type    TEXT NOT NULL,                   -- "sync" | "backfill"
    target_plugin   TEXT NOT NULL,
    requested_by    TEXT NOT NULL,                   -- "system" | "user:{id}"
    status          TEXT DEFAULT 'pending',          -- pending | dispatched | completed | failed
    reason          TEXT,
    metric_ids      JSONB DEFAULT '[]',
    window_start    TIMESTAMPTZ,
    window_end      TIMESTAMPTZ,
    collected_count INT,
    error           TEXT,
    requested_at    TIMESTAMPTZ DEFAULT NOW(),
    accepted_at     TIMESTAMPTZ,
    completed_at    TIMESTAMPTZ,
    attempts        INT NOT NULL DEFAULT 0,          -- 原子领取次数，最多 5 次
    last_dispatched_at TIMESTAMPTZ,                  -- 最近领取时间
    lease_expires_at TIMESTAMPTZ,                    -- dispatched 租约截止时间
    last_error      TEXT,                            -- 最近租约/派发/执行错误
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_command_log_dispatchable
    ON command_log(status, lease_expires_at, requested_at)
    WHERE status IN ('pending', 'dispatched');
```

## 15. Post-MVP contract tables

Migrations 008-010 extend the review and operational contracts without
rewriting historical alerts, observations, or ontology versions.

```sql
-- 008: Core statistical candidates share the plugin review queue.
ALTER TABLE relation_suggestions
    ALTER COLUMN plugin_id DROP NOT NULL;
ALTER TABLE relation_suggestions
    ADD COLUMN source TEXT NOT NULL DEFAULT 'plugin_suggested'
        CHECK (source IN ('plugin_suggested', 'system_inferred'));
CREATE UNIQUE INDEX uq_relation_suggestions_system_candidate
    ON relation_suggestions (source_id, target_id, relation_type)
    WHERE plugin_id IS NULL AND source = 'system_inferred';

-- 009: one rolling quota window per provider and immutable research/rule audit.
CREATE TABLE provider_quota (
    provider TEXT NOT NULL,
    window_start TIMESTAMPTZ NOT NULL,
    window_end TIMESTAMPTZ NOT NULL,
    used INT NOT NULL DEFAULT 0,
    failures INT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (provider, window_start)
);

CREATE TABLE research_feedbacks (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    alert_id TEXT NOT NULL REFERENCES research_snapshots(alert_id),
    cluster_id TEXT,
    verdict TEXT NOT NULL CHECK (verdict IN ('worth_researching','irrelevant','duplicate')),
    rationale TEXT,
    user_id TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE rule_audit_log (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    rule_id INT NOT NULL REFERENCES rules(id),
    scope TEXT NOT NULL DEFAULT 'global',
    field TEXT NOT NULL,
    old_value JSONB NOT NULL,
    new_value JSONB NOT NULL,
    actor TEXT NOT NULL DEFAULT 'system',
    reason TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE entity_representative_metric (
    entity_id TEXT NOT NULL,
    purpose TEXT NOT NULL DEFAULT 'default',
    metric_id TEXT NOT NULL,
    source TEXT NOT NULL DEFAULT 'manual',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (entity_id, purpose)
);

-- 010: statistical evidence retained when a candidate becomes manual.
ALTER TABLE manual_relations
    ADD COLUMN extension JSONB NOT NULL DEFAULT '{}';
```

`provider_quota` is enforced by the API client's atomic reservation path;
`research_feedbacks.alert_id` points to a frozen snapshot, and rule mutations
always create a new version before writing `rule_audit_log`.

---

## 16. 索引汇总

| 表 | 索引 | 类型 | 用途 |
|----|------|------|------|
| entities | `idx_entities_current` | Partial | 查当前生效版本 |
| entities | `idx_entities_plugin` | Normal | 按 Plugin 查 Entity |
| entities | `idx_entities_type` | Normal | 按类型查 |
| metric_definitions | `idx_metric_def_current` | Partial | 查当前生效版本 |
| metric_definitions | `idx_metric_def_uid` | Normal | 按 uid 查 |
| relations | `idx_relations_current` | Partial | 查当前生效关系 |
| relations | `idx_relations_source` | Normal | 图遍历 |
| relations | `idx_relations_target` | Normal | 图遍历 |
| rules | `idx_rules_current` | Partial | 查当前生效的启用规则 |
| observations | `idx_obs_metric_uid_time` | Composite | 时间范围扫描 |
| observations | `idx_obs_source` | Composite | 数据源审计 |
| observations | `idx_obs_idempotency` | Unique | 幂等保护 |
| alerts | `idx_alerts_status` | Normal | 首页查询 |
| command_log | `idx_command_log_dispatchable` | Partial | 待派发命令与过期租约领取 |
| alerts | `idx_alerts_severity` | Normal | 按严重级过滤 |
| alerts | `idx_alerts_triggered` | Normal | 时间排序 |
| alerts | `idx_alerts_active_dedup` | Unique Partial | 活跃告警去重 |

---

## 17. 迁移策略

```sql
-- 迁移文件命名: migrations/001_baseline.up.sql

-- 迁移原则:
-- 1. 每个版本一个 migration pair（.up.sql / .down.sql）
-- 2. 所有 version 表（entities, metric_definitions, relations, rules）
--    使用 effective_from/effective_to 而非 DELETE
-- 3. observations 是 append-only（只 INSERT + 条件 UPDATE）
-- 4. 历史数据绝不删除，标记 retired/deprecated
```
