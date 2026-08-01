# M4: 研究上下文组装

**里程碑：M4 — Research Read Path**
**版本：1.0**
**状态：Draft**
**关联文档：** [prd-v4.md](../prd-v4.md) · [system-architecture.md](../system-architecture.md) · [domain-model.md](../domain-model.md) · [adr.md](../adr.md)

---

## 一、目标

M4 的核心目标是实现围绕 Alert 的**研究上下文自动组装**，把"某个 Metric 异常了"升级为"这个异常涉及哪些实体、关联指标如何变化、系统当时是如何理解的"。具体包括：

1. **Ontology Snapshot**：Alert 触发时冻结当前生效的 Ontology 状态（entities + relations），确保历史 Alert 的解释不受后续 Ontology 变更影响（system-architecture.md §九）。
2. **实体关系遍历**：从 Alert 的 Metric 出发 → 找到归属 Entity → 沿 Relation 图做 BFS 遍历（depth ≤ 2），收集关联 Entity 及其当前 Metric 值。
3. **趋势数据点**：为每个关联 Metric 查询最近 N 天的观测序列，形成时序趋势数据点，供前端折线图渲染。
4. **REST API**：提供 `GET /api/v1/research/:alert_id` 直接返回冻结的 ResearchContext JSON。

M4 替换 M3 中的 ResearchAssembler 桩实现，完成 `Alert → ResearchSnapshot` 的 1:1 不变量闭环。

---

## 二、用户故事

### 故事 1：查看 Alert 的研究上下文
> 作为用户，当我在 Capital Radar 首页点击一条 Alert，我希望看到：这个异常发生的指标、它关联的实体、这些关联实体当前的状态指标，以及系统是如何通过 Ontology 关系将它们串联起来的。

**验收条件：**
- GET `/api/v1/research/alt_20260730_abc123` 返回完整 JSON
- 包含：triggered_metric、related_entities（至少 1 个）、relation_paths、metric_time_series
- 响应时间 < 500ms（读 snapshot，不做实时计算）

### 故事 2：Ontology 变更不影响历史解释
> 作为系统，当 Plugin 升级导致 Ontology Relation 被修改或删除，已触发的历史 Alert 的研究上下文应保持不变。

**验收条件：**
- Alert A 在 Day 1 触发 → `research_snapshots.context` 冻结
- Day 2 Plugin 升级，删除一条 Relation
- 查询 Alert A 的 Research 结果与 Day 1 完全一致（snapshot 不受影响）

### 故事 3：深度控制
> 作为系统，关联展开不应无限递归，避免当 Ontology 关系图较大时返回过多无关实体。

**验收条件：**
- 默认 BFS depth = 2（从 Metric → Entity → 一跳 Relation → 关联 Entity → 二跳 Relation）
- 单条 Alert 返回的 `related_entities` 不超过 20 个
- depth 和上限可通过配置调整（MVP 硬编码）

---

## 三、领域模型变化

M4 首次写入一个领域对象，并激活两个已有对象的查询能力：

### 3.1 ResearchSnapshot（首次写入）

| 字段 | 说明 |
|------|------|
| `alert_id` | PK，FK → alerts |
| `context` | 冻结的 ResearchContext JSON |
| `ontology_frozen_at` | 冻结时刻的时间戳 |

M4 将 M3 中 `research_snapshots.context = '{}'` 的桩替换为真实组装结果。

### 3.2 Entity（激活图遍历查询）

M1 已注册 entities/relations 数据。M4 新增 Ontology 查询能力：

```go
type OntologyQuerier interface {
    // 当前生效版本的 Entity 查询
    GetCurrentEntity(ctx context.Context, id string) (*Entity, error)
    
    // 从指定 Entity 出发，BFS 遍历 Relation 图
    GetRelatedEntities(ctx context.Context, entityID string, maxDepth int, maxResults int) ([]RelatedEntity, error)
    
    // 查询 Entity 当前生效的所有 Relation（出边 + 入边）
    GetRelations(ctx context.Context, entityID string) ([]Relation, error)
}

type RelatedEntity struct {
    Entity      Entity
    Relation    Relation     // 从起点 Entity 到这个 Entity 的关系
    Depth       int          // BFS 深度
    Path        []string     // 关系路径上的 Entity ID 序列
}
```

### 3.3 Relation（激活按层过滤）

遍历时按 Relation layer 过滤：
- **structural**：总是包含（ETF tracks 黄金、ETF issued_by 基金公司）
- **semantic**：总是包含（黄金 hedges 美元）
- **statistical**：confidence ≥ 0.7 才包含（避免低质量关联污染）
- **causal**：仅 accepted 的包含（pending 不包含）

### 3.4 ResearchContext JSON 结构

```json
{
  "alert_id": "alt_20260730_abc123",
  "triggered_metric": {
    "metric_id": "gold.etf.net_inflow",
    "metric_uid": "mtr_abc123",
    "current_value": 152000000,
    "unit": "USD",
    "time_series": [
      {"time": "2026-07-24T00:00:00Z", "value": 98000000},
      {"time": "2026-07-25T00:00:00Z", "value": 105000000},
      {"time": "2026-07-30T00:00:00Z", "value": 152000000}
    ]
  },
  "related_entities": [
    {
      "id": "ent_gold_etf",
      "name": "黄金ETF",
      "entity_type": "instrument",
      "relation": {"type": "component_of", "direction": "forward", "layer": "structural"},
      "current_metrics": [
        {"metric_id": "gold.etf.price", "value": 195.2, "change_7d": "+3.1%", "unit": "USD"}
      ]
    },
    {
      "id": "ent_dxy",
      "name": "美元指数",
      "entity_type": "index",
      "relation": {"type": "inversely_correlates", "direction": "bidirectional", "layer": "statistical", "confidence": 0.82},
      "current_metrics": [
        {"metric_id": "usd.index", "value": 103.2, "change_15d": "-1.5%", "unit": ""}
      ]
    }
  ],
  "ontology_frozen_at": "2026-07-30T08:30:00Z",
  "narrative": ""
}
```

---

## 四、数据流

```
AlertEngine.handleDetectionTriggered() (M3 已完成)
  │
  ├─ 新 Alert 创建后 → 调用 ResearchAssembler.GetContext(alert)
  │
  ▼
ResearchAssembler.GetContext(alert)
  │
  ├─ Step 1: 解析 Metric 归属 Entity
  │   metric_id → metric_definitions_v2.entity_id → entities_v2 (effective_to IS NULL)
  │
  ├─ Step 2: BFS 图遍历 (depth ≤ 2, max 20 entities)
  │   Queue = [{entity: rootEntity, depth: 0, path: []}]
  │   While Queue not empty:
  │     current = Dequeue()
  │     relations = GetRelations(current.entity.id)
  │     For each relation where confidence ≥ threshold AND layer allowed:
  │       nextEntity = GetEntity(relation.target_id or source_id)
  │       If nextEntity not visited AND current.depth < maxDepth:
  │         Enqueue({nextEntity, depth+1, path + [nextEntity.id]})
  │         Collect RelatedEntity
  │
  ├─ Step 3: 组装关联 Metric 时序数据点
  │   For each RelatedEntity:
  │     metrics = GetMetricsForEntity(entity.id)
  │     For each metric:
  │       time_series = QueryTimeSeries(metric.uid, last=30, unit='days')
  │       → [(t1, v1), (t2, v2), ...]
  │
  ├─ Step 4: 组装触发 Metric 自身时序
  │   trigger_series = QueryTimeSeries(alert.metric_uid, last=30, unit='days')
  │
  ├─ Step 5: 序列化为 ResearchContext JSON
  │
  └─ Step 6: 写入 research_snapshots
      INSERT INTO research_snapshots (alert_id, context, ontology_frozen_at)
      VALUES ($1, $2::jsonb, NOW())
```

### 4.1 时序查询接口（复用 observations 表）

```sql
SELECT time, value
FROM observations
WHERE metric_uid = $1
  AND time > NOW() - INTERVAL '30 days'
  AND source_plugin = (
    SELECT source_plugin FROM source_preferences
    WHERE metric_id = $2 AND priority = 0
    LIMIT 1
  )
ORDER BY time ASC;
```

使用 TimescaleDB hypertable 的 `metric_uid + time DESC` 索引，查询性能 O(log n)。

### 4.2 并发控制

- ResearchAssembler.GetContext() 是**同步调用**——AlertEngine 在创建 Alert 后立即调用，完成后才返回。
- 由于组装过程涉及多次 DB 查询 + 图遍历，预计耗时 50-200ms。
- MVP 不做异步构建或队列缓冲——如果成为瓶颈（实测 P99 > 500ms），后续可改为 outbox 模式异步组装。

---

## 五、数据库表变更

M4 不新增表，使用 baseline DDL 中的 `research_snapshots`。新增查询优化索引：

### 5.1 图遍历优化（relations_v2 已有索引确认）

```sql
-- 已有：idx_relations_source, idx_relations_target
-- 新增：加速"查当前生效关系的 source/target"
CREATE INDEX idx_relations_current_source ON relations_v2(source_id) WHERE effective_to IS NULL;
CREATE INDEX idx_relations_current_target ON relations_v2(target_id) WHERE effective_to IS NULL;
```

### 5.2 Entity → Metric 查询优化

```sql
-- metric_definitions_v2 已有 idx_metric_def_current (WHERE effective_to IS NULL)
-- 新增：按 entity_id 查当前生效 metrics
CREATE INDEX idx_metric_def_entity_current ON metric_definitions_v2(entity_id) WHERE effective_to IS NULL;
```

### 5.3 migration 文件命名

```
migrations/003_m4_research_indexes.up.sql   — 上述索引
migrations/003_m4_research_indexes.down.sql — DROP INDEX
```

### 5.4 research_snapshots 表结构（确认）

```sql
CREATE TABLE research_snapshots (
    alert_id              TEXT PRIMARY KEY REFERENCES alerts(id),
    context               JSONB NOT NULL,
    ontology_frozen_at    TIMESTAMPTZ NOT NULL,
    created_at            TIMESTAMPTZ DEFAULT NOW()
);
```

---

## 六、API/协议变更

M4 新增 HTTP REST API（不新增 gRPC proto):

### 6.1 Research API

```
GET /api/v1/research/:alert_id
```
- **输入**：alert_id (path parameter)
- **输出**：ResearchContext JSON（直接从 `research_snapshots` 读）
- **逻辑**：`SELECT context FROM research_snapshots WHERE alert_id = $1`
- **404**：alert 不存在或无 snapshot

```
GET /api/v1/alerts/:alert_id
```
- **输入**：alert_id
- **输出**：Alert 基本信息（与 research 分离，用于 Alert 列表/卡片展示）

### 6.2 REST Handler 骨架

```go
func (h *ResearchHandler) GetResearch(w http.ResponseWriter, r *http.Request) {
    alertID := chi.URLParam(r, "alertID")
    var contextJSON []byte
    err := h.db.QueryRowContext(r.Context(),
        "SELECT context FROM research_snapshots WHERE alert_id = $1", alertID,
    ).Scan(&contextJSON)
    if err == sql.ErrNoRows {
        http.NotFound(w, r)
        return
    }
    w.Header().Set("Content-Type", "application/json")
    w.Write(contextJSON)
}
```

---

## 七、验收标准

1. **快照生成**：Alert 创建后，`research_snapshots` 表立即有一行对应记录，`context` 非空 JSON，`ontology_frozen_at` 不为 NULL。
2. **关联展开**：对 gold.etf.net_inflow 的 Alert，Research 返回的 `related_entities` 至少包含黄金 ETF 和 COMEX 黄金两个 direct relation（由 M1 注册的 Entity 数据驱动）。
3. **时序数据**：每个 `related_entities[].current_metrics[].time_series` 包含 30 天内的观测数据点（或更早起始日到当天的所有点）。
4. **快照不可变性**：手动修改 relations_v2 的 effective_to（模拟 Plugin 升级），历史 Alert 的 Research 返回结果不变（`ontology_frozen_at` 保持原值）。
5. **性能**：`GET /api/v1/research/:alert_id` 响应时间 P95 < 200ms（snapshot 为预计算读）。

---

## 八、边界/局限

- **BFS 深度固定为 2**：不做动态深度调整（如 "如果 depth=1 结果太少，扩展到 depth=3"）。
- **不做实时重新组装**：Snapshot 一旦冻结，永远不会被"用最新 ontology 重新解释"（system-architecture.md §9.4 中提到的手动覆盖功能延迟到 Phase 2）。
- **关联 Entity 上限 20**：超过后被截断，不做分页或展开更多。
- **statistical 层 confidence 阈值固定 0.7**：不做个性化或按 Metric 类型调整。
- **不做 Path 最短路径算法**：BFS 保证最少跳数，但不保证全局最短（加权图）。
- **narrative 字段为空**：MVP 不自动生成自然语言描述（Phase 2 AI 能力）。
- **不做多 Metric 联动分析**：每个 Alert 独立展开，不考虑 "Entity A 的 Metric X 和 Entity B 的 Metric Y 同时异常" 的联合分析。
- **不做 Observation 和 Ontology 的反向校验**：假定 M1 注册的 Ontology 与 M2/Observation 的 Metric 数据一致，不做 orphan observation 检测。

---

## 九、与后续里程碑的接口

| 下游里程碑 | M4 提供的能力 |
|-----------|--------------|
| M5 Control + Frontend | `GET /api/v1/research/:alert_id` API 供 Alert 详情页 ECharts 渲染；Capital Radar 首页可直接展示摘要 |
| Phase 2（AI narrative） | `research_snapshots.context` 结构化 JSON 作为 LLM prompt 输入，生成 `narrative` 描述 |
| Phase 2（Snapshot 重写） | 手动触发 `ResearchAssembler.GetContext()` 覆盖已有 snapshot，需新增 API 端点 |
