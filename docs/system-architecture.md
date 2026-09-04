# Sonde — 系统架构设计

版本：1.1
状态：冻结（设计决策不变；1.1 仅修订里程碑与文档引用）

> 修订记录：
> - **1.1（2026-07-31）**：里程碑新增 M0（仓库基础设施）；M1 显式纳入 `pkg/pluginrunner` 与两项 proto 待决事项拍板（见 adr.md）；M2 纳入首个真实数据源端到端；M3 增加 snapshot 桩说明；修复 §十三 的 PRD 文件名引用。设计章节零改动。
> - **1.0**：初始冻结稿。

---

## 前置约定

**ID 格式规范：**

| 前缀 | 类型 | 示例 |
|------|------|------|
| `mtr_` | Metric UID | `mtr_abc123def456` |
| `ent_` | Entity ID | `ent_gold_etf_flow` |
| `rel_` | Relation ID | `rel_abc789` |
| `rul_` | Rule ID | `rul_gold_trend_001` |
| `alt_` | Alert ID | `alt_20260730_abc123` |
| `plg_` | Plugin ID | `plg_etf` |
| `cmd_` | Command ID | `cmd_sync_20260730_001` |

Entity/Metric 的人可读 ID（如 `gold_etf_flow`、`gold.etf.net_inflow`）不受前缀约束，由 Plugin 定义。

---

## 一、架构哲学

五条硬约束：

1. **Plugin 是独立进程**：通过 gRPC 与 Core 通信，固定端口
2. **Core 不认识金融实体，但认识通用 Ontology Contract**
3. **Ontology 归 Core 所有**：Plugin 建议，Core 审核/版本化
4. **Observation 与 Ontology 彻底隔离**：Research 时临时 Join
5. **No Silent Data Loss**：must-deliver 事件绝不丢弃

### 1.1 核心澄清：Core 到底认识什么？

> **Core 不认识金融实体，但认识通用 Ontology Contract。**

```
Core 不知道的：
  ✗ "黄金" 是什么
  ✗ "BTC" 和 "ETH" 的区别
  ✗ "北向资金" 的含义

Core 知道的：
  ✓ Relation 有四层分类：structural / semantic / statistical / causal
  ✓ 每种 relation_type 属于哪层（硬编码在 taxonomy registry 中）
  ✓ 每层关系的审核规则：structural 可自动接受，causal 必须 pending
  ✓ Entity 有七种类型：asset / instrument / flow / institution / indicator / index / market
  ✓ 版本号、生效时间、变更日志的语义
  ✓ Metric 有三段式命名规范：namespace.entity.metric

这是一个"认识语法不认识单词"的设计。
就像编译器知道什么是合法的语句，但不知道变量名代表什么业务含义。
```

---

## 1.2 Core 生命周期

```
┌─────────────────────────────────────────────────────────┐
│                    Core 生命周期                         │
│                                                         │
│  ┌──────┐    ┌─────────┐    ┌───────────┐    ┌───────┐ │
│  │ INIT │───→│ BOOTING │───→│ HEALTHY   │───→│ STOP  │ │
│  └──────┘    └────┬────┘    └───┬─┬─────┘    └───────┘ │
│                   │             │ │           ↑         │
│                   ↓             │ │           │         │
│              ┌────────┐        │ │      ┌────┴─────┐   │
│              │ FAILED │        │ └─────→│ DEGRADED │   │
│              └────────┘        │        └────┬─────┘   │
│                   ↑            │             │         │
│                   └────────────┴─────────────┘         │
│                                                         │
│  INIT       进程启动，加载配置，连接 DB                    │
│  BOOTING    等待所有 Plugin 连接并通过注册
             (boot_timeout: 60s)
             - 如果有配置了 required_plugins，则必须全部就绪
             - 如果无 required_plugins 配置，首个注册的 Plugin
               连接后即进入 HEALTHY
             - 超时且有 required_plugins 未就绪 → FAILED
             - 超时且无 required_plugins 也无人注册 → HEALTHY（空集群合法）                │
│             (boot_timeout: 60s)                          │
│  HEALTHY    所有必需 Plugin 在线，Detector 正常运行       │
│  DEGRADED   至少一个 Plugin 异常（未连接/频繁错误）        │
│             但核心检测链路仍在运行                        │
│  FAILED     关键依赖不可用（DB 断开、无 Plugin 在线）      │
│  STOP       收到 SIGTERM，优雅关闭                        │
│                                                         │
│  状态转换：                                              │
│    INIT → BOOTING        配置加载完成                    │
│    BOOTING → HEALTHY     所有 Plugin 注册 ∧ 心跳正常     │
│    BOOTING → FAILED      boot_timeout 超时              │
│    HEALTHY → DEGRADED    某个 Plugin 心跳超时            │
│    HEALTHY → FAILED      DB 连接断开                     │
│    DEGRADED → HEALTHY    Plugin 恢复心跳                 │
│    DEGRADED → FAILED     所有 Plugin 离线                │
│    任意状态 → STOP       SIGTERM                         │
│                                                         │
│  对检测链路的影响：                                       │
│    HEALTHY:   正常检测，正常告警                          │
│    DEGRADED:  正常检测，告警附带 "系统降级" 标记          │
│    FAILED:    停止检测，停止告警                          │
│    其他:      不运行检测                                  │
└─────────────────────────────────────────────────────────┘
```

### 1.3 Plugin 生命周期

```
┌─────────────────────────────────────────────────────────────────┐
│                     Plugin 生命周期                              │
│                                                                 │
│  ┌────────┐   ┌──────────┐   ┌──────────┐   ┌────────────────┐ │
│  │ START  │──→│ REGISTER │──→│ RUNNING  │──→│ SHUTDOWN/STOP  │ │
│  └────────┘   └────┬─────┘   └──┬─┬─────┘   └────────────────┘ │
│                    │             │ │             ↑               │
│                    ↓             │ │             │               │
│               ┌─────────┐       │ │        ┌────┴───────┐       │
│               │ FAILED  │       │ └───────→│  DEGRADED  │       │
│               └─────────┘       │          └────┬───────┘       │
│                    ↑            │               │               │
│                    └────────────┴───────────────┘               │
│                                                                 │
│  START      进程启动，加载配置                                    │
│  REGISTER   连接 Core gRPC，调用 RegisterPlugin()               │
│             - Core 验证声明                                     │
│             - Core 审核 Relation/Rule suggestions               │
│             - Core 分配 plugin_id + registration_version        │
│  RUNNING    注册成功，打开 stream，开始采集循环                   │
│  DEGRADED   采集连续失败超过阈值（consecutive_errors > N）        │
│             仍在尝试采集，但在 heartbeat 中报告状态              │
│  FAILED     注册被拒 / stream 断开且重试耗尽 / 致命错误          │
│  SHUTDOWN   收到 SIGTERM，完成当前采集后关闭 stream，优雅退出    │
│                                                                 │
│  状态转换：                                                      │
│    START → REGISTER      配置加载完成，已连接 Core               │
│    REGISTER → RUNNING    Core 返回 success=true                 │
│    REGISTER → FAILED     Core 返回 success=false                │
│    RUNNING → DEGRADED    连续采集错误 > 阈值                     │
│    DEGRADED → RUNNING    一次成功采集                            │
│    RUNNING → FAILED      stream 断开且重试耗尽                   │
│    任意 → SHUTDOWN       SIGTERM                                │
└─────────────────────────────────────────────────────────────────┘
```

### 1.4 Plugin Upgrade 流程

Plugin 升级是最复杂的生命周期事件。升级不是简单的"停旧启新"，而是涉及声明变更、版本迁移、数据连续性。

```
升级时序：

  Old Plugin (v1.2.0, RUNNING)
    │
    │  ═══ 运维触发升级（docker compose restart / k8s rolling update）═══
    │
    ├─ 收到 SIGTERM
    ├─ 完成当前采集循环
    ├─ 发送 final heartbeat（state=stopping）
    ├─ 关闭 stream
    ├─ 关闭 gRPC 连接
    └─ 退出
                              │
  New Plugin (v1.3.0) 启动    │
    │                         │
    ├─ START                  │
    ├─ REGISTER               │
    │   ├─ 声明 Entity/Metric（可能变更）                 │
    │   ├─ 声明 RelationSuggestion（可能新增/修改/撤回）   │
    │   ├─ 声明 RuleSuggestion（可能新增/修改/撤回）       │
    │   └─ PluginInfo{version: "1.3.0"}                  │
    │                         │
    │   Core 检测到 version 变化                           │
    │   ├─ Entity/Metric: 同样 ID → 创建新 version 行     │
    │   │   INSERT INTO entities_v2 (..., version=2,      │
    │   │     effective_from=NOW(),                       │
    │   │     supersedes=1)                               │
    │   │   UPDATE entities_v2 SET effective_to=NOW()     │
    │   │     WHERE id='gold_etf' AND version=1           │
    │   │                                                 │
    │   ├─ Relation: Plugin 重新 suggestion 所有关系       │
    │   │   - 仍在 suggestion 列表的 → 保持                │
    │   │   - 不在 suggestion 列表的 → Core 标记 retired   │
    │   │   - 新增的 → 按 taxonomy 分层审核               │
    │   │                                                 │
    │   ├─ Rule: Plugin 重新 suggestion 所有规则           │
    │   │   - 仍在 suggestion 列表的 → 保持                │
    │   │   - 不在的 → Core 标记 retired（不是立刻删除）   │
    │   │   - 新增的 → 进入审核                            │
    │   │                                                 │
    │   └─ EventBus.Publish(VersionChanged{               │
    │        plugin: "etf",                               │
    │        old: "1.2.0", new: "1.3.0"})                 │
    │                                                     │
    ├─ RUNNING（继续采集，旧版本数据流无缝接续）              │
    │                                                     │
    │   关键保证：                                          │
    │   ✅ metric_id 不变 → metric_uid 不变                 │
    │      → observations 表不需要迁移                    │
    │   ✅ 采集间隔内完成升级 → 最多丢一个数据点            │
    │   ✅ Backfill 可补回丢失窗口（如果 Plugin 支持）      │
    │   ✅ 旧版本 Alert 的 rule_version 已固化             │
    │      → rule 升级不影响历史 Alert 的可解释性          │
    │                                                     │
    │   Core 的升级保证：                                    │
    │   ✅ Entity/Metric/Relation/Rule 全部版本化           │
    │   ✅ 查询总是用 effective_to IS NULL → 当前版本      │
    │   ✅ 历史版本保留，不会被删除                         │
    │   ✅ Research Snapshot 已固化 → 不受 ontology 变更影响│
    │   ✅ 旧 Rule 标记 retired 而非删除                    │
    │      → 如果将来需要对比（"为什么以前的告警少了？"）    │
    │         retired rules 仍然可查                      │
```

---

## 二、Relation Taxonomy：四层分流

### 2.1 层级定义

Relation 不再是扁平的枚举，而是四层分级。每层有不同的审核策略。

```
Layer 1: structural（结构关系）     → 自动接受为主
  tracks          A 跟踪 B 的价值（ETF 跟踪指数）
  component_of    A 是 B 的组成部分（个股是 ETF 的成分）
  issued_by       A 由 B 发行（ETF 由基金公司发行）
  belongs_to      A 属于 B 的类别/市场（A股属于中国权益市场）

Layer 2: semantic（语义关系）       → 自动接受，但记录为"声明型"
  hedges          A 是对冲 B 的工具
  competes        A 和 B 竞争同一类资金
  signals         A 是 B 的前瞻信号

Layer 3: statistical（统计关系）    → 要求 evidence
  correlates          A 和 B 正相关
  inversely_correlates A 和 B 负相关
  leads               A 领先 B 变化（统计意义上的先行）
  lags                A 滞后 B 变化

Layer 4: causal（因果关系）         → 默认 pending，需人工审核
  causes          A 导致 B 变化
  depends_on_regime A 和 B 的关系依赖市场状态
```

### 2.2 分层审核规则

```go
// RelationManager 的审核决策树

func (m *RelationManager) Review(suggestion RelationSuggestion) RelationReview {
    layer := m.taxonomy.LayerOf(suggestion.RelationType)

    switch layer {
    case "structural":
        // 只检查 source/target 是否都存在
        // 如果都注册了 → auto-accept
        return AutoAccept(suggestion)

    case "semantic":
        // 自动接受，标记 source="plugin_declared"
        // relation.metadata.source = "plugin_declared"（区分于系统推断的）
        return AutoAcceptWithSource(suggestion, "plugin_declared")

    case "statistical":
        // 必须提供 evidence，且通过统计阈值校验
        ev := suggestion.StatisticalEvidence
        if ev == nil {
            return Reject("statistical relation requires StatisticalEvidence")
        }
        if ev.PValue > 0.05 || ev.SampleSize < 30 {
            // 证据不充分 → 转人工审核
            return PendingReview(suggestion)
        }
        return AutoAccept(suggestion) // 通过统计校验则接受

    case "causal":
        // 默认 pending，进入人工审核队列
        // 只有标记了 is_verified_by_human=true 才能转为 accepted
        return PendingReview(suggestion)
    }
}
```

### 2.3 proto 中的变更

```protobuf
message RelationSuggestion {
  // ... 原有字段 ...

  // 关联层级（由 Core.taxonomy.LayerOf(relation_type) 自动推导）
  // Plugin 建议的 layer 仅作参考，Core 以 taxonomy registry 为准
  RelationLayer suggested_layer = 10;  // "structural" | "semantic" | "statistical" | "causal"

  // 新增：统计证据（仅 statistical 层需要）
  StatisticalEvidence statistical_evidence = 11;
}

message StatisticalEvidence {
  string method = 1;          // "pearson", "spearman", "granger", "cointegration"
  int32 window_days = 2;      // 计算窗口
  double coefficient = 3;     // 相关系数或检验统计量
  double p_value = 4;
  int32 sample_size = 5;
}
```

### 2.4 为什么这样分

分层的根本原因不是学术分类，而是**治理可预测性**。

- structural 关系是事实性的（"GLD 跟踪 COMEX 黄金"——这不是观点，这是产品说明书写的事实），不应该被人工审核阻塞
- causal 关系是高度主观的（"美联储加息导致黄金下跌"——这是理论，不是事实），默认不自动接受是对用户负责
- statistical 关系的可复现性要求（带 evidence），确保未来能追溯"这个关系当初是怎么来的"

---

## 三、Metric UID：弱绑定，强审计

### 3.1 问题

`observations.metric_id` 是纯字符串，不做 FK。隔离是对的，但如果 metric 被误拼写、重命名、或同名但在语义版本下含义变更，查询层无法察觉。

### 3.2 方案

保留 `metric_id` 字符串，增加 `metric_uid`——由 Core 在注册时分配，终身不变。

```
metric_id = "gold.etf.net_inflow"     ← 人可读，可变
metric_uid = "mtr_abc123def456"      ← 系统分配，不可变
```

写入时：`PushSnapshots` 用 `metric_id`。Core 收到后通过 `metric_definitions_v2` 查找当前生效的 `metric_uid`，一并存入 `observations`。

```sql
CREATE TABLE observations (
    time                    TIMESTAMPTZ NOT NULL,
    metric_id               TEXT NOT NULL,          -- "gold.etf.net_inflow"
    metric_uid              TEXT NOT NULL,          -- "mtr_abc123def456"
    value                   DOUBLE PRECISION NOT NULL,
    labels                  JSONB DEFAULT '{}',
    labels_hash             TEXT NOT NULL DEFAULT '', -- md5(sorted labels)

    source_plugin           TEXT NOT NULL,
    source_plugin_version   TEXT NOT NULL,
    source_provider         TEXT NOT NULL,
    source_fetched_at       TIMESTAMPTZ NOT NULL,

    quality_grade           TEXT DEFAULT 'delayed',
    quality_confidence      FLOAT DEFAULT 0.8,
    system_quality_score    FLOAT DEFAULT NULL,

    ingested_at             TIMESTAMPTZ DEFAULT NOW()
);

SELECT create_hypertable('observations', 'time');
CREATE INDEX idx_obs_metric_uid_time ON observations(metric_uid, time DESC);
CREATE INDEX idx_obs_source ON observations(source_plugin, source_provider);

-- 未注册 metric 的暂存表
CREATE TABLE pending_metrics (
    metric_id       TEXT PRIMARY KEY,
    first_seen_at   TIMESTAMPTZ DEFAULT NOW(),
    first_seen_from_plugin TEXT NOT NULL,
    status          TEXT DEFAULT 'unknown_source'  -- unknown_source | registered
);
-- Plugin 注册后，对应的 pending_metrics 行标记为 registered

-- 幂等索引
CREATE UNIQUE INDEX idx_obs_idempotency 
ON observations(metric_uid, time, source_plugin, source_provider, labels_hash);
```

### 3.3 数据迁移

如果 metric 被重命名：

```
1. Plugin 升级 → RegisterPlugin 声明
   metric_id = "gold.etf_flow.net_inflow"（改名了）
   supersedes = "gold.etf.net_inflow"

2. Core 检测到 supersedes
   → 新 metric_id 沿用旧 metric_uid
   → 旧 metric_id 标记 deprecated
   → 所有 Observation 的 metric_uid 不变，metric_id 不需要回填

3. 查询层
   → 查最新 metric_id 用 metric_definitions_v2
   → 查历史数据用 metric_uid（不受改名影响）
```

**不是 FK，是弱绑定 ID。** 不强约束引用完整性，但提供审计链路。

### 3.4 Observation 写入幂等性

Plugin 重连、stream 重试、backfill 补数时，同一份数据可能被多次 Push。必须保证 observation 层面不重复写入。

**幂等键：**

```
(metric_uid, time, source_plugin, source_provider, labels_hash)
```

`labels_hash` = md5(sorted(k1=v1&k2=v2...))，用于区分同一 metric 的不同维度（如 `symbol=AAPL` vs `symbol=MSFT`）。

**写入策略（统一为条件更新）：**

在 `MetricService.BatchInsert` 中固化以下 SQL。不做两套语句，统一用条件 `DO UPDATE`：

```sql
INSERT INTO observations (
    metric_id, metric_uid, time, value, source_plugin, 
    source_plugin_version, source_provider, source_fetched_at, 
    quality_grade, quality_confidence, labels_hash, labels
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
ON CONFLICT (metric_uid, time, source_plugin, source_provider, labels_hash) 
DO UPDATE SET 
    value = EXCLUDED.value,
    quality_grade = EXCLUDED.quality_grade,
    quality_confidence = EXCLUDED.quality_confidence,
    source_fetched_at = EXCLUDED.source_fetched_at,
    ingested_at = NOW()
WHERE EXCLUDED.quality_grade = 'revised' 
  AND observations.quality_grade != 'revised';
```

**语义：**

| 新数据 grade | 已有数据 grade | 行为 |
|-------------|---------------|------|
| 任意（相同数据） | 任意（相同数据） | 跳过（幂等保护） |
| `revised` | `preliminary` | 覆盖 |
| `revised` | `estimated` | 覆盖 |
| `revised` | `delayed` | 覆盖 |
| `revised` | `revised` | 跳过（不重复覆盖） |
| `delayed` | `realtime` | 跳过（realtime 优先于 delayed） |
| `delayed` | `estimated` | **覆盖**（delayed 优先级 3 > estimated 优先级 2，incoming rank > existing rank → UPDATE） |
| `preliminary` | 任意 | 跳过（preliminary 不覆盖任何已有数据） |
| backfill 补充历史空白 | — | INSERT 新行 |

**quality_grade 的优先级：** `realtime > revised > delayed > estimated > preliminary`

只有 `revised` 可以降级覆盖已有的 `delayed`/`estimated`/`preliminary`。
`realtime` 数据一旦写入，不再被任何非 `revised` 覆盖。
这样修正链路清晰：原始估计 → 延迟数据 → 正式修正，每一步都有明确的可覆盖规则。

**PushSnapshots 返回值增强：**

```protobuf
message PushSnapshotsResponse {
  bool success = 1;
  int32 inserted = 2;     // 新写入条数
  int32 deduplicated = 3; // 因幂等性跳过的条数
  int32 rejected = 4;     // 验证失败的条数
  string message = 5;
}
```

Plugin 可以根据 `deduplicated` 判断是否在重复发送，调整重试策略。

---

## 四、Event Bus：分级交付

### 4.1 问题

Channel-based Event Bus 会因 consumer 慢而丢弃事件。`MetricUpdated` 或 `DetectionTriggered` 被丢弃意味着检测不完整或 alert 漏报。

### 4.2 分级策略

```
Tier 1: must-deliver（不可丢）
  MetricUpdated
  DetectionTriggered
  DetectionCleared
  AlertCreated
  AlertResolved

Tier 2: best-effort（可丢，可从 DB 恢复）
  SnapshotsReceived
  PluginConnected
  PluginDisconnected
  PluginDegraded
  RelationChanged
  RuleChanged
  VersionChanged
```

### 4.3 MVP 实现：Transactional Outbox



如果采集写入在 gRPC handler 里同步阻塞等待 Detector 计算完成（PercentileDetector 要扫全窗口），会导致：
- Plugin 的 gRPC Stream 因等待 ACK 而积压 → 触发客户端超时 → Plugin 重试 → 数据重复推送 → 恶性循环
- 高频采集时，一个慢 Metric 拖垮所有其他 Plugin 的数据接收

**方案：数据写入与检测计算彻底解耦。PushSnapshots 只做"写入 + Outbox 事件"，同一事务，立即返回 ACK。**

```
[Plugin Push] ──→ [DB Tx: Write Observations + Write Outbox] ──→ [Return gRPC ACK]（极快）
                                   │
                          (Async Worker Poll)
                                   │
                                   ▼
                        [Detector Engine Run]
```

```go
func (pm *PluginManager) handlePushSnapshots(req *PushSnapshotsRequest) (*pb.PushSnapshotsResponse, error) {
    // 1. 单事务：写入 observations + outbox 事件
    tx, _ := pm.db.Begin(ctx)
    defer tx.Rollback()

    metricIDs, err := pm.metricService.BatchInsertTx(tx, snapshots)
    if err != nil {
        return err
    }

    for _, mid := range uniqueMetricIDs(metricIDs) {
        // 写入 outbox（同一事务）
        pm.outbox.EnqueueTx(tx, MetricUpdated{MetricID: mid, Timestamp: time.Now()})
    }

    tx.Commit()  // observations + outbox 原子提交

    // 2. 立即返回 ACK，不等待检测
    return nil
}

// Detector 通过 MetricService 查询历史观测数据
// 访问方式：MetricService.QueryHistory(metric_uid, window) → []DataPoint
// 底层 SQL: SELECT time, value FROM observations WHERE metric_uid = $1 AND time > $2
//            AND source_plugin = preferred ORDER BY time
//  使用 TimescaleDB hypertable 的时间范围扫描
func (w *DetectorWorker) Run(ctx context.Context) {
    ticker := time.NewTicker(5 * time.Second)
    for {
        select {
        case <-ticker.C:
            events := w.outbox.Poll("pending", 100)
            for _, e := range events {
                w.outbox.MarkProcessing(e.ID)
                w.detectorEngine.HandleMetricUpdated(e.Payload)
                w.outbox.MarkDone(e.ID)
            }
        case <-ctx.Done():
            return
        }
    }
}
```

**为什么是 `BatchInsert` + Outbox 同事务？**

若先写 observations 成功、再写 outbox 失败（或进程崩溃），Metric 会丢失检测。同一事务保证"数据入库 ←→ 一定会被检测"的原子性。

**Tier 1 事件全部通过 Transactional Outbox 传递，不经过 in-process channel。**

`PublishAsync` 保留用于 Tier 2 事件（PluginConnected、SnapshotsReceived 等），它们丢得起且不需要事务保证。

```sql
CREATE TABLE event_outbox (
    id              SERIAL PRIMARY KEY,
    event_type      TEXT NOT NULL,           -- "MetricUpdated"
    payload         JSONB NOT NULL,          -- {metric_id: string, metric_uid: string, timestamp: int64}
    status          TEXT DEFAULT 'pending',   -- pending | processing | done | failed
    attempts        INT DEFAULT 0,
    created_at      TIMESTAMPTZ DEFAULT NOW()
);

-- Worker 轮询: SELECT * FROM event_outbox WHERE status='pending' ORDER BY id LIMIT 100 FOR UPDATE SKIP LOCKED
```

Scheduler 也定期扫描 stuck outbox（status=processing 超过 5 分钟 → 重置为 pending）。超过 5 次 failed 标记 done_with_error。

---

### 4.4 Tier 2 事件（保留 channel）

```go
type Bus struct {
    mu        sync.RWMutex
    asyncSubs map[string][]chan Event   // Tier 2 only: drop ok
}

func (b *Bus) PublishAsync(e Event) {
    for _, ch := range b.asyncSubs[e.EventType()] {
        select {
        case ch <- e:
        default: // drop
        }
    }
}
```

---

## 五、控制面：单长连接 + Bidirectional Stream

### 5.1 问题

Plugin 开随机端口 + Core 回连的模式在 Docker 环境下存在端口变更、连接缓存、NAT 穿透等问题。

### 5.2 方案：去掉独立 PluginCommand 服务，改为同一 gRPC 连接上的 Bidirectional Stream

```
┌──────┐                          ┌──────┐
│Plugin├── gRPC stream ──────────→│ Core │
│      │←─ gRPC stream ──────────┤      │
└──────┘                          └──────┘
    同一条 TCP 连接，双向流
```

```protobuf
service PluginHost {
  // 注册（unary）
  rpc RegisterPlugin(RegisterPluginRequest) returns (RegisterPluginResponse);

  // 数据推送 + 命令下发（bidirectional streaming）
  // Plugin → Core: PushSnapshots, Heartbeat
  // Core → Plugin: SyncCommand, BackfillCommand
  rpc MaintainSession(stream PluginMessage) returns (stream CoreMessage);

  // 心跳（轻量 unary，作为 stream 的 fallback）
  rpc Heartbeat(HeartbeatRequest) returns (HeartbeatResponse);
}

message PluginMessage {
  oneof payload {
    PushSnapshotsRequest push_snapshots = 1;
    HeartbeatRequest heartbeat = 2;
  }
}

message CoreMessage {
  oneof payload {
    SyncCommand sync = 1;
    BackfillCommand backfill = 2;
  }
}

message SyncCommand {
  string command_id = 1;   // 用于 Plugin 回复时关联
  string reason = 2;
  repeated string metric_ids = 3;
}

message BackfillCommand {
  string command_id = 1;
  string reason = 2;
  int64 window_start = 3;
  int64 window_end = 4;
  repeated string metric_ids = 5;
}
```

**优势：**
- 不需要 Plugin 开监听端口，不需要 Core 回连
- Docker 网络只需 Core 暴露 9090，Plugin 只需知道 `core:9090`
- 天然解决 NAT、防火墙、端口变更、连接缓存问题
- gRPC stream 自带流控和重连

**Plugin 端行为：**
- 注册后立即打开 `MaintainSession` stream
- 持续通过 stream 发 PluginMessage（PushSnapshots / Heartbeat / CommandAck）
- 持续从 stream 读 CoreMessage，收到命令后执行并回复

### 5.3 gRPC Stream 并发安全：Session Writer Loop



Go gRPC 的 `stream.Send()` 内部有互斥锁缺失——如果 Core 内部多个 Goroutine 同时调用 `stream.Send()`（例如 Heartbeat 响应 Goroutine 和 Command 下发 Goroutine 并发），程序会发生 Data Race 并 `panic: concurrent write to stream`。

**方案：每个 Session 维护一个专属的单写 Goroutine + channel。所有下发消息统一入队，由该 Goroutine 串行写入 Stream。**

```go
type StreamSession struct {
    stream pb.PluginHost_MaintainSessionServer
    sendCh chan *pb.CoreMessage  // 所有 Core → Plugin 的消息统一入队
    ctx    context.Context
    cancel context.CancelFunc
}

func (s *StreamSession) StartWriter() {
    for {
        select {
        case msg := <-s.sendCh:
            if err := s.stream.Send(msg); err != nil {
                // Stream 断开，通知 PluginManager 清理 session
                s.cancel()
                return
            }
        case <-s.ctx.Done():
            return
        }
    }
}

// 任何需要发消息的地方，只调用 SendAsync（非阻塞入队）：
func (s *StreamSession) SendAsync(msg *pb.CoreMessage) error {
    select {
    case s.sendCh <- msg:
        return nil
    case <-s.ctx.Done():
        return errors.New("session closed")
    default:
        // channel 满 → 意味着消费端慢或阻塞，记录 metric 后丢弃
        metrics.IncSessionSendDropped(s.sessionID)
        return errors.New("session send buffer full")
    }
}
```

**关键约束：**
- 任何时候不允许直接调用 `s.stream.Send()`，必须走 `s.sendCh`
- Plugin 端也遵循相同范式（Plugin 端的 Stream 写 Goroutine 单写）
- `sendCh` buffer 大小建议 64——够用但不会堆积过多消息

### 5.4 Command 生命周期

Core 下发的每个命令都有明确的生命周期，Plugin 通过同一条 stream 回执状态。

```
命令状态机：
  COMMAND_ACCEPTED → COMMAND_RUNNING → COMMAND_COMPLETED
                                     → COMMAND_FAILED

Plugin 收到命令后的行为：
  1. 立即发送 CommandAck{status=ACCEPTED}
     → Core 写入 command_log，UI 显示"已接收"
  2. 开始执行，发送 CommandAck{status=RUNNING}
     → Core 更新 command_log，UI 显示"执行中"
  3. 执行完成，发送 CommandAck{status=COMPLETED, result=...} 或
     执行失败，发送 CommandAck{status=FAILED, error=...}
     → Core 更新 command_log，UI 显示结果
```

**Proto 扩展：**

```protobuf
message PluginMessage {
  oneof payload {
    PushSnapshotsRequest push_snapshots = 1;
    HeartbeatRequest heartbeat = 2;
    CommandAck command_ack = 3;        // 新增
  }
}

message CommandAck {
  string command_id = 1;
  string status = 2;          // "accepted" | "running" | "completed" | "failed"
  string message = 3;         // 人类可读的状态描述
  int64 timestamp = 4;        // unix seconds
  // 仅 completed 时有值
  int32 collected_count = 5;
  // 仅 failed 时有值
  string error = 6;
}
```

**Core 端存储：**

```sql
CREATE TABLE command_log (
    command_id    TEXT PRIMARY KEY,
    command_type  TEXT NOT NULL,         -- "sync" | "backfill"
    target_plugin TEXT NOT NULL,
    requested_by  TEXT NOT NULL,         -- "system" | "user:{user_id}"
    status        TEXT DEFAULT 'pending', -- pending|accepted|running|completed|failed
    reason        TEXT,
    metric_ids    JSONB DEFAULT '[]',
    window_start  TIMESTAMPTZ,
    window_end    TIMESTAMPTZ,
    collected_count  INT,
    error         TEXT,
    requested_at  TIMESTAMPTZ DEFAULT NOW(),
    accepted_at   TIMESTAMPTZ,
    completed_at  TIMESTAMPTZ
);
```

**超时处理：**

Core 发命令后 60 秒内未收到任何 ack → 标记 `status=timeout`。Plugin 端收到命令后如果超过 5 分钟未完成 → Core 端可以通过 heartbeat 的 `has_pending_command` 提示检查。

**重试语义：**

- `SyncCommand`：不幂等——每次 sync 都是"再采集一次"
- `BackfillCommand`：幂等——同一 window + 同一 metric_ids，重试不产生重复数据（靠 observation 层的 `idx_obs_idempotency` 保证）

---

## 六、数据质量：双层评估

### 6.1 Plugin 自评 vs Core 系统评估

```
DataQuality {
  grade:        Plugin 自评（"realtime", "delayed", "estimated"）
  confidence:   Plugin 自评（0.0-1.0）
}

插入 Observation 时，Core 追加 system_quality_score：
  - 基于 source_preference 的优先级
  - 基于 source_provider 的历史延迟、缺失率
  - 基于 quality_grade 的降级：realtime=1.0, delayed=0.9, estimated=0.5, revised=0.7
```

```sql
-- system_quality_score is computed by Core during BatchInsert
-- (column defined in observations DDL, Section 三)
```

Plugin 说 0.95，系统综合评估可能给出 0.82。

**system_quality_score 计算公式：**

```
system_quality_score = base_score × provider_factor × freshness_factor

base_score:
  realtime = 1.00
  revised  = 0.90
  delayed  = 0.85
  estimated = 0.50
  preliminary = 0.30

provider_factor:
  基于 source_provider 最近 30 天的数据完整率（缺失天数/30）
  完整率 100% → 1.00, 90% → 0.90, <50% → 0.50

freshness_factor:
  min(1.0, 1.0 - max(0, (now - source_fetched_at - expected_lag) / expected_lag * 0.3))
  数据越新鲜分数越高，延迟越久衰减越重
```——Research 页面展示系统分，低分数据加视觉标记。

---

## 七、Source Preferences 增强

```sql
CREATE TABLE source_preferences (
    metric_id          TEXT NOT NULL,
    source_plugin      TEXT NOT NULL,
    source_provider    TEXT NOT NULL,
    priority           INT NOT NULL DEFAULT 0,
    effective_from     TIMESTAMPTZ NOT NULL DEFAULT NOW(),  -- 新增
    is_manual_override BOOLEAN DEFAULT FALSE,               -- 新增
    reason             TEXT,
    set_by             TEXT DEFAULT 'system',
    created_at         TIMESTAMPTZ DEFAULT NOW(),
    updated_at         TIMESTAMPTZ DEFAULT NOW(),

    PRIMARY KEY (metric_id, source_plugin, source_provider)
);
```

`is_manual_override=true` 的行不会被系统自动更新/覆盖，只有用户显式改。

---

## 八、Alert 绑定 Rule 版本

```sql
CREATE TABLE alerts (
    -- ... 原有字段 ...
    rule_id         INT,
    rule_version    INT NOT NULL DEFAULT 1,         -- 新增：触发时的 rule 版本
    rule_effective_from TIMESTAMPTZ,                 -- 新增：触发时 rule 生效时间
    -- ...
);
```

Alert 生成时，从 `rules_v2` 读取当前生效的 version + effective_from 并固化。以后 rule 改了，历史 alert 仍能准确解释"当时按什么规则触发的"。

---

## 九、Research：统一为 Snapshot 模式

### 9.1 为什么去掉 Historical Replay

早期的设计中考虑过两种模式：
- Live：用当前 ontology 解释历史 alert
- Replay：按触发时的 ontology 还原当时的解释

但两种模式哲学冲突，且需要维护两套代码路径。

### 9.2 Snapshot 模式（唯一方案）

**Alert 触发时，系统立刻冻结 ResearchContext 并持久化。**

语义很明确：**"系统在当时是怎么理解这次异常的"。**

```
Alert 触发 →
  1. 生成 Alert（已有）
  2. 立刻调用 ResearchAssembler.GetContext() ← 用当前生效的 ontology
  3. INSERT INTO research_snapshots ← 冻结点
  4. 后续任何对这条 Alert 的查询，永远返回 snapshot
```

用户看到的永远是 Alert 发生那一刻系统的理解。Ontology 后来怎么变，都不影响这条历史 Alert 的解释。

**为什么比 Replay 好：**
- 不需要保留 point-in-time 查询能力（省掉 entity/relation 的历史版本查询接口）
- 不需要"回到那个时间点重新算"的计算成本
- 语义清晰无歧义："这就是系统当时的判断"
- Snapshot 可以被手动触发覆盖（"用最新 ontology 重新解释"），但默认只读

### 9.3 实现

```sql
CREATE TABLE research_snapshots (
    alert_id              TEXT PRIMARY KEY REFERENCES alerts(id),
    context               JSONB NOT NULL,       -- 冻结的完整 ResearchContext
    ontology_frozen_at    TIMESTAMPTZ NOT NULL,  -- 冻结时刻
    created_at            TIMESTAMPTZ DEFAULT NOW()
);
```

```go
//  DetectionTriggered vs AlertCreated 关系：
//   - DetectionTriggered 总是发布（即使被去重，evidence 会更新）
//   - AlertCreated 仅在真正新生成 Alert 时发布
//   - AlertUpdated 在 severity 升降级或 evidence 更新时发布
//   - 订阅者可根据需要选择监听级别

// Alert 生成后紧跟着冻结 ResearchContext
func (ae *AlertEngine) handleDetectionTriggered(detection DetectionTriggered) {
    alert, isNew := ae.createOrUpdateAlert(detection)
    
    if isNew {
        // 新 Alert → 冻结 ResearchContext
        ctx, err := ae.researchAssembler.GetContext(alert.ID)
        if err == nil {
            ae.store.FreezeResearchSnapshot(alert.ID, ctx)
        }
    } else {
        // 已有 Alert（severity 升级/降级）→ 可选更新 snapshot
        // MVP 不更新，保持首次冻结版本
    }
}
```

Research API 直接读 snapshot：

```
GET /api/v1/research/:alert_id
  → SELECT context FROM research_snapshots WHERE alert_id = $1
  → 返回 JSON
```

### 9.4 手动覆盖

未来提供"用最新 ontology 重新解释"按钮，用户手动触发时覆盖 snapshot。MVP 不做。

---

## 十、Rule 所有权

### 10.1 原则

> **Rule 和 Relation 一样：Plugin 永远只能 Suggest，Core 永远拥有。**

Plugin 不能"定义"Rule，只能"建议"Rule。所有 Rule 的最终形态由 Core 的 RuleManager 决定。

### 10.2 三方博弈模型

Rule 的值可能来自三个源头，优先级为：

```
规则优先级（高→低）：
  1. 用户手动覆盖（user_override = true）  ← 最高优先，永不自动覆盖
  2. Core 默认值（system_default）         ← 未被覆盖时的兜底
  3. Plugin 建议值（plugin_suggested）     ← 最低优先，仅作建议
```

### 10.3 冲突解决场景

```
场景：gold.etf.net_inflow 的 TrendDetector 配置

  Plugin v1.0.0 建议:
    {consecutive_days: 15, lookback_days: 90}

  → Core 接受 → 写入 rules_v2 {version=1, source="plugin_suggested"}

  ── 用户手动修改 ──
  用户在 UI 上改成 {consecutive_days: 10}

  → RuleManager.UpdateRule(override=true)
  → 写入 rules_v2 {version=2, source="user_override", supersedes=1}

  ── Plugin 升级到 v1.1.0 ──
  Plugin 重新建议: {consecutive_days: 20, lookback_days: 120}

  → RuleManager.Review()
  → 发现：该 Rule 被 user_override 占用
  → 决策：插入新 suggestion（version=3, source="plugin_suggested"）
          status = "pending_conflict"
          → UI 提示用户："Plugin 建议将连续天数从 10 改为 20，是否采用？"

  → 但如果用户没有覆盖（没有 user_override）
  → RuleManager 自动采用 Plugin 新建议：version=3, source="plugin_suggested"
```

### 10.4 决策表

| 当前 Rule 来源 | Plugin 新建议 | 决策 |
|---------------|---------------|------|
| `plugin_suggested` (v1) | 与当前一致 | Skip（无变化） |
| `plugin_suggested` (v1) | 与当前不同 | **自动采用**新建议（v2） |
| `user_override` | 与当前一致 | 标记 suggested 为 accepted（无声更新） |
| `user_override` | 与当前不同 | **冲突**：标记 suggested 为 pending_conflict，UI 通知 |
| `system_default` | 任意 | **自动采用** |

### 10.5 Rule 表扩展

```sql
CREATE TABLE rules_v2 (
    -- ... 原有字段 ...
    source       TEXT NOT NULL DEFAULT 'plugin_suggested',  -- "plugin_suggested" | "user_override" | "system_default"
    is_override  BOOLEAN DEFAULT FALSE,  -- true = 用户/管理员明确改过，升级时不自动覆盖
    -- ...
);
```

`is_override` 和 `source='user_override'` 是冗余的——前者是快速查询标记。只有用户/管理员通过 UI/API 显式修改才会设为 true。

---

## 十一、实现里程碑

### 11.0 M0: Repository Bootstrap
```
目标：让 M1 可以开工——工具链与基础设施就位
产出：deployments/docker-compose.yml（PostgreSQL 16 + TimescaleDB 2.16）、
     init.sql + migrations/001_baseline（DDL 来自 database-schema.md，
     迁移工具在 golang-migrate / atlas 中定一并记入 ADR）、
     buf 安装与首次 buf generate（生成 pkg/proto）、
     CI 骨架（gofmt / go build / go vet / go test / buf lint）
估时：1 天
```

### 11.1 M1: Plugin Registration Path
```
目标：一个 Plugin 能启动、连 Core、注册成功，Entity/Metric/RelationSuggestion/RuleSuggestion 入库
产出：plugin.proto 定稿（含 adr.md 两项待决事项的拍板：push ack 通路、ContextTemplates 取舍）、
     Core PluginManager、pkg/pluginrunner（插件侧 stream 会话模板，单写循环）、
     registrations 写入 DB、单元测试
估时：2-3 天
```

### 11.2 M2: Observation Ingest Path
```
目标：PushSnapshots 写入 observations、source_preferences 查询 primary source、
     首个真实数据源跑通（成功标准 #1 的 owner）
产出：MetricService.BatchInsert、source preference 解析、时序查询接口、
     任一插件接入一个真实 provider，一条真实 metric 从采集到入库端到端
     （验证幂等键、quality_grade 覆盖矩阵在真实数据下成立）
估时：2-3 天
```

### 11.3 M3: Rule Evaluation Path
```
目标：MetricUpdated → Rule lookup → Detector → Alert 生成
产出：Detector Engine（实现顺序：outbox worker + threshold 先行，percentile / trend 随后）、
     Alert Engine（含去重）、outbox 兜底
注：M4 的 ResearchAssembler 就绪前，AlertEngine 以桩实现冻结空 snapshot，
   M4 补全——alert ↔ snapshot 1:1 不变量的例外仅限开发期
估时：3-4 天
```

### 11.4 M4: Research Read Path
```
目标：alert → metric_uid → current entity → relation graph → ResearchContext JSON
产出：ResearchAssembler（Live Mode）、relation graph 遍历、REST API
估时：2-3 天
```

### 11.5 M5: Control + Frontend
```
目标：Capital Radar 首页、Alert 详情页、手动 Sync/Backfill API
产出：API Server handler、Next.js 两个页面 + ECharts
估时：2-3 天
```

### 11.6 优先级

```
M0 → M1 → M2 → M3 → M4 → M5
```

每个里程碑完成后立即集成测试，不堆积。

---

## 十二、技术栈定案

| 层 | 技术 | 理由 |
|----|------|------|
| Core | Go 1.22+ | 并发、性能、静态部署 |
| Plugin | Go（MVP），gRPC 天然多语言 | 第一个 Plugin 用 Go 降低启动成本 |
| Plugin ↔ Core | gRPC Bidirectional Stream | 单连接双向，无回连复杂性 |
| Database | PostgreSQL 16 + TimescaleDB 2.16 | 关系 + 时序一体 |
| Proto | Buf | 更好的 protobuf 工具链 |
| Frontend | Next.js 14 + React + Tailwind + ECharts | 最简两个页面 |
| 部署 | Docker Compose | 单机一键启动 |
| 热重载 | air（Go）+ HMR（Next.js） | 开发体验 |

---

## 十三、目录结构

```
sonde/
├── cmd/
│   ├── core/main.go
│   └── api/main.go
├── internal/
│   ├── core/
│   │   ├── eventbus/        # Tier 2 async events only (Tier 1 uses outbox)
│   │   ├── pluginmgr/       # gRPC server + stream handler
│   │   ├── relationmgr/     # 四层 taxonomy + review
│   │   ├── rulemgr/         # Rule review + versioning
│   │   ├── metric/          # Observation ingest + source preference
│   │   ├── ontology/        # Entity/Relation query (current + point-in-time)
│   │   ├── detector/        # 订阅 MetricUpdated → 执行 Rules → 发事件
│   │   ├── alert/           # 订阅 DetectionTriggered → 生成/去重/生命周期
│   │   ├── scheduler/       # cron + outbox retry
│   │   └── research/        # Snapshot mode assembler
│   └── api/
│       ├── handler/
│       └── middleware/
├── pkg/
│   ├── proto/plugin/v1/
│   ├── model/
│   └── pluginrunner/        # Stream session 管理模板
├── proto/plugin/v1/plugin.proto
├── plugins/
│   ├── etf/
│   ├── crypto/
│   └── macro/
├── web/
├── deployments/
│   ├── docker-compose.yml
│   ├── Dockerfile.core
│   ├── Dockerfile.api
│   ├── Dockerfile.plugin
│   └── init.sql
└── docs/
    ├── prd-v4.md
    ├── system-architecture.md   # 本文档
    ├── domain-model.md
    ├── plugin-protocol.md
    ├── database-schema.md
    ├── conventions.md
    ├── adr.md
    └── archive/                 # 历史版本（v0 架构、PRD v1-v3 等）
```
