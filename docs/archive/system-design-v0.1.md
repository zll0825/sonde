# Sonde — 系统设计文档

版本：0.1（Draft）
状态：讨论中

---

## 摘要

Sonde 是一个面向资本市场的可观测性平台。本文档定义系统的三层架构——

| 层 | 职责 | 核心问题 |
|---|---|---|
| **Ontology 层** | 资本世界的统一实体/关系模型 | "这个世界由什么组成？它们之间是什么关系？" |
| **Metric 层** | 统一时序观测数据模型 | "每个实体在某个维度上的数值是什么？" |
| **Detect/Alert/Research 层** | 异常发现与研究导航 | "今天哪些数值和平时不一样？这说明了什么？" |

三层依次依赖：Ontology 定义"观测什么"，Metric 定义"怎么观测"，Detect/Alert/Research 定义"观测到异常后怎么办"。

本文档不替代 prdv3.md（产品定义）或 ontology-design.md（Ontology 实现细节），而是阐述三层之间的**关系**和**整体设计决策**。

---

## 一、系统架构全景

```
                         ┌──────────────────────────────────┐
                         │         Research Page            │
                         │    (用户看到的结构化研究入口)       │
                         └──────────────┬───────────────────┘
                                        │ 展开关联上下文
                         ┌──────────────┴───────────────────┐
                         │            Alert                 │
                         │    (异常事件的标准化表达)          │
                         └──────────────┬───────────────────┘
                                        │ 触发
                         ┌──────────────┴───────────────────┐
                         │          Detector                │
                         │    (独立于 Plugin 的检测算法)      │
                         └──────────────┬───────────────────┘
                                        │ 读取
                         ┌──────────────┴───────────────────┐
                         │           Metric                 │
                         │    (统一时序观测数据模型)          │
                         └──────────────┬───────────────────┘
                                        │ 归属
                         ┌──────────────┴───────────────────┐
                         │          Ontology                │
                         │    (实体 + 关系的语义网络)         │
                         └──────────────┬───────────────────┘
                                        │ 声明
                         ┌──────────────┴───────────────────┐
                         │           Plugin                 │
                         │    (领域能力包：采集 + 定义 + 关系) │
                         └──────────────────────────────────┘
```

数据流自底向上：Plugin 声明 Ontology 并采集数据 → Ontology 定义实体关系 → Metric 存储观测值 → Detector 扫描异常 → Alert 触发 → Ontology 提供关联上下文 → Research 页面呈现。

控制流自顶向下：用户看到 Research 页面 ← Ontology 展开关联 ← Alert 引用 Metric ← Detector 读取 Metric 基线。

---

## 二、Ontology 层：定义"世界是什么"

### 2.1 为什么需要这一层

Metric 层的命名规范 `namespace.entity.metric` 解决了"观测值如何命名"的问题（表层语法），但没有解决"观测对象之间有什么关系"的问题（底层语义）。

没有 Ontology 时：

```
gold.etf.net_inflow 异常 → "黄金 ETF 连续流入 15 天"
gold.price 异常          → "黄金价格突破历史高点"

// 两个 Alert 各自独立，系统不知道它们之间存在语义关联
```

有 Ontology 时：

```
gold.etf.net_inflow 异常 → 
  系统知道 gold_etf_flow --[leads]--> gold_etf --[tracks]--> comex_gold
  系统知道 dxy --[causes]--> comex_gold（负向）
  系统知道 fed --[causes]--> us_10y_yield --[correlates]--> comex_gold
  
  → 自动展开跨 Metric 的联动上下文
  → 用户看到的不是"一个 ETF 异常"，而是"黄金生态链上多个环节同时出现的信号"
```

### 2.2 核心概念

**Entity（实体）**：被观测的对象。Metric 归属于 Entity。类型包括：

- `asset`：可交易资产（BTC、现货黄金）
- `instrument`：金融工具（ETF、期货、期权）
- `flow`：资金流（北向资金、ETF 净流入）
- `institution`：机构（美联储、日本央行）
- `indicator`：宏观指标（CPI、PMI、利率）
- `index`：指数（DXY、S&P 500）
- `market`：市场/交易所（A股、美股、韩国）

**Relation（关系）**：实体之间的有向连接。类型包括：

- 因果类：`causes`、`leads`、`signals`
- 相关类：`correlates`、`inversely_correlates`
- 结构类：`component_of`、`tracks`、`hedges`
- 竞争类：`competes`

每个关系带有方向、置信度、典型滞后时间和来源 Plugin。

### 2.3 与 Plugin 的关系

Ontology 是 Plugin 声明的，不是 Core 硬编码的。

```
ETF Plugin：
  声明 Entity：gold_etf, gold_etf_flow, silver_etf...
  声明 Relation：gold_etf_flow --[leads]--> gold_etf

Macro Plugin：
  声明 Entity：dxy, fed, us_10y_yield, cpi...
  声明 Relation：fed --[causes]--> us_10y_yield

// 跨 Plugin 的关系同样可以声明：
// dxy --[causes]--> comex_gold（由 ETF Plugin 或 Macro Plugin 声明）
```

Core 只负责：存储 Entity/Relation、查询关系图、提供图遍历 API。Core 不包含任何金融知识。

### 2.4 设计约束

- **有向图**：`leads` 和 `causes` 都有方向性，方向性是推理的基础
- **置信度可变**：MVP 阶段静态赋值，未来基于历史数据动态校准
- **渐进式构建**：先声明已知关系，再通过统计方法发现新关系，人工审核后纳入
- **Ontology 不替代 Detector**：Detector 负责检测单个 Metric 的统计异常，Ontology 负责异常产生后提供语义关联上下文

---

## 三、Metric 层：定义"怎么观测"

### 3.1 为什么需要这一层

不同市场、不同数据源的原始数据格式完全不同（Bloomberg API 返回的字段 vs Glassnode API 返回的字段 vs SEC EDGAR 的 13F 文件），但在系统中它们必须被规约为同一种观测语言。

Metric 层的核心决策是：**用一个统一的三段式命名规范，把一切观测值标准化。**

### 3.2 核心模型

```
<namespace>.<entity>.<metric>

gold.etf.net_inflow       # 黄金 → ETF 工具 → 净流入
gold.price                # 黄金 → 现货 → 价格
btc.exchange.balance      # BTC → 交易所 → 余额
fed.balance_sheet         # 美联储 → 资产负债表 → 规模
china.northbound.flow     # 中国 → 北向通道 → 资金流
usd.index                 # 美元 → 指数 → 数值
```

每个 Metric 记录：id、name、description、unit、frequency、plugin、observed_entity_id、observed_property、value、timestamp。

### 3.3 与 Ontology 的关系

Metric 的 `observed_entity_id` 字段是两层的桥梁：

```
Metric: gold.etf.net_inflow
  observed_entity_id → "gold_etf_flow"  (Entity)

Entity: gold_etf_flow
  type: flow
  relations:
    --[leads]--> gold_etf
    --[tracks]--> comex_gold
```

这意味着：当 `gold.etf.net_inflow` 触发 Alert 时，系统通过 `observed_entity_id` 找到 `gold_etf_flow`，再通过关系图找到所有相关实体及其 Metric。

### 3.4 设计约束

- Core 不知道 "gold"、"btc"、"etf" 的含义——它只知道 namespace、entity、metric 三个字符串段
- Metric 的全部业务语义来自 Ontology 层的 Entity 定义
- Plugin 负责声明"我有哪些 Metric，每个 Metric 归属于哪个 Entity"

---

## 四、Detect/Alert/Research 层：定义"异常之后怎么办"

### 4.1 为什么需要这层

Ontology 定义了世界结构，Metric 存储了观测值——但用户每天面对的不是这个结构，而是：

> "今天有哪些地方和平时不一样？"

这一层的输入是 Metric 时序数据，输出是 Research 页面。中间经过 Detector（统计检测）和 Ontology Lookup（语义关联）。

### 4.2 处理链路

```
Detector 扫描 Metric
  ↓
  发现 gold.etf.net_inflow 连续 15 天净流入（Trend Detector, 99% 历史分位）
  ↓
  生成 Alert
  ↓
  Ontology Lookup：gold_etf_flow 有哪些邻居？
  ↓
  展开关联 Entity 的当前 Metric 快照
  ↓
  组装 Research 页面
```

### 4.3 Detector 设计原则

Detector 独立于 Plugin，可以作用于任意 Metric。检测的是**统计属性**，不是**业务属性**：

- Threshold Detector：值是否超过阈值
- Percentile Detector：值是否处于历史极值
- Trend Detector：是否存在连续单向变化
- Volatility Detector：波动率是否突增
- Moving Average Detector：是否偏离均值

Detector 不关心被检测的实体是"黄金"还是"BTC"——它只知道这是一个时间序列。

### 4.4 Alert 设计原则

Alert 是异常事件的标准化表达，不是投资结论。包含：

- 异常标题和概要
- 严重级别（Critical / Warning / Info）
- 触发 Metric 和 Detector
- 时间窗口
- 去重键（避免同一异常重复告警）
- 证据（当前值、历史分位等）

### 4.5 Research 页面生成逻辑

Research 页面的生成来自两个来源的合并：

1. **Plugin 提供的默认领域上下文**：该 Plugin 声明的"这个异常类型通常需要展开哪些指标"
2. **Ontology 提供的跨实体关系扩展**：通过 Entity 关系图找到所有相关实体及其当前 Metric 快照

两个来源合并后，Research 页面展示：
- 异常描述
- 历史变化
- 关联 Metric（来自 Plugin 默认上下文 + Ontology 扩展）
- 关联实体与资产
- 相关新闻
- 时间线
- 历史对比

---

## 五、Core 与 Plugin 的责任边界

### 5.1 Core 负责

```
core/
  metric/          # Metric 注册、存储、查询（只认识三段式命名）
  ontology/        # Entity/Relation 存储、图查询、AlertContext 构建
  detector/        # 检测算法（只读 Metric，不关心业务含义）
  alert/           # Alert 生命周期管理
  research/        # Research 页面组装（调用 Plugin Context Builder + Ontology Lookup）
  scheduler/       # 采集调度、检测调度
```

Core 的核心约束：不出现任何具体金融概念。不出现 "gold"、"BTC"、"ETF"、"northbound" 等字符串——这些只存在于 Plugin 的声明中。

### 5.2 Plugin 负责

每个 Plugin 是一个完整的领域能力包：

```
plugin/
  etf/
    collector.go       # 数据采集
    metrics.go         # Metric 声明
    ontology.go        # Entity + Relation 声明
    detectors.go       # 默认 Detector 配置
    context.go         # Research 上下文模板
```

Plugin 的接口：

```go
type Plugin interface {
    // 身份
    Name() string
    
    // Metric 声明
    Metrics() []MetricDeclaration
    
    // Ontology 声明
    Entities() []EntityDeclaration
    Relations() []RelationDeclaration
    
    // 数据采集
    Collect(ctx context.Context) ([]MetricSnapshot, error)
    
    // 默认检测器配置
    DefaultDetectors() []DetectorConfig
    
    // Research 上下文生成
    BuildContext(ctx context.Context, alert Alert, ontology *OntologyService) (*ResearchContext, error)
}
```

### 5.3 依赖方向

```
Plugin → Core（Plugin 依赖 Core 提供的接口和抽象）
Core → Plugin（Core 通过接口调用 Plugin，不依赖具体实现）
```

编译期 Core 不 import Plugin。运行期通过注册机制加载。

---

## 六、一条完整的用户旅程

以"黄金 ETF 异常"为例，贯穿三层：

### 6.1 系统侧

```
1. ETF Plugin 启动时
   → 向 Core 注册 Metric：gold.etf.net_inflow, gold.etf.aum, gold.price...
   → 向 Core 注册 Entity：gold_etf_flow, gold_etf, comex_gold, spot_gold
   → 向 Core 注册 Relation：gold_etf_flow --[leads]--> gold_etf,
                            gold_etf --[tracks]--> comex_gold

2. Macro Plugin 启动时
   → 向 Core 注册 Entity：dxy, fed, us_10y_yield
   → 向 Core 注册 Relation：fed --[causes]--> us_10y_yield,
                            dxy --[causes]--> comex_gold

3. 每日采集
   → ETF Plugin.Collect() → 写入 gold.etf.net_inflow = +850M（连续第 15 天为正）
   → Macro Plugin.Collect() → 写入 dxy.value = 103.2

4. 每日检测
   → Trend Detector 扫描 gold.etf.net_inflow 的 90 天窗口
   → 发现：连续 15 天净流入，处于历史 99% 分位
   → 生成 Alert：{title: "黄金ETF连续流入15天", severity: Critical}

5. Ontology 展开
   → OntologyService.GetAlertContext(alertID)
   → 从 gold.etf.net_inflow 的 observed_entity_id 找到 gold_etf_flow
   → 遍历邻居：gold_etf（leads）、comex_gold（tracks）
   → 继续遍历：dxy（causes comex_gold）、us_10y_yield（correlates comex_gold）
   → 抓取所有邻居实体的当前 Metric 快照

6. Research 组装
   → Plugin Context Builder 提供黄金 ETF 默认研究上下文
   → Ontology Lookup 提供跨实体扩展
   → 合并生成 Research 页面
```

### 6.2 用户侧

```
用户打开首页 Capital Radar

看到：
  🚨 Critical — 黄金ETF连续流入15天（历史99%分位）
  ⚠️ Warning  — 美元指数跌破半年趋势线
  ⚠️ Warning  — BTC交易所余额创五年新低

点击"黄金ETF连续流入15天" →

Research 页面自动展示：
  异常描述：连续15天净流入，累计+$12B，处于历史99%分位
  相关指标变化：
    - 黄金ETF价格：过去15天 +3.1%
    - COMEX黄金期货：过去15天 +2.8%
    - 美元指数：过去15天 -1.5%（负相关信号）
    - 美国10年期收益率：过去15天 -12bp
    - COMEX黄金库存：过去30天 -5%
  关系图谱：
    美联储 → 美债收益率 → 黄金 ← 美元指数
                           ↑
                    黄金ETF ← 资金持续流入
  历史对比：当前连续流入天数在历史上处于什么位置
  相关新闻："Fed暗示9月可能降息"...

用户自己判断动机：
  这是利率预期驱动的？还是避险情绪驱动的？
  决定是否进一步研究黄金相关机会。
```

**关键：系统在整个链路中从未告诉用户"应该买黄金"。它只是把"异常"和"关联证据"组织好，交到用户面前。**

---

## 七、MVP 范围与演进路径

### 7.1 MVP（Phase 1）

范围：跑通三层完整链路，但不追求覆盖全部市场。

```
Ontology 层：
  - 静态 Entity/Relation（Plugin 硬编码声明）
  - 关系图查询（GetNeighbors, GetAlertContext）
  - 3 个 Plugin（ETF, Crypto, Macro）的 Entity/Relation

Metric 层：
  - 三段式命名规范
  - TimescaleDB 存储时序数据
  - 每日采集

Detect/Alert/Research 层：
  - 3 种 Detector（Threshold, Percentile, Trend）
  - Capital Radar 首页
  - 基础 Research 页（Plugin 上下文 + Ontology 扩展）
```

### 7.2 Phase 2

```
- 基于历史 Metric 数据自动计算相关性
- 高相关性对推荐给用户确认："这两个指标相关性 0.85，是否加入关系图？"
- 支持用户手动添加/编辑 Relation
- 更多 Plugin（13F, Options, Bond, Commodity）
```

### 7.3 Phase 3

```
- 基于 Granger Causality 自动发现领先/滞后关系
- 动态 Confidence（基于滚动窗口重算）
- 关系在不同市场状态下的条件性（regime-dependent relations）
- 异常聚合（同一资金逻辑触发多个 Metric 异常时，聚合为一个 Narrative）
```

---

## 八、核心设计决策汇总

| 决策 | 选择 | 理由 |
|---|---|---|
| 统一语言的形式 | `namespace.entity.metric` 三段式 + Ontology 图 | 表层统一命名，底层统一语义 |
| Ontology 由谁定义 | Plugin 声明，Core 存储 | Core 不含金融知识，Plugin 是领域能力的完整封装 |
| 关系的方向性 | 有向图（leads, causes, signals 等） | 方向性是推理预测的基础 |
| Detector 与 Asset 的关系 | 完全解耦 | Detector 只读 Metric 时间序列，不关心业务含义 |
| Alert 的定位 | 研究入口，不是结论 | 产品边界：发现异常 vs 提供建议 |
| Research 上下文的来源 | Plugin 默认 + Ontology 扩展 | 领域知识 + 跨领域关系，两者互补 |
| 构建策略 | 渐进式：静态 → 半自动 → 全自动 | 先确定架构正确，再逐步自动化 |

---

## 九、与其他文档的关系

| 文档 | 内容 | 本设计文档的角色 |
|---|---|---|
| prdv3.md | 产品定位、原则、用户故事、MVP 范围 | 本设计文档是 prdv3 的架构深化 |
| ontology-design.md | Ontology 层实现细节（Go 接口、SQL Schema、API 定义） | 本设计文档定义三层关系，ontology-design.md 是 Ontology 层的详细实现 |
