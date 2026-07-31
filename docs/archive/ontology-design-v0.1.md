# Ontology Layer Design

## 问题

Metric 命名规范（`namespace.entity.metric`）只定义了表层语法，没有定义底层语义。

当前系统能做的：

```
gold.etf.net_inflow  →  检测到连续流入15天  →  Alert
gold.price           →  检测到突破历史高点    →  Alert
```

当前系统做不到的：

```
gold.etf.net_inflow 的异常 →  为什么会发生？
gold.price 的异常          →  还有什么和它相关？
china.northbound.flow      →  和 usd.index 有没有关系？
```

**Metric 是观测值。Ontology 是观测对象之间的结构。**

没有 Ontology，Alert 是孤立的。有了 Ontology，Alert 是一张网的入口。

---

## 设计目标

1. **定义实体之间的关系**，而不只是实体本身
2. **支持 Alert 关联推理**：一个异常触发时，知道应该展开哪些上下文
3. **支持跨 Metric 关联**：不同 namespace 的 Metric 可以通过实体关系连接
4. **渐进式构建**：MVP 只定义已知关系，未来逐步扩展
5. **Plugin 声明式**：Plugin 声明自己拥有的实体和关系，Core 只负责存储和查询

---

## 核心模型

### Entity（实体）

Entity 是被观测的对象。Metric 依附于 Entity。

```
Entity:
  id:           string        # "comex_gold", "gold_etf", "dxy", "btc"
  name:         string        # "COMEX黄金", "黄金ETF", "美元指数", "BTC"
  namespace:    string        # "comex", "etf", "forex", "crypto"
  entity_type:  EntityType    # 见下方
  tags:         []string
  metadata:     map           # Plugin 自定义的扩展字段
```

### EntityType（实体类型）

类型定义了实体的"类别"，决定了它在关系中可能扮演的角色。

```go
type EntityType string

const (
    EntityTypeAsset         EntityType = "asset"         # 可交易资产：BTC, ETH, 黄金
    EntityTypeIndex         EntityType = "index"         # 指数：DXY, NASDAQ, S&P500
    EntityTypeInstrument    EntityType = "instrument"    # 金融工具：ETF, 期货, 期权
    EntityTypeFlow          EntityType = "flow"          # 资金流：北向资金, ETF资金流
    EntityTypeInstitution   EntityType = "institution"   # 机构：美联储, 日本央行
    EntityTypeIndicator     EntityType = "indicator"     # 宏观指标：CPI, PMI, 利率
    EntityTypeMarket        EntityType = "market"        # 市场/交易所：A股, 美股, 韩国
)
```

### Relation（关系）

关系是 Ontology 的核心。两个 Entity 之间的有向连接。

```
Relation:
  source:        string        # 起始 Entity id
  target:        string        # 目标 Entity id
  relation_type: RelationType  # 关系类型
  direction:     Direction     # 因果/领先方向
  confidence:    float         # 置信度 0-1
  typical_lag:   Duration      # 典型滞后时间（可选）
  description:   string        # 人类可读描述
  plugin:        string        # 谁声明的这条关系
```

### RelationType（关系类型）

```go
type RelationType string

const (
    // 因果类
    RelationTypeCauses        RelationType = "causes"         # A 导致 B
    RelationTypeLeads         RelationType = "leads"          # A 领先于 B（时间上先变）
    RelationTypeSignals       RelationType = "signals"        # A 是 B 的信号/先行指标

    // 相关类
    RelationTypeCorrelates    RelationType = "correlates"     # A 和 B 相关（方向性待定）
    RelationTypeInverselyCorrelates RelationType = "inversely_correlates"  # A 和 B 负相关

    // 结构类
    RelationTypeComponentOf   RelationType = "component_of"   # A 是 B 的组成部分
    RelationTypeTracks        RelationType = "tracks"         # A 跟踪/追踪 B
    RelationTypeHedges        RelationType = "hedges"         # A 是 B 的对冲

    // 竞争类
    RelationTypeCompetes      RelationType = "competes"       # A 和 B 竞争同一资金/注意力
)
```

### Direction（方向）

```go
type Direction string

const (
    DirectionForward  Direction = "forward"   # source → target
    DirectionBackward Direction = "backward"  # source ← target
    DirectionBidirectional Direction = "bidirectional"  # 双向
)
```

---

## 举例

### 黄金生态

```
Entities:
  comex_gold       (asset,    "COMEX黄金期货")
  spot_gold        (asset,    "现货黄金")
  gold_etf         (instrument, "黄金ETF (GLD)")
  gold_etf_flow    (flow,     "黄金ETF资金流")
  dxy              (index,    "美元指数")
  us_10y_yield     (indicator, "美国10年期国债收益率")
  fed              (institution, "美联储")
  comex_inventory  (indicator, "COMEX黄金库存")

Relations:
  gold_etf_flow    →leads→      gold_etf          # ETF资金流领先ETF价格
  gold_etf         →tracks→     comex_gold        # ETF追踪期货价格
  dxy              →causes→     comex_gold        # 美元强弱影响金价（负相关）
  us_10y_yield     →correlates→ comex_gold        # 实际利率影响金价
  fed              →causes→     us_10y_yield      # 美联储政策影响利率
  comex_inventory  →signals→    comex_gold        # 库存变化信号价格
```

### 中美跨境

```
Entities:
  china_northbound_flow  (flow,     "北向资金")
  a_share                (market,   "A股")
  usd_index              (index,    "美元指数")
  usd_cny                (indicator, "美元/人民币汇率")
  china_bond             (market,   "中国债市")

Relations:
  usd_cny               →causes→    china_northbound_flow  # 汇率影响北向资金
  usd_index             →correlates→ usd_cny               # 美元指数影响汇率
  china_northbound_flow →signals→   a_share                # 北向资金是A股信号
```

---

## Ontology 在系统中的位置

```
Plugin
  ↓
  声明 Entity + Relation
  ↓
Collect
  ↓
  生成 Metric
  ↓
Detect
  ↓
  生成 Alert
  ↓
Ontology Lookup  ← 这里介入
  ↓
  展开相关 Entity + Relation
  ↓
Research Page
```

### Alert → Ontology → Research 的流程

用户看到：

```
🚨 黄金ETF连续流入15天
```

点击进入。系统通过 Ontology 查询：

```
gold_etf_flow  --[leads]-->  gold_etf
gold_etf_flow  --[tracks]-->  comex_gold
dxy            --[causes]-->  comex_gold
us_10y_yield   --[correlates]-->  comex_gold
```

自动展开：

```
📊 相关指标变化：
  - 美元指数：过去15天下降3.2%（↓）
  - 美国10年期收益率：过去15天下降12bp（↓）
  - COMEX黄金库存：过去30天减少5%（↓）
  - 北向资金：今日净流入45亿（↑）

🔗 关系图谱：
  美联储 → 美债收益率 → 黄金 → 黄金ETF → 资金流
```

用户自己判断：这是一个利率驱动的黄金行情，还是避险情绪驱动的？

---

## 数据模型（PostgreSQL）

### entities 表

```sql
CREATE TABLE entities (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    namespace   TEXT NOT NULL,
    entity_type TEXT NOT NULL,
    tags        JSONB DEFAULT '[]',
    metadata    JSONB DEFAULT '{}',
    plugin      TEXT NOT NULL,
    created_at  TIMESTAMPTZ DEFAULT NOW(),
    updated_at  TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_entities_namespace ON entities(namespace);
CREATE INDEX idx_entities_type ON entities(entity_type);
```

### relations 表

```sql
CREATE TABLE relations (
    id              SERIAL PRIMARY KEY,
    source_id       TEXT NOT NULL REFERENCES entities(id),
    target_id       TEXT NOT NULL REFERENCES entities(id),
    relation_type   TEXT NOT NULL,
    direction       TEXT NOT NULL DEFAULT 'forward',
    confidence      FLOAT DEFAULT 0.5,
    typical_lag     INTERVAL,
    description     TEXT,
    plugin          TEXT NOT NULL,
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW(),

    UNIQUE(source_id, target_id, relation_type)
);

CREATE INDEX idx_relations_source ON relations(source_id);
CREATE INDEX idx_relations_target ON relations(target_id);
CREATE INDEX idx_relations_type ON relations(relation_type);
```

### metric_entities 表（Metric ↔ Entity 的归属关系）

```sql
CREATE TABLE metric_entities (
    metric_id   TEXT NOT NULL,
    entity_id   TEXT NOT NULL REFERENCES entities(id),
    role        TEXT DEFAULT 'observation',  -- observation, derived, aggregated
    PRIMARY KEY (metric_id, entity_id)
);
```

---

## Plugin 接口

每个 Plugin 声明自己拥有的 Entity 和 Relation：

```go
// ontology.go

type EntityDeclaration struct {
    ID          string
    Name        string
    Namespace   string
    EntityType  EntityType
    Tags        []string
    Metadata    map[string]interface{}
}

type RelationDeclaration struct {
    SourceID      string
    TargetID      string
    RelationType  RelationType
    Direction     Direction
    Confidence    float64
    TypicalLag    time.Duration
    Description   string
}

type OntologyProvider interface {
    // 声明本 Plugin 拥有的实体
    Entities() []EntityDeclaration

    // 声明本 Plugin 知道的关系
    Relations() []RelationDeclaration
}
```

示例：

```go
// plugin/etf/ontology.go

type ETFOntology struct{}

func (o *ETFOntology) Entities() []ontology.EntityDeclaration {
    return []ontology.EntityDeclaration{
        {ID: "gold_etf", Name: "黄金ETF", Namespace: "etf", EntityType: ontology.EntityTypeInstrument},
        {ID: "silver_etf", Name: "白银ETF", Namespace: "etf", EntityType: ontology.EntityTypeInstrument},
    }
}

func (o *ETFOntology) Relations() []ontology.RelationDeclaration {
    return []ontology.RelationDeclaration{
        {
            SourceID:     "gold_etf_flow",
            TargetID:     "gold_etf",
            RelationType: ontology.RelationTypeLeads,
            Direction:    ontology.DirectionForward,
            Confidence:   0.7,
            TypicalLag:   24 * time.Hour,
            Description:  "ETF资金流通常领先ETF价格变化1-2天",
        },
    }
}
```

---

## Ontology 查询 API

```go
// core/ontology/service.go

type OntologyService interface {
    // 给定一个 Entity，返回它的所有邻居（关系图）
    GetNeighbors(ctx context.Context, entityID string, depth int) (*EntityGraph, error)

    // 给定一个 Alert，返回关联的 Entity 和 Relation
    GetAlertContext(ctx context.Context, alertID string) (*AlertContext, error)

    // 给定一个 Metric，找到它归属的 Entity，再展开关系
    GetMetricContext(ctx context.Context, metricID string) (*MetricContext, error)

    // 两个 Entity 之间是否存在关系路径
    FindPath(ctx context.Context, from, to string) ([]Relation, error)
}
```

### AlertContext 示例输出

```json
{
  "alert": {
    "title": "黄金ETF连续流入15天",
    "metric": "gold.etf.net_inflow",
    "entity": "gold_etf_flow"
  },
  "related_entities": [
    {
      "id": "gold_etf",
      "name": "黄金ETF",
      "current_metrics": {
        "price": {"value": 195.2, "change_7d": "+3.1%"},
        "aum": {"value": "850亿美元", "change_30d": "+5.2%"}
      },
      "relation": {"type": "leads", "lag": "1-2天", "confidence": 0.7}
    },
    {
      "id": "comex_gold",
      "name": "COMEX黄金",
      "current_metrics": {
        "price": {"value": 2350, "change_7d": "+2.8%"}
      },
      "relation": {"type": "tracks", "confidence": 0.95}
    },
    {
      "id": "dxy",
      "name": "美元指数",
      "current_metrics": {
        "value": {"value": 103.2, "change_15d": "-1.5%"},
        "recent_alerts": ["美元指数跌破半年趋势线"]
      },
      "relation": {"type": "causes", "confidence": 0.8, "direction": "inversely_correlates"}
    }
  ],
  "narrative": "黄金ETF资金持续流入，与美元走弱、实际利率下行同步。历史上，类似的ETF资金流领先价格变化约1-2天。"
}
```

---

## 渐进式构建策略

### Phase 1：MVP

- Plugin 声明 Entity 和 Relation（硬编码）
- Research 页面通过 Relation 展开关联指标
- 不做自动发现，只做已知关系的查询

### Phase 2：半自动

- 基于历史 Metric 数据，自动计算 Correlation
- 高相关性对推荐给用户确认："这两个指标相关性 0.85，是否加入 Ontology？"
- 允许用户手动添加 Relation

### Phase 3：全自动

- 基于 Granger Causality、VAR 模型自动发现领先/滞后关系
- 自动计算 Confidence 和 TypicalLag
- 人工审核后纳入 Ontology

---

## 核心原则重申

1. **Ontology 是 Plugin 声明的，不是 Core 硬编码的**：Core 只负责存储和查询关系图，不包含任何"黄金和美元负相关"这样的金融知识。

2. **Ontology 是有向图，不是无向图**：`leads`、`causes`、`signals` 都有方向性。方向性是推理的基础。

3. **Confidence 是可变的**：关系的置信度随时间、市场状态变化。MVP 可以是静态的，未来应该是动态的。

4. **Ontology 支持但不替代 Detector**：Detector 检测单个 Metric 的异常。Ontology 在 Alert 产生后，提供关联上下文。两者分工明确。
