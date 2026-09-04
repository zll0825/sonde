# Sonde PRD v4.0

**一个面向资本市场的可观测性平台**

版本：4.1
状态：Active — 需求基线，与 System Architecture v1.0（冻结）对齐

> **对齐勘误（2026-07-31）**：本 PRD 成稿早于架构冻结。以下条目以冻结文档为准：
>
> - Entity / Relation 词表已收敛为 7 类实体 + 四层 13 种关系，新旧映射见 [ADR-7](./adr.md)。§9.2 的示例使用旧词表，仅作意图说明，插件声明必须使用冻结词表。
> - 技术栈以 §16（已更新）与 [system-architecture.md](./system-architecture.md) §十二 为准；Redis / NATS / Temporal / OpenSearch / Kubernetes 均移出 MVP。
> - 目录结构以 [conventions.md](./conventions.md) 与 system-architecture.md §十三 为准。
> - MVP 页面为两个：Capital Radar 首页 + Alert 详情页（Research 上下文以冻结 Snapshot 呈现在详情页内，见 system-architecture.md §九）。§14 的"基础版 Research 页"即指后者，不是第三个页面。
> - Plugin 能力包中的 Context Builder（`ContextTemplates()`）未进入 plugin.proto v1 契约，是否纳入 MVP 在 M1 proto 定稿时拍板（见 [adr.md](./adr.md) 待决事项）。

---

# 一、产品简介

### 产品名称

**Sonde**

### 一句话定位

> 持续监控全球资本指标，自动发现异常变化，把用户带到最值得研究的线索上。

### 产品使命

> **Monitor Capital Metrics. Detect Capital Anomalies.**

---

# 二、产品背景

现代投资的主要矛盾，不是没有数据，而是注意力不足。

市场每天都在产生海量数据——股票、ETF、债券、商品、外汇、Crypto、宏观经济、新闻、链上数据。个人研究者无法在有限时间内判断"今天应该先看什么"。

现有工具（Bloomberg、Wind、TradingView）更擅长回答：

> "告诉我 XXX 现在怎么样。"

Sonde 要回答的是：

> "今天资本市场有哪些地方出现了异常，值得优先研究？"

---

# 三、产品目标

系统只做三件事：

1. **持续观察**资本市场中的关键指标。
2. **自动发现**偏离正常状态的异常变化。
3. **提供研究入口**，而不是投资结论。

产品明确不负责：

- 投资建议
- 买卖推荐
- 收益预测
- 自动交易
- 策略回测

---

# 四、目标用户

### MVP 用户

具备一定投资经验、愿意独立研究、但每天面对海量信息不知道该优先关注什么的个人投资者。当前阶段即作者本人。

### 用户需求

用户需要的不是更多数据面板，而是一个每天打开后能直接看到"今天新增了哪些重要异常"的系统。先被告知异常，再决定是否进入更深的研究。

---

# 五、产品原则

## Principle 1：异常优先

系统优先输出异常，而不是输出全部信息流。首页的默认视角必须是"今天哪些地方和平时不一样"。

## Principle 2：研究优先

系统提供研究入口，不提供推荐结论。任何页面都不应输出"建议买入""建议卖出"等投资建议。

## Principle 3：Metric First

系统中的所有检测、告警与研究入口，首先都围绕 Metric 组织。资产类别、市场类别、数据源类别都不能直接侵入 Core 逻辑。

## Principle 4：Ontology Under Metric

Metric 是观测值的统一表层语法，Ontology 定义观测对象之间的结构关系。系统不仅要知道"某个指标异常了"，还要知道"它和哪些实体、因子、市场存在结构关系"。

## Principle 5：Plugin First

金融世界的领域知识通过 Plugin 注入，而不是写死在 Core 中。每个 Plugin 不只是数据接入器，也是一组完整的领域能力包。

---

# 六、核心理念

Sonde 借鉴软件可观测性（Observability）思想。

就像 Prometheus 持续监控服务器一样，Sonde 持续监控资本市场。服务器监控关注 CPU、Memory、Network；资本市场监控关注 ETF 资金流、成交量、持仓变化、链上数据、宏观指标。

目标都是：**发现偏离正常状态的异常。**

因此，本产品不是行情终端，不是投顾系统，而是一套**资本市场异常发现与研究导航系统**。

---

# 七、产品能力

系统提供五项基础能力。

## 1. Collect

持续从多个市场、多个来源采集资本市场数据。

## 2. Observe

将原始数据标准化为统一的 Metric，持续记录其时间序列变化。

## 3. Relate

维护 Capital Ontology，定义实体、渠道、资产、市场、因子之间的结构关系，支持跨 Metric 的联动理解。

## 4. Detect

基于历史基线和检测规则自动识别异常，生成 Alert。

## 5. Research

围绕 Alert 自动展开关联指标、相关资产、时间线与背景信息，帮助用户快速进入研究。

---

# 八、系统流程

```text
Plugin
  ↓
Collect
  ↓
Observe (Metric)
  ↓
Relate (Ontology)
  ↓
Detect
  ↓
Alert
  ↓
Research
```

用户每天打开系统时，看到的不是全部市场数据，而是经过异常检测和关系扩展后的重点事件列表。

---

# 九、统一数据抽象

## 9.1 Metric

Metric 是系统最核心的观测单位。命名格式：

```text
<namespace>.<entity>.<metric>
```

例如：

```text
gold.etf.net_inflow
gold.price
btc.exchange.balance
fed.balance_sheet
usd.index
china.northbound.flow
```

每个 Metric 至少包含：

| 字段 | 说明 |
|------|------|
| `id` | 唯一标识 |
| `name` | 人类可读名称 |
| `description` | 描述 |
| `unit` | 单位 |
| `frequency` | 采集频率 |
| `plugin` | 所属 Plugin |
| `tags` | 标签 |
| `observed_entity_id` | 关联的 Ontology Entity |
| `observed_property` | 观测的属性 |
| `value` | 当前值 |
| `timestamp` | 时间戳 |

全部进入时序数据库。

## 9.2 Ontology

Ontology 是 Metric 之下的语义层，定义"观测对象是什么"以及"对象之间的关系是什么"。

### 解决的问题

- `gold.etf.net_inflow` 与 `gold.price` 为什么可能有关？
- `china.northbound.flow` 与 `usd.index` 在不同时间窗口下是否存在结构关系？
- 一个异常发生后，系统应该沿着哪些关系自动展开研究上下文？

### Entity 类型

| 类型 | 说明 | 示例 |
|------|------|------|
| `Asset` | 可交易资产 | BTC, ETH, 黄金, 原油 |
| `Vehicle` | 金融工具 | ETF, 期货, 期权 |
| `Market` | 市场/交易所 | A股, 美股, 韩国, 日本 |
| `Actor` | 市场参与者 | 美联储, 日本央行, 北向资金 |
| `Jurisdiction` | 管辖区域 | 美国, 中国, 日本 |
| `Factor` | 宏观因子 | 利率, 通胀, 汇率 |
| `Channel` | 资金/信息通道 | ETF申赎, 北向通道, 期货交割 |
| `Indicator` | 观测指标 | CPI, PMI, VIX |

### Relation 类型

| 关系 | 方向性 | 说明 |
|------|--------|------|
| `tracks` | A → B | A 追踪 B 的价格/状态 |
| `flows_into` | A → B | 资金从 A 流入 B |
| `flows_out_of` | A → B | 资金从 A 流出到 B |
| `exposed_to` | A → B | A 暴露于 B 的风险 |
| `influences` | A → B | A 影响 B（因果方向明确） |
| `correlates_with` | 双向 | A 与 B 相关（方向性待定） |
| `leads` | A → B | A 在时间上领先于 B |
| `lags` | A → B | A 在时间上滞后于 B |
| `depends_on_regime` | A → B | A 与 B 的关系依赖于市场状态 |
| `hedges` | A → B | A 是 B 的对冲 |

### 示例：黄金生态

```text
Entities:
  comex_gold        (Asset,      "COMEX黄金期货")
  spot_gold         (Asset,      "现货黄金")
  gold_etf          (Vehicle,    "黄金ETF (GLD)")
  gold_etf_flow     (Channel,    "黄金ETF资金流")
  dxy               (Factor,     "美元指数")
  us_10y_yield      (Factor,     "美国10年期国债收益率")
  fed               (Actor,      "美联储")
  comex_inventory   (Indicator,  "COMEX黄金库存")

Relations:
  gold_etf_flow  →leads→      gold_etf        # 资金流领先ETF价格
  gold_etf       →tracks→     comex_gold      # ETF追踪期货价格
  dxy            →influences→ comex_gold      # 美元影响金价
  us_10y_yield   →correlates_with→ comex_gold # 利率与金价相关
  fed            →influences→ us_10y_yield    # 美联储影响利率
  comex_inventory→leads→      comex_gold      # 库存变化领先价格
```

### 示例：中美跨境

```text
Entities:
  china_northbound_flow  (Channel,   "北向资金")
  a_share                (Market,    "A股")
  usd_index              (Factor,    "美元指数")
  usd_cny                (Factor,    "美元/人民币汇率")

Relations:
  usd_cny               →influences→    china_northbound_flow
  usd_index             →correlates_with→ usd_cny
  china_northbound_flow →leads→         a_share
```

### Alert 关联推理流程

当 Alert「黄金ETF连续流入15天」触发时：

```text
gold_etf_flow --[leads]--> gold_etf
gold_etf      --[tracks]--> comex_gold
dxy           --[influences]--> comex_gold
us_10y_yield  --[correlates_with]--> comex_gold
```

系统自动展开：

```text
📊 相关指标变化：
  美元指数：过去15天下降3.2%（↓）
  美国10年期收益率：过去15天下降12bp（↓）
  COMEX黄金库存：过去30天减少5%（↓）
  北向资金：今日净流入45亿（↑）

🔗 关系路径：
  美联储 → 美债收益率 → 黄金 → 黄金ETF → 资金流
```

用户自行判断：这是利率驱动的黄金行情，还是避险情绪驱动的。

## 9.3 Alert

Alert 是异常事件的标准化表达，而不是投资结论。每个 Alert 至少包含：

| 字段 | 说明 |
|------|------|
| `title` | 标题 |
| `summary` | 摘要 |
| `severity` | 严重级别（Critical / Warning / Info） |
| `metric_id` | 触发的 Metric |
| `detector_id` | 使用的 Detector |
| `timestamp` | 时间戳 |
| `window` | 检测时间窗口 |
| `plugin` | 所属 Plugin |
| `context` | 上下文信息 |
| `dedup_key` | 去重键 |
| `evidence` | 证据（原始数据快照） |

---

# 十、Core 与 Plugin 架构

## 10.1 Core 负责什么

Core 不包含具体金融资产知识。Core 只负责以下通用能力：

- Metric Registry（指标注册）
- Ontology Registry（本体注册）
- Baseline Engine（基线计算）
- Detector Engine（异常检测）
- Alert Engine（告警生成）
- Research Assembler（研究页组装）
- Scheduler（调度）

**Core 永远不应该直接知道什么叫 BTC、黄金、ETF 或北向资金；它只认识统一接口和统一抽象。**

## 10.2 Plugin 负责什么

Plugin 是领域能力包，每个 Plugin 至少提供：

| 能力 | 说明 |
|------|------|
| **Data Collector** | 采集原始数据 |
| **Metric Definition** | 定义本领域的 Metric |
| **Ontology Definition** | 声明 Entity 和 Relation |
| **Default Detector** | 定义本领域适合的异常检测策略 |
| **Context Builder** | Alert 触发后，定义默认展开的关联上下文 |

### Plugin 接口定义

```go
// Plugin 注册的完整能力
type Plugin interface {
    Name() string
    Version() string

    // 数据采集
    Collectors() []Collector

    // Metric 声明
    Metrics() []MetricDeclaration

    // Ontology 声明
    Entities() []EntityDeclaration
    Relations() []RelationDeclaration

    // 检测策略
    Detectors() []DetectorConfig

    // 研究上下文
    ContextTemplates() []ContextTemplate
}

type MetricDeclaration struct {
    ID                string
    Name              string
    Description       string
    Unit              string
    Frequency         time.Duration
    ObservedEntityID  string
    ObservedProperty  string
    Tags              []string
}

type EntityDeclaration struct {
    ID         string
    Name       string
    EntityType string  // Asset, Vehicle, Market, Actor, etc.
    Namespace  string
    Tags       []string
    Metadata   map[string]interface{}
}

type RelationDeclaration struct {
    SourceID      string
    TargetID      string
    RelationType  string  // tracks, leads, influences, etc.
    Direction     string  // forward, backward, bidirectional
    Confidence    float64
    TypicalLag    time.Duration
    Description   string
}
```

## 10.3 依赖方向

```
Core 定义接口和协议
  ↑
Plugin 实现接口并注册
```

Core 不依赖 Plugin。Plugin 依赖 Core。

## 10.4 MVP 首批 Plugin

- **ETF Plugin**：ETF 资金流、规模、价格
- **Crypto Plugin**：交易所余额、链上数据、哈希率
- **Macro Plugin**：美联储数据、国债收益率、汇率、CPI

---

# 十一、检测系统

Detector 独立于具体 Plugin，可以作用于任意 Metric。

## MVP 支持的 Detector

| Detector | 说明 | 示例 |
|----------|------|------|
| **Threshold** | 超过绝对阈值 | BTC > $100,000 |
| **Percentile** | 历史分位异常（95% / 99%） | ETF资金流达到历史99%分位 |
| **Trend** | 连续方向性变化 | 黄金ETF连续流入15天 |
| **Volatility** | 波动率突增 | VIX单日波动超过历史2σ |
| **Moving Average** | 偏离均值 | 价格偏离200日均线超过10% |

## 检测质量控制

为了避免首页被噪音淹没，检测系统需支持：

- **时间窗口定义**：支持 1h / 1d / 1w / 1m 等多种窗口
- **基线定义**：滚动历史基线，支持百分位、均值、标准差
- **告警去重**：同一异常在窗口内不重复告警（dedup_key）
- **严重级别分层**：Critical / Warning / Info
- **同类异常聚合**：多个相关 Metric 的异常合并为一个 Alert

---

# 十二、首页：Capital Radar

首页只做一件事：**按优先级展示今天最值得关注的异常。**

```text
┌─────────────────────────────────────────┐
│  🚨 Critical                            │
│  黄金ETF        连续流入15天             │
│  当前连续流入达到历史99%分位             │
├─────────────────────────────────────────┤
│  ⚠️  Warning                             │
│  BTC            交易所余额创五年新低      │
├─────────────────────────────────────────┤
│  ⚠️  Warning                             │
│  日本银行ETF    成交量突破历史极值        │
├─────────────────────────────────────────┤
│  ℹ️  Info                                │
│  美元指数       突破半年趋势线           │
└─────────────────────────────────────────┘
```

## 排序依据

首页排序综合以下因素：

1. **Severity**：Critical > Warning > Info
2. **新颖性**：新发现的异常优先于持续中的异常
3. **历史分位极端程度**：越极端越靠前
4. **跨 Metric 关联范围**：关联的实体越多，优先级越高
5. **用户关注域匹配度**：与用户历史研究领域相关的优先

首页不是搜索入口，不是全市场行情页，而是**异常雷达页**。

---

# 十三、研究页面

研究页面围绕一次 Alert 展开，不直接给出投资结论。

## 页面结构

```text
异常描述
  ↓
历史变化（时序图）
  ↓
关联 Metric（来自 Ontology 关系扩展）
  ↓
关联实体（Entity 关系图）
  ↓
相关新闻
  ↓
时间线
  ↓
历史对比
```

## 生成逻辑

Research 页面的生成来自两部分：

1. **Plugin 提供的默认领域上下文**：每个 Plugin 定义本领域异常发生时，应该展示哪些关联指标
2. **Ontology 提供的跨实体关系扩展**：通过关系图遍历，自动发现关联的 Entity 和 Metric

---

# 十四、MVP 范围

第一阶段只要求跑通完整链路，不追求覆盖全部市场。

## 数据域

- ETF
- Crypto
- Macro

## 检测器

- Threshold
- Percentile
- Trend

## Ontology

- 已知关系查询（Plugin 声明）
- Alert 关联展开（Research 页面）

## 页面

- Capital Radar 首页
- Alert 详情页
- 基础版 Research 页

## 必须完成的系统闭环

```text
Collect → Metric → Ontology → Detect → Alert → Research
```

---

# 十五、不在 MVP 范围

以下能力不在第一阶段：

- AI 投资建议
- AI 荐股
- 自动交易
- 策略回测
- 技术指标推荐
- 收益预测
- 风险评分
- Ontology 自动发现（Phase 2+）

---

# 十六、技术架构

## 技术栈（与 system-architecture.md §十二 对齐）

| 层 | 选型 |
|----|------|
| Backend | Go 1.22 |
| Database | PostgreSQL 16 + TimescaleDB 2.16（关系 + 时序一体） |
| Plugin ↔ Core | gRPC Bidirectional Stream（Buf 工具链） |
| 事件传递 | Transactional Outbox（PostgreSQL 内，替代消息队列） |
| Frontend | Next.js 14 + React + Tailwind + ECharts |
| 部署 | Docker Compose（单机一键启动） |

> 已移出 MVP：Redis、NATS、Temporal、OpenSearch、Kubernetes。在出现真实瓶颈前不引入——MQ 场景由 Outbox 覆盖（ADR-5），检索/缓存/编排需求出现时再立项。

## 目录结构

```text
sonde/
  cmd/
  core/
    metric/           # Metric 注册与存储
    ontology/         # Entity/Relation 注册与图查询
    detector/         # 检测引擎
    alert/            # 告警引擎
    research/         # 研究页组装
    scheduler/        # 调度器
  plugin/
    etf/              # ETF Plugin
    crypto/           # Crypto Plugin
    macro/            # Macro Plugin
    news/             # News Plugin（后期）
  pkg/                # 公共库
  internal/           # 内部实现
  configs/            # 配置文件
  api/                # API 层
  web/                # 前端
  docs/               # 文档
```

**Core 依赖抽象，不依赖 Plugin；Plugin 依赖 Core 提供的接口与协议。**

---

# 十七、成功标准

MVP 阶段的成功不以覆盖多少资产衡量，而以以下标准衡量：

1. 系统能稳定采集并更新三类数据源。
2. 系统能把异构数据统一成 Metric。
3. 系统能通过 Ontology 构造跨 Metric 研究上下文。
4. 系统每天能稳定输出少量高质量异常，而不是大量噪音。**噪音预算：默认规则配置下，全系统 Alert ≤ 10 条/天**（首个校准靶，M3 调参时按实际误报率修订）。
5. 用户打开首页后，能在几分钟内确定今天最值得继续研究的主题。

---

# 十八、开发原则（必须遵守）

1. **Core 与 Plugin 完全解耦**：Core 不允许出现任何 ETF、股票、BTC、黄金等业务概念。
2. **Everything is Metric**：所有 Detector、Alert 都只依赖 Metric 接口，不依赖具体数据来源。
3. **Plugin 是能力包，不只是数据源**：Plugin 负责定义 Metric、声明 Ontology、采集数据、配置 Detector、构建 Research Context。
4. **Alert 是入口，不是结论**：任何地方都不要输出"建议买入""建议卖出"等投资建议。
5. **先抽象接口，再写实现**：每增加一个 Plugin，先设计接口，再开发 Collector 和 Metric。
6. **MVP 优先**：先让 ETF、Macro、Crypto 三个 Plugin 跑通完整链路，再扩展新的能力。
7. **Ontology 是关系图，不是规则引擎**：Ontology 声明 Entity 之间的结构关系，不包含推理逻辑。关联展开是查询，不是推断。

---

# 十九、Ontology 渐进式构建策略

## Phase 1（MVP）

Plugin 声明已知的 Entity 和 Relation（硬编码）。Research 页面通过 Relation 展开关联指标。不做自动发现，只做已知关系的查询。

## Phase 2（半自动）

基于历史 Metric 数据，自动计算相关性。高相关性对推荐给用户确认："这两个指标相关性 0.85，是否加入 Ontology？"允许用户手动添加 Relation。

## Phase 3（全自动）

基于 Granger Causality、VAR 模型自动发现领先/滞后关系。自动计算 Confidence 和 TypicalLag。人工审核后纳入 Ontology。

---

# 二十、长期愿景

任何开发者都应能够通过新增 Plugin 的方式扩展系统能力——新的数据源、新的领域知识、新的关系模板、新的研究上下文。

未来可能的 Plugin：

- Glassnode Plugin（链上分析）
- Bloomberg Plugin
- Wind Plugin
- Yahoo Plugin
- SEC Plugin（13F 持仓）
- Tushare Plugin
- TradingEconomics Plugin

安装后自动获得：Collector、Metric、Ontology、Detector、Context。

当系统能够持续回答"**今天资本市场发生了哪些以前没有发生的事情，以及这些事情与哪些更深层结构相关**"时，它就具备了真正的资本可观测性能力。

---

# 二十一、Ontology 数据模型参考

## entities 表

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

## relations 表

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

## metric_entities 表

```sql
CREATE TABLE metric_entities (
    metric_id   TEXT NOT NULL,
    entity_id   TEXT NOT NULL REFERENCES entities(id),
    role        TEXT DEFAULT 'observation',
    PRIMARY KEY (metric_id, entity_id)
);
```

## Ontology 查询接口

```go
type OntologyService interface {
    // 给定一个 Entity，返回它的关系图（指定深度）
    GetNeighbors(ctx context.Context, entityID string, depth int) (*EntityGraph, error)

    // 给定一个 Alert，返回关联的 Entity 和 Relation
    GetAlertContext(ctx context.Context, alertID string) (*AlertContext, error)

    // 给定一个 Metric，找到归属 Entity，再展开关系
    GetMetricContext(ctx context.Context, metricID string) (*MetricContext, error)

    // 两个 Entity 之间是否存在关系路径
    FindPath(ctx context.Context, from, to string) ([]Relation, error)
}
```

## AlertContext 输出示例

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
      "relation": {"type": "influences", "confidence": 0.8}
    }
  ],
  "narrative": "黄金ETF资金持续流入，与美元走弱、实际利率下行同步。历史上，类似的ETF资金流领先价格变化约1-2天。"
}
```
