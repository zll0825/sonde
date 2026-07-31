# Capital Observatory PRD v3

## 一、产品简介

### 产品名称

**Capital Observatory**

### 一句话定位

> 一个面向资本市场的可观测性平台，持续监控全球资本指标，自动发现异常变化，并把用户带到最值得研究的线索上。[cite:75][cite:91]

### 产品使命

> Monitor Capital Metrics. Detect Capital Anomalies.[cite:75]

---

## 二、产品背景

现代投资的主要矛盾，不是没有数据，而是注意力不足。市场每天都在产生大量股票、ETF、债券、商品、外汇、Crypto、宏观、新闻和链上数据，但个人研究者无法在有限时间内判断“今天应该先看什么”。[cite:75][cite:91]

现有工具更擅长回答“某个资产现在怎么样”，而本产品要回答的是“今天资本市场有哪些地方出现了异常，值得优先研究”。产品不替用户做判断，而是替用户完成异常发现与研究入口组织。[cite:75][cite:91]

---

## 三、产品目标

Capital Observatory 的核心目标只有三个：

- 持续观察资本市场中的关键指标。[cite:91]
- 自动发现偏离正常状态的异常变化。[cite:75][cite:91]
- 为异常提供结构化研究入口，而不是投资结论。[cite:75][cite:91]

产品明确不负责：

- 投资建议。
- 买卖推荐。
- 收益预测。
- 自动交易。
- 策略回测。[cite:75][cite:91]

---

## 四、目标用户

### MVP 用户

MVP 阶段服务对象是具备一定投资经验、愿意自己研究、但每天面对海量市场信息时不知道该优先关注什么的个人投资者；在当前阶段，这个用户首先就是作者本人。[cite:75][cite:91]

### 用户需求

用户真正需要的不是更多数据面板，而是一个每天打开后能直接看到“今天新增了哪些重要异常”的系统。用户希望先被告知异常，再决定是否进入更深的研究。[cite:75][cite:91]

---

## 五、产品原则

### Principle 1：异常优先

系统优先输出异常，而不是输出全部信息流。首页的默认视角必须是“今天哪些地方和平时不一样”。[cite:75][cite:91]

### Principle 2：研究优先

系统提供研究入口，不提供推荐结论。任何页面都不应输出“建议买入”“建议卖出”等投资建议。[cite:75][cite:91]

### Principle 3：Metric First

系统中的所有检测、告警与研究入口，首先都围绕 Metric 组织。资产类别、市场类别、数据源类别都不能直接侵入 Core 逻辑。[cite:75]

### Principle 4：Ontology Under Metric

Metric 是观测值的统一表层语法，但系统还需要在 Metric 之下定义观测对象及其关系结构。系统不仅要知道“某个指标异常了”，还要知道“它和哪些实体、渠道、因子、市场存在结构关系”。[cite:91]

### Principle 5：Plugin First

金融世界的领域知识通过 Plugin 注入，而不是写死在 Core 中。每个 Plugin 不只是数据接入器，也是一组领域能力包。[cite:75]

---

## 六、产品核心理念

Capital Observatory 借鉴的是软件可观测性思想。就像监控系统不会先问“某台服务器值不值得买”，而是先问“哪台服务器状态异常”，Capital Observatory 也不会先问“哪个资产会涨”，而是先问“哪个资本指标偏离了正常状态”。[cite:91]

因此，本产品本质上不是行情终端，也不是投顾系统，而是一套资本市场异常发现与研究导航系统。[cite:75][cite:91]

---

## 七、产品能力

系统提供五项基础能力。

### 1. Collect

持续从多个市场、多个来源采集资本市场相关数据。[cite:75][cite:91]

### 2. Observe

将原始数据标准化为统一的 Metric，并持续记录其时间序列变化。[cite:75][cite:91]

### 3. Relate

在 Metric 之下维护统一的 Capital Ontology，定义实体、渠道、资产、市场、因子之间的结构关系，用于支持跨 Metric 的联动理解。[cite:91]

### 4. Detect

基于历史基线和检测规则自动识别异常，并生成 Alert。[cite:75][cite:91]

### 5. Research

围绕 Alert 自动展开关联指标、相关资产、时间线与背景信息，帮助用户快速进入研究。[cite:75][cite:91]

---

## 八、系统流程

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

用户每天打开系统时，看到的不是全部市场数据，而是经过异常检测和关系扩展后的重点事件列表。[cite:75][cite:91]

---

## 九、统一数据抽象

### 9.1 Metric

Metric 是系统最核心的观测单位。建议命名格式保持为：

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

每个 Metric 至少包含以下字段：

- `id`
- `name`
- `description`
- `unit`
- `frequency`
- `plugin`
- `tags`
- `observed_entity_id`
- `observed_property`
- `value`
- `timestamp` [cite:75]

### 9.2 Ontology

在 Metric 之下，系统维护一层统一语义模型，用来定义“观测对象是什么”以及“对象之间的关系是什么”。这层模型解决的问题包括：

- `gold.etf.net_inflow` 与 `gold.price` 为什么可能有关。
- `china.northbound.flow` 与 `usd.index` 在不同时间窗口下是否存在结构关系。
- 一个异常发生后，系统应该沿着哪些关系自动展开研究上下文。[cite:91]

Ontology 至少包含以下对象类型：

- `Asset`
- `Vehicle`
- `Market`
- `Actor`
- `Jurisdiction`
- `Factor`
- `Channel`
- `Indicator` [cite:91]

Ontology 至少包含以下关系类型：

- `tracks`
- `flows_into`
- `flows_out_of`
- `exposed_to`
- `influences`
- `correlates_with`
- `leads`
- `lags`
- `depends_on_regime`
- `hedges` [cite:91]

### 9.3 Alert

Alert 是异常事件的标准化表达，而不是投资结论。每个 Alert 至少包含：

- `title`
- `summary`
- `severity`
- `metric_id`
- `detector_id`
- `timestamp`
- `window`
- `plugin`
- `context`
- `dedup_key`
- `evidence` [cite:75][cite:91]

---

## 十、Core 与 Plugin 架构

### 10.1 Core 负责什么

Core 不包含具体金融资产知识。Core 只负责以下通用能力：

- Metric registry
- Ontology registry
- Baseline engine
- Detector engine
- Alert engine
- Research assembler
- Scheduler [cite:75][cite:91]

Core 永远不应该直接知道什么叫 BTC、黄金、ETF 或北向资金；它只认识统一接口和统一抽象。[cite:75]

### 10.2 Plugin 负责什么

Plugin 是领域能力包，每个 Plugin 至少提供：

- Data Collector
- Metric Definition
- Ontology Definition
- Relationship Templates
- Default Detector
- Context Builder [cite:75][cite:91]

MVP 首批 Plugin：

- ETF Plugin
- Crypto Plugin
- Macro Plugin [cite:75][cite:91]

---

## 十一、检测系统

Detector 独立于具体 Plugin，可以作用于任意 Metric。MVP 支持：

- Threshold Detector
- Percentile Detector
- Trend Detector
- Volatility Detector
- Moving Average Detector [cite:75]

为了避免首页被噪音淹没，异常检测需要至少支持以下机制：

- 时间窗口定义。
- 基线定义。
- 告警去重。
- 严重级别分层。
- 同类异常聚合。 [cite:91]

---

## 十二、首页：Capital Radar

首页只做一件事情：按优先级展示今天最值得关注的异常。[cite:75][cite:91]

首页排序至少综合以下因素：

- Severity。
- 新颖性。
- 历史分位极端程度。
- 跨 Metric 关联范围。
- 用户关注域匹配度。 [cite:91]

首页不是搜索入口，也不是全市场行情页，而是异常雷达页。[cite:75][cite:91]

---

## 十三、研究页面

研究页面围绕一次 Alert 展开，不直接给出投资结论。页面至少包含：

- 异常描述
- 历史变化
- 关联 Metric
- 关联实体
- 相关资产
- 相关新闻
- 时间线
- 历史对比 [cite:75][cite:91]

Research 页的生成逻辑应来自两部分：

- Plugin 提供的默认领域上下文。
- Ontology 提供的跨实体关系扩展。 [cite:75][cite:91]

---

## 十四、MVP 范围

第一阶段只要求跑通完整链路，不追求覆盖全部市场。[cite:75][cite:91]

MVP 范围如下：

### 数据域

- ETF
- Crypto
- Macro [cite:75][cite:91]

### 检测器

- Threshold
- Percentile
- Trend [cite:75][cite:91]

### 页面

- Capital Radar 首页
- Alert 详情页
- 基础版 Research 页 [cite:75][cite:91]

### 必须完成的系统闭环

```text
Collect → Metric → Ontology → Detect → Alert → Research
```

---

## 十五、不在 MVP 范围

以下能力不在第一阶段范围内：

- AI 投资建议
- AI 荐股
- 自动交易
- 策略回测
- 技术指标推荐
- 收益预测
- 风险评分 [cite:75][cite:91]

---

## 十六、技术架构建议

建议技术栈：

- Backend：Go
- Database：PostgreSQL
- Time Series：TimescaleDB
- Cache：Redis
- Message Queue：NATS
- Workflow：Temporal（后期）
- Search：OpenSearch
- Frontend：Next.js + React + Tailwind
- Chart：ECharts
- Deploy：Docker / Kubernetes [cite:75]

推荐目录结构：

```text
capital-observatory/
  cmd/
  core/
    metric/
    ontology/
    detector/
    alert/
    research/
    scheduler/
  plugin/
    etf/
    crypto/
    macro/
    news/
  pkg/
  internal/
  configs/
  api/
  web/
  docs/
```

Core 依赖抽象，不依赖 Plugin；Plugin 依赖 Core 提供的接口与协议。[cite:75]

---

## 十七、成功标准

MVP 阶段的成功不以覆盖多少资产衡量，而以以下标准衡量：

- 系统能稳定采集并更新三类数据源。
- 系统能把异构数据统一成 Metric。
- 系统能通过 Ontology 构造跨 Metric 研究上下文。
- 系统每天能稳定输出少量高质量异常，而不是大量噪音。
- 用户打开首页后，能在几分钟内确定今天最值得继续研究的主题。 [cite:75][cite:91]

---

## 十八、长期愿景

长期来看，任何开发者都应能够通过新增 Plugin 的方式扩展系统能力，包括新的数据源、新的领域知识、新的关系模板和新的研究上下文。[cite:75]

当系统能够持续回答“今天资本市场发生了哪些以前没有发生的事情，并且这些事情与哪些更深层结构相关”这个问题时，它就具备了真正的资本可观测性能力。[cite:91]
