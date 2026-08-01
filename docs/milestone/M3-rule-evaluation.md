# M3: 规则评估与告警生成

**里程碑：M3 — Rule Evaluation Path**
**版本：1.0**
**状态：Draft**
**关联文档：** [prd-v4.md](../prd-v4.md) · [system-architecture.md](../system-architecture.md) · [domain-model.md](../domain-model.md) · [adr.md](../adr.md)

---

## 一、目标

M3 的核心目标是建立从 Observation 写入到 Alert 生成的**检测链路**，实现 Metric 数据的自动异常发现和告警输出。具体包括：

1. **Outbox Worker**：在Observation 同一事务写入后，异步 Worker 轮询 `event_outbox` 触发检测，确保"数据入库必被检测"（ADR-5）。
2. **Threshold Detector**：实现阈值类检测逻辑（高于/低于/超出区间），作为 MVP 首个 Detector。
3. **Alert Engine**：接收检测结果，通过部分唯一索引（`WHERE status='active'`）实现告警去重，生成 / 更新 / 解除 Alert。
4. **事件流**：`MetricUpdated → DetectionTriggered → AlertCreated/AlertUpdated/AlertResolved` 的 Tier 1 事件链路闭环。

M3 依赖 M2 的 observations 写入能力；M3 不负责 Research Context 冻结（M4 补齐）。Alert ↔ Snapshot 的 1:1 不变量在 M3 中由 AlertEngine 调用 ResearchAssembler 桩实现（M4 替换为完整实现）。

---

## 二、用户故事

### 故事 1：阈值告警触发
> 作为系统，当一个 Metric 的值超过预设阈值时，我应自动生成一条 Critical/Warning/Info 级别的 Alert，并在后续检测中如果值恢复正常则自动解除。

**验收条件：**
- `rules_v2` 中配置了 `detector_name='threshold'`, `config={op: '>', value: 100000}` 的规则
- 当 observations 中有 value > 100000 写入时，outbox worker 触发检测
- 生成 `alerts` 行：`status='active'`, `dedup_key=hash(metric_id + rule_id + window)`
- 下一次检测未再触发 → `status='resolved'`, `resolved_at=NOW()`

### 故事 2：告警去重
> 作为系统，在同一检测窗口内同一 Rule 对同一 Metric 的重复触发不应产生多条活跃 Alert。

**验收条件：**
- 连续 5 次 observations 写入均触发同一规则
- `alerts` 表中对同一 `dedup_key` 只有 1 行 `status='active'`
- 该行的 `evidence` 更新为最新检测证据（`updated_at` 变更）
- `idx_alerts_active_dedup` 部分唯一索引生效，数据库层杜绝重复

### 故事 3：Outbox 兜底恢复
> 作为系统，当因进程崩溃导致 outbox 事件处理中断，恢复后不应遗漏任何 Metric 检测。

**验收条件：**
- 进程崩溃重启后，Scheduler 扫描 `status='pending'` 或 `status='processing'` 超过 5 分钟的 outbox 事件
- 将这些事件重新置为 `pending`，Worker 继续处理
- 同一事件处理超过 5 次仍失败 → 标记 `status='failed'`，不阻塞后续事件（`FOR UPDATE SKIP LOCKED`）

---

## 三、领域模型变化

M3 首次写入两个已冻结的领域对象，并引入 Detector 运行时概念：

### 3.1 Rule（运行时激活）

M1 已写入 `rules_v2` 的定义。M3 在检测时读取当前生效的规则（`WHERE effective_to IS NULL AND enabled = TRUE`），按 `detector_name` 分发到对应 Detector 实例。

### 3.2 Alert（首次写入）

| 关键字段 | 说明 |
|---------|------|
| `id` | `alt_` + 日期 + 随机串 |
| `dedup_key` | `hash(metric_id + rule_id + window_start)`，去重关键 |
| `status` | `active → resolved`，不删除 |
| `rule_version` | 触发时 Rule 的版本号（固化，不随 Rule 更新而变） |
| `evidence` | JSONB 格式检测证据快照 |

### 3.3 Detector（新增运行时对象，非持久化）

Detector 不是领域对象表，而是 Core 内的运行时组件：

```go
type Detector interface {
    Name() string
    Detect(metricUID string, config json.RawMessage, window DetectionWindow) (*DetectionResult, error)
}

type DetectionResult struct {
    Triggered   bool
    Severity    string
    Evidence    map[string]any
    WindowStart time.Time
    WindowEnd   time.Time
}
```

MVP 优先级：**threshold → percentile → trend**（M3 至少实现 threshold，其余可在 M3 后续迭代中完成）。

---

## 四、数据流

### 4.1 检测链路全流（含 M2 的 Outbox 写入起点）

```
M2 阶段已完成:
  Plugin PushSnapshots → DB Tx {INSERT observations + INSERT event_outbox} → ACK

M3 阶段开始:
  event_outbox (status='pending')
       │
       │  ◄── Worker 每 5s 轮询: SELECT * FROM event_outbox
       │       WHERE status='pending' ORDER BY id LIMIT 100 FOR UPDATE SKIP LOCKED
       │
       ▼
  Outbox Worker (per event)
       │
       ├─ 读取 payload: {metric_id, metric_uid, timestamp}
       ├─ 从 rules_v2 查询该 metric_id 关联的生效规则
       │   SELECT * FROM rules_v2 WHERE metric_id=$1 AND enabled=TRUE AND effective_to IS NULL
       │
       ├─ 遍历每条规则，分发到 Detector:
       │   ├─ 'threshold' → ThresholdDetector.Detect()
       │   ├─ 'percentile' → PercentileDetector.Detect() (M3 后续迭代)
       │   └─ 'trend' → TrendDetector.Detect() (M3 后续迭代)
       │
       ├─ DetectionResult.Triggered == true → 发布 DetectionTriggered 事件
       │
       └─ 更新 outbox event: status='done' (或 'failed')

  DetectionTriggered 事件
       │
       ▼
  AlertEngine.HandleDetectionTriggered()
       │
       ├─ 计算 dedup_key = hash(metric_id + rule_id + window)
       │
       ├─ 查找已有 Alert:
       │   SELECT * FROM alerts WHERE dedup_key=$1 AND status='active'
       │
       ├─ 不存在 → 创建新 Alert:
       │   INSERT INTO alerts (..., status='active', dedup_key=..., triggered_at=NOW())
       │   → 发布 AlertCreated 事件
       │
       ├─ 存在且 severity 变更 → 更新:
       │   UPDATE alerts SET severity=$1, evidence=$2, updated_at=NOW()
       │   → 发布 AlertUpdated 事件
       │
       └─ 所有 active dedup_key 在本次检测中未再触发的 → 自动解除:
           UPDATE alerts SET status='resolved', resolved_at=NOW()
           WHERE dedup_key NOT IN (本次触发的 dedup_keys) AND status='active'
            AND window_end < NOW()
           → 发布 AlertResolved 事件
```

### 4.2 ThresholdDetector 逻辑

```go
type ThresholdDetector struct{}

func (d *ThresholdDetector) Detect(metricUID string, config json.RawMessage, window DetectionWindow) (*DetectionResult, error) {
    var cfg struct {
        Op    string  // ">" | "<" | ">=" | "<=" | "between" | "outside"
        Value float64
        Value2 float64 // for between/outside
    }
    json.Unmarshal(config, &cfg)

    // 获取最新 observation value
    value, err := metricService.GetLatestValue(metricUID)
    if err != nil { return nil, err }

    triggered := false
    switch cfg.Op {
    case ">":  triggered = value > cfg.Value
    case "<":  triggered = value < cfg.Value
    case ">=": triggered = value >= cfg.Value
    case "<=": triggered = value <= cfg.Value
    case "between":    triggered = value >= cfg.Value && value <= cfg.Value2
    case "outside":   triggered = value < cfg.Value || value > cfg.Value2
    }

    return &DetectionResult{
        Triggered: triggered,
        Severity:  cfg.Severity,
        Evidence: map[string]any{
            "current_value": value,
            "threshold_op": cfg.Op,
            "threshold_value": cfg.Value,
        },
    }, nil
}
```

### 4.3 去重机制

```
dedup_key = sha256(metric_id + ":" + rule_id + ":" + window_start.Format(time.RFC3339))[:16]

索引:
  CREATE UNIQUE INDEX idx_alerts_active_dedup ON alerts(dedup_key) WHERE status = 'active';

语义保证:
  - 同一 dedup_key + status='active' → 只能有 1 行
  - 新触发到来时，先尝试 UPDATE 已有 active 行
  - UPDATE 0 行 → INSERT 新行
  - 并发冲突 → 依赖唯一索引拒绝第二个 INSERT
```

---

## 五、数据库表变更

M3 不新增表，使用 baseline DDL 中已定义的 `alerts` 和 `event_outbox`。确认以下关键点：

### 5.1 alerts 表

已在 baseline DDL 中定义完整，核心去重索引：
```sql
CREATE UNIQUE INDEX idx_alerts_active_dedup ON alerts(dedup_key) WHERE status = 'active';
```

M3 新增辅助索引（如需按时间窗口自动解除）：
```sql
CREATE INDEX idx_alerts_active_window ON alerts(window_end) WHERE status = 'active';
```

### 5.2 event_outbox 表

已在 baseline DDL 中定义。M3 新增监控视图：
```sql
-- 监控：待处理事件数 / 平均等待时间
CREATE VIEW v_outbox_stats AS
SELECT status, COUNT(*) AS cnt, MIN(created_at) AS oldest
FROM event_outbox
GROUP BY status;
```

### 5.3 迁移文件命名

```
migrations/002_m3_alert_indexes.up.sql    — 上述新增索引
migrations/002_m3_alert_indexes.down.sql  — 对应 DROP INDEX
```

---

## 六、API/协议变更

M3 不新增 proto 消息。所有检测计算在 Core 内部进行，Plugin 不需要感知。

### 6.1 内部事件约定

```go
// Tier 1 事件（via Outbox）
type MetricUpdated struct {
    MetricID  string `json:"metric_id"`
    MetricUID string `json:"metric_uid"`
    Timestamp int64  `json:"timestamp"`
}

// In-process 事件（AlertEngine 内部订阅 DetectionTriggered）
type DetectionTriggered struct {
    MetricID    string
    MetricUID   string
    RuleID      int
    RuleVersion int
    Detector    string
    Severity    string
    Evidence    map[string]any
    WindowStart time.Time
    WindowEnd   time.Time
}

type AlertCreated Alert
type AlertUpdated Alert
type AlertResolved Alert
```

### 6.2 Alert Engine 错误处理

- **outbox 处理失败**：`attempts++`，超过 5 次标记 `failed`，不阻塞后续 event
- **Detector panic**：`recover()` 捕获，记录 error metric，事件标记 `failed`
- **Alert DB 写入失败**：outbox event 保留 `pending`，等待下次轮询重试

---

## 七、验收标准

1. **Threshold 触发**：配置 `gold.etf.net_inflow` 的 `threshold > 100000000` 规则后，写入一条 value=150000000 的 observation，5 秒内 `alerts` 表生成 `status='active'` 行，severity 与规则配置一致。
2. **自动解除**：已触发的 Alert 对应的 Metric 值恢复正常（value < 阈值）后，下一次检测周期（≤ 10s）内 Alert `status` 变为 `resolved`。
3. **去重有效**：连续写入 10 条超阈值 observation，`alerts` 表对同一 dedup_key 仅 1 行 active；后续 observation 只更新 `evidence` 和 `updated_at`。
4. **Outbox 不丢**：Kafka 风格验证——kill Core 进程后重启，`event_outbox` 中无 `status='pending'` 的遗留事件未被处理。
5. **噪音预算**：在默认规则配置下（3-5 条 threshold 规则），24 小时内 Alert 总数 ≤ 10 条。可观察 `SELECT COUNT(*) FROM alerts WHERE triggered_at > NOW() - INTERVAL '24h'`。

---

## 八、边界/局限

- **仅实现 Threshold Detector**：Percentile、Trend 在 M3 后续迭代中完成，但不在本里程碑首版的验收范围内。
- **不做 Research Snapshot**：M3 的 AlertEngine 调用 ResearchAssembler 时返回空 context（桩实现），`research_snapshots` 表写入 `context='{}'`。M4 替换桩为完整实现。
- **单 Worker 轮询**：MVP 用一个 Goroutine 轮询 outbox，不做分区/分片。
- **无聚合告警**：同一 Entity 的多个 Metric 异常不会聚合为一个 Alert；每个 Rule + Metric 组合独立告警。
- **不做 Alert 通知**：无邮件、短信、Webhook 等通知通道（可能在 M5 或后续实现）。
- **不做人工审核**：Alert 自动触发自动解除，无需人工确认。
- **window 范围固定**：默认检测窗口为 "当前值 vs 固定阈值"，不做滑动窗口（TrendDetector 在后续迭代实现）。

---

## 九、与后续里程碑的接口

| 下游里程碑 | M3 提供的能力 |
|-----------|--------------|
| M4 Research Assembly | Alert 已生成 → ResearchAssembler 被调用（桩→完整），冻结 ontology snapshot |
| M5 Control + Frontend | alerts 表数据用于 Capital Radar 首页展示和 Alert 详情页 |
| 后续（通知通道） | AlertCreated/Resolved 事件可订阅用于邮件/Webhook 推送 |
