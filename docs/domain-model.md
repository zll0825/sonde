# Capital Observatory — Domain Model Spec

版本：1.0
状态：Draft（Sprint 0.1）

---

## 概述

本文档定义 Capital Observatory 的五个核心领域对象。所有字段、状态、关系、约束在此统一，不散落在系统架构文档各处。

每条定义遵循同一结构：
- **身份**：唯一标识符、人可读名称
- **字段**：属性清单（名称、类型、必填、约束）
- **生命周期**：有效状态及状态转换
- **关系**：与其他领域对象的关联
- **约束**：不可违反的规则

---

## 1. Entity

### 1.1 身份

| 属性 | 说明 |
|------|------|
| ID 格式 | `ent_` 前缀 + Plugin 定义的人可读后缀（如 `ent_gold_etf_flow`） |
| 名称 | Plugin 声明的人可读名称（如 "黄金ETF资金流"） |
| 命名空间 | `namespace`，三段式 Metric 命名的第一段 |

### 1.2 字段

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `id` | TEXT | ✅ | 全局唯一，PK |
| `name` | TEXT | ✅ | 人可读名称 |
| `namespace` | TEXT | ✅ | 三段式命名的第一段，如 "etf"、"crypto" |
| `entity_type` | EntityType | ✅ | 七种类型之一 |
| `plugin_id` | TEXT | ✅ | FK → plugins，声明者 |
| `tags` | JSONB | ❌ | 自由标签 |
| `metadata` | JSONB | ❌ | Plugin 自定义扩展 |
| `version` | INT | ✅ | 单调递增 |
| `effective_from` | TIMESTAMPTZ | ✅ | 版本生效时间 |
| `effective_to` | TIMESTAMPTZ | ❌ | NULL = 当前生效 |
| `supersedes` | INT | ❌ | 前一版本号 |
| `change_log` | TEXT | ❌ | 变更说明 |

### 1.3 EntityType

```
asset       — 可交易资产（BTC、现货黄金、原油）
instrument  — 金融工具（ETF、期货合约、期权）
flow        — 资金流（北向资金、ETF净流入、交易所余额变化）
institution — 机构（美联储、日本央行、财政部）
indicator   — 宏观指标（CPI、PMI、GDP增长率、失业率）
index       — 指数（DXY、S&P 500、VIX）
market      — 市场/交易所（A股、美股、韩国KOSPI）
```

### 1.4 生命周期

```
CREATED → ACTIVE → DEPRECATED

CREATED:    Plugin 首次注册声明，version=1, effective_from=NOW()
ACTIVE:     effective_to IS NULL
DEPRECATED: 被新版本取代（effective_to 设为旧版本关停时间点）
            或被 Plugin 主动撤回（所有 version 标记 effective_to=NOW()）
```

状态由 `effective_from` / `effective_to` 隐式表达，不存显式 status 字段。

### 1.5 关系

- Entity 是 Metric 的观测对象（1:N）
- Entity 参与 Relation 作为 source 或 target（M:N）
- Entity 归属于一个 Plugin（N:1）

### 1.6 约束

- `id` 全局唯一，不可重名
- `entity_type` 不可变更（变更 = 新 Entity）
- `namespace` 不可变更（变更 = 新 Entity）
- 同一 `id` 在同一时刻只能有一个生效版本（`effective_to IS NULL` 唯一）

---

## 2. Metric

### 2.1 身份

| 属性 | 说明 |
|------|------|
| ID 格式 | `namespace.entity.metric` 三段式（如 `gold.etf.net_inflow`） |
| UID 格式 | `mtr_` + 随机串（如 `mtr_abc123def456`）—— Core 分配，终身不变 |

### 2.2 字段

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `id` | TEXT | ✅ | 三段式命名，PK |
| `uid` | TEXT | ✅ | `mtr_` 前缀，Core 分配，全局唯一 |
| `name` | TEXT | ✅ | 人可读名称 |
| `description` | TEXT | ❌ | 说明 |
| `unit` | TEXT | ✅ | 量纲（USD、%、count、bps、ratio） |
| `frequency` | TEXT | ✅ | 采集频率（daily、hourly、realtime、weekly、quarterly） |
| `entity_id` | TEXT | ✅ | 归属于哪个 Entity（字符串，不做 FK） |
| `plugin_id` | TEXT | ✅ | FK → plugins，声明者 |
| `tags` | JSONB | ❌ | 附加标签 |
| `active` | BOOLEAN | ✅ | 是否活跃（Plugin 撤回声明时设为 false） |
| `version` | INT | ✅ | 单调递增 |
| `effective_from` | TIMESTAMPTZ | ✅ | 版本生效时间 |
| `effective_to` | TIMESTAMPTZ | ❌ | NULL = 当前生效 |
| `supersedes` | INT | ❌ | 前一版本号（用于 metric 重命名追踪） |
| `change_log` | TEXT | ❌ | 变更说明 |

### 2.3 生命周期

```
REGISTERED → ACTIVE → DEPRECATED

REGISTERED: Plugin 首次注册，Core 分配 uid、version=1
ACTIVE:     active=true AND effective_to IS NULL
DEPRECATED: Plugin 撤回声明（active=false）
            或被重命名（旧 id 的 effective_to 设值，新 id 沿用旧 uid）
```

### 2.4 关系

- Metric 归属于一个 Entity（N:1）
- Metric 产生多条 Observation（1:N）
- Metric 被多条 Rule 引用（1:N）
- Metric 有多条 SourcePreference（1:N）

### 2.5 约束

- `id` 全局唯一（同一时刻生效版本唯一）
- `uid` 终身不变，跨重命名追踪
- `effective_to IS NULL` 在同一 `id` 下唯一
- 重命名时新 `id` 的 `uid` 必须沿用旧 `id` 的 `uid`

---

## 3. Relation

### 3.1 身份

| 属性 | 说明 |
|------|------|
| ID 格式 | `rel_` + 随机串（如 `rel_abc789`） |
| 唯一键 | `(source_id, target_id, relation_type)` |

### 3.2 字段

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `id` | SERIAL | ✅ | 自增 PK |
| `source_id` | TEXT | ✅ | 起始 Entity ID |
| `target_id` | TEXT | ✅ | 目标 Entity ID |
| `relation_type` | RelationType | ✅ | 关系类型 |
| `layer` | RelationLayer | ✅ | 四层分类（Core 推断，不存 suggestion 值） |
| `direction` | Direction | ✅ | forward / backward / bidirectional |
| `confidence` | FLOAT | ✅ | 0.0–1.0 |
| `typical_lag` | INTERVAL | ❌ | 典型滞后时间 |
| `description` | TEXT | ❌ | 人类可读描述 |
| `source` | TEXT | ✅ | 来源：plugin_suggested / system_inferred / user_defined |
| `version` | INT | ✅ | 单调递增 |
| `effective_from` | TIMESTAMPTZ | ✅ | 版本生效时间 |
| `effective_to` | TIMESTAMPTZ | ❌ | NULL = 当前生效 |

### 3.3 RelationType + Layer

| Layer | Type | 审核策略 |
|-------|------|---------|
| structural | `tracks` | 自动接受 |
| structural | `component_of` | 自动接受 |
| structural | `issued_by` | 自动接受 |
| structural | `belongs_to` | 自动接受 |
| semantic | `hedges` | 自动接受，标记为声明型 |
| semantic | `competes` | 自动接受，标记为声明型 |
| semantic | `signals` | 自动接受，标记为声明型 |
| statistical | `correlates` | 要求 StatisticalEvidence，p<0.05 ∧ n≥30 |
| statistical | `inversely_correlates` | 同上 |
| statistical | `leads` | 同上 |
| statistical | `lags` | 同上 |
| causal | `causes` | 默认 pending，需人工审核 |
| causal | `depends_on_regime` | 默认 pending，需人工审核 |

### 3.4 Direction

```
forward      — source → target
backward     — source ← target
bidirectional — source ↔ target
```

### 3.5 生命周期

```
SUGGESTED → ACCEPTED → ACTIVE → RETIRED

SUGGESTED: Plugin 通过 RegisterPlugin 提交 RelationSuggestion，存入 relation_suggestions
ACCEPTED:  Core 的 RelationManager 审核通过，写入 relations_v2
ACTIVE:     effective_to IS NULL
RETIRED:    被新版本取代，或被 Plugin 不再建议（升级时撤回）
```

### 3.6 关系

- Relation 连接两个 Entity（source → target）
- Relation 由一个 Plugin 建议（N:1），但最终由 Core 拥有

### 3.7 约束

- 同一 `(source_id, target_id, relation_type)` 在同一时刻只有一个生效版本
- causal 关系必须经过人工审核才能 ACTIVE
- statistical 关系必须通过统计阈值校验（p<0.05, n≥30）

---

## 4. Rule

### 4.1 身份

| 属性 | 说明 |
|------|------|
| ID 格式 | `rul_` + 描述性后缀（如 `rul_gold_trend_001`） |
| 唯一键 | `(name, metric_id, detector_name)` |

### 4.2 字段

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `id` | SERIAL | ✅ | 自增 PK |
| `name` | TEXT | ✅ | 人类可读名称（如 "黄金ETF连续流入"） |
| `metric_id` | TEXT | ✅ | 作用于哪个 Metric（三段式 ID） |
| `detector_name` | TEXT | ✅ | detector 类型（threshold / percentile / trend / volatility / moving_average） |
| `severity` | Severity | ✅ | critical / warning / info |
| `config` | JSONB | ✅ | detector 特定配置（如 `{consecutive_days: 15, lookback_days: 90}`） |
| `description` | TEXT | ❌ | 说明 |
| `enabled` | BOOLEAN | ✅ | 是否启用 |
| `source` | RuleSource | ✅ | plugin_suggested / user_override / system_default |
| `is_override` | BOOLEAN | ✅ | true = 用户/管理员显式修改过 |
| `version` | INT | ✅ | 单调递增 |
| `effective_from` | TIMESTAMPTZ | ✅ | 版本生效时间 |
| `effective_to` | TIMESTAMPTZ | ❌ | NULL = 当前生效 |

### 4.3 Severity

```
critical — 极端异常，需立即关注
warning  — 显著异常，建议关注
info     — 轻微异常，可选择性关注
```

### 4.4 RuleSource + 优先级

```
优先级（高→低）：
  1. user_override     — 用户/管理员显式修改，升级时不自动覆盖
  2. system_default    — Core 默认兜底
  3. plugin_suggested  — Plugin 建议
```

### 4.5 生命周期

```
SUGGESTED → ACCEPTED → ACTIVE → RETIRED

SUGGESTED: Plugin 提交 RuleSuggestion
ACCEPTED:  Core 的 RuleManager 审核通过，或用户手动创建
ACTIVE:    enabled=true AND effective_to IS NULL
RETIRED:   被新版本取代，或 Plugin 不再建议
```

### 4.6 冲突解决

| 当前 source | Plugin 升级建议 | 决策 |
|------------|---------------|------|
| `plugin_suggested` | 与当前相同 | Skip |
| `plugin_suggested` | 与当前不同 | 自动采用新建议 |
| `user_override` | 与当前相同 | 静默更新建议记录 |
| `user_override` | 与当前不同 | 标记 pending_conflict，通知用户 |
| `system_default` | 任意 | 自动采用新建议 |

### 4.7 关系

- Rule 作用于一个 Metric（N:1）
- Rule 触发多条 Alert（1:N）

### 4.8 约束

- 同一 `(name, metric_id, detector_name)` 同一时刻只有一个生效版本
- `is_override=true` 的 Rule 不会被 Plugin 升级自动覆盖
- Rule 被 RETIRED 时不删除，保留用于历史 Alert 解释

---

## 5. Alert

### 5.1 身份

| 属性 | 说明 |
|------|------|
| ID 格式 | `alt_` + 日期 + 随机串（如 `alt_20260730_abc123`） |

### 5.2 字段

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `id` | TEXT | ✅ | PK |
| `title` | TEXT | ✅ | 异常标题 |
| `summary` | TEXT | ✅ | 异常概要 |
| `severity` | Severity | ✅ | critical / warning / info |
| `status` | AlertStatus | ✅ | active / resolved |
| `metric_id` | TEXT | ✅ | 触发 Metric 的三段式 ID |
| `rule_id` | INT | ✅ | FK → rules_v2，触发时的 Rule |
| `rule_version` | INT | ✅ | 触发时 Rule 的版本号 |
| `rule_effective_from` | TIMESTAMPTZ | ✅ | 触发时 Rule 版本的生效时间 |
| `detector_name` | TEXT | ✅ | detector 类型 |
| `dedup_key` | TEXT | ✅ | hash(metric_id + rule_id + window) |
| `window_start` | TIMESTAMPTZ | ❌ | 检测窗口起点 |
| `window_end` | TIMESTAMPTZ | ❌ | 检测窗口终点 |
| `evidence` | JSONB | ✅ | 检测证据（当前值、历史分位、趋势信息等） |
| `plugin_id` | TEXT | ✅ | FK → plugins |
| `triggered_at` | TIMESTAMPTZ | ✅ | 首次触发时间 |
| `resolved_at` | TIMESTAMPTZ | ❌ | 解除时间 |
| `created_at` | TIMESTAMPTZ | ✅ | 记录创建时间 |
| `updated_at` | TIMESTAMPTZ | ✅ | 最后更新时间 |

### 5.3 AlertStatus

```
active   — 异常仍在持续
resolved — 异常已解除（下一次检测未再次触发）
```

### 5.4 生命周期

```
TRIGGERED → ACTIVE → RESOLVED

TRIGGERED: Detector 首次检测到异常，生成 Alert
ACTIVE:     dedup_key 未冲突，或后续检测仍触发
RESOLVED:  检测不再触发，AlertEngine 自动标记 resolved
```

如果后续检测再次触发已 RESOLVED 的同一条件，生成新 Alert（新的 dedup_key 或新窗口）。

### 5.5 事件

```
DetectionTriggered → AlertEngine.createOrUpdateAlert()
  → 新 Alert: AlertCreated 事件
  → 已有 Alert 升级/降级: AlertUpdated（evidence 更新）
  → 解除: AlertResolved 事件

Alert 生成后 → ResearchAssembler.GetContext() → research_snapshots（冻结点）
```

### 5.6 关系

- Alert 由一条 Rule 触发（N:1）
- Alert 关联一个 Metric（N:1）
- Alert 拥有一条 Research Snapshot（1:1）

### 5.7 约束

- 同一时刻同一 dedup_key 只能有一个 ACTIVE Alert（唯一索引 `WHERE status='active'`）
- `rule_version` 和 `rule_effective_from` 在 Alert 创建时固化，不可变更
- Alert 无法被删除，只能 RESOLVED

---

## 6. 跨对象关系总览

```
Plugin (1) ──────── (N) Entity      ─── 由 Plugin 声明，Core 版本化
Plugin (1) ──────── (N) Metric      ─── 由 Plugin 声明，Core 分配 uid
Plugin (1) ──────── (N) Relation    ─── Plugin 建议，Core 审核并拥有
Plugin (1) ──────── (N) Rule        ─── Plugin 建议，Core 审核并拥有

Entity (1) ──────── (N) Metric      ─── Metric.observed_entity_id
Entity (M) ──────── (M) Entity      ─── Relation (source → target)

Metric (1) ──────── (N) Observation ─── 观测值，纯字符串关联
Metric (1) ──────── (N) Rule        ─── Rule.metric_id
Metric (1) ──────── (N) SourcePreference ─── 多源优先级

Rule (1) ───────── (N) Alert        ─── Alert.rule_id

Alert (1) ──────── (1) ResearchSnapshot ─── 触发时冻结
```

---

## 7. 命名规范汇总

| 类型 | ID 格式 | 示例 | 分配者 |
|------|---------|------|--------|
| Entity | `ent_` + 可读后缀 | `ent_gold_etf_flow` | Plugin |
| Metric | `namespace.entity.metric` | `gold.etf.net_inflow` | Plugin |
| Metric UID | `mtr_` + 随机 | `mtr_abc123def456` | Core |
| Relation | `rel_` + 随机 | `rel_abc789` | Core |
| Rule | `rul_` + 可读后缀 | `rul_gold_trend_001` | Core |
| Alert | `alt_` + 日期 + 随机 | `alt_20260730_abc123` | Core |
| Plugin | `plg_` + 名称 | `plg_etf` | Core |
| Command | `cmd_` + 类型 + 日期 | `cmd_sync_20260730_001` | Core |
