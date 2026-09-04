# PRD v1.0

# Sonde

**一个面向资本市场的可观测性平台（Capital Observability Platform）**

版本：0.1（MVP）
作者：ChatGPT & Zhu Liangliang
状态：Draft

---

# 1. 项目背景

## 为什么做

个人投资者每天面对的信息量已经远远超过个人处理能力。

每天都会产生：

* 股票行情
* ETF资金流
* 13F持仓变化
* Crypto链上数据
* 美联储数据
* 国债收益率
* 外汇
* 商品
* 新闻
* 社交媒体

真正的问题不是：

> **没有数据。**

而是：

> **不知道今天应该关注什么。**

Bloomberg、Wind、TradingView 已经很好地解决了"查询数据"的问题。

本项目解决的是另一个问题：

> **自动发现资本市场今天哪些地方出现了异常。**

它不是一个数据平台。

不是一个行情平台。

不是一个荐股平台。

而是：

> **资本市场的可观测性平台。**

---

# 2. 产品定位

一句话：

> **持续监控资本市场，自动发现异常变化，帮助投资者第一时间发现值得研究的机会。**

产品永远不回答：

* 买什么
* 卖什么
* 能涨多少

产品只回答：

> **今天哪些地方和平时不一样？**

---

# 3. 产品原则

## Principle 1

发现异常

而不是预测未来。

---

## Principle 2

提供研究入口

而不是投资结论。

---

## Principle 3

Metric First

整个系统只认识 Metric。

不认识：

* ETF
* 股票
* BTC
* 黄金

这些都属于 Plugin。

---

## Principle 4

Plugin First

所有市场知识全部插件化。

Core 永远保持极简。

---

# 4. 用户画像

第一阶段用户：

就是作者本人。

特点：

* 有一定投资经验
* 每天关注市场
* 不知道今天应该重点关注什么
* 希望第一时间发现资本异动

以后再扩展：

* 主动投资者
* 量化研究员
* 基金经理
* Macro Trader

---

# 5. 用户故事（User Story）

每天早上打开系统。

首页直接告诉我：

今天值得关注的异常。

例如：

```
🚨 黄金ETF连续流入15天

🚨 BTC交易所余额创五年新低

🚨 韩国外资连续净买入10天

🚨 日本银行ETF成交量突破历史极值
```

我点击其中一项。

立即进入研究页面。

快速了解：

为什么发生。

还有哪些指标一起变化。

之后由我自己决定：

是否值得投资。

---

# 6. 产品目标

帮助投资者：

**管理注意力。**

而不是：

管理资产。

---

# 7. 整体架构

```
Plugin

↓

Collect

↓

Metric

↓

Detect

↓

Alert

↓

Ontology Lookup    ← 实体关系关联

↓

Research
```

---

# 8. Core Architecture

Core 不包含任何金融知识。

Core 只负责：

```
Metric

↓

Baseline

↓

Detector

↓

Alert

↓

Ontology Lookup
```

Core 永远不知道：

什么叫 BTC。

什么叫 ETF。

什么叫黄金。

---

# 9. Plugin Architecture

Plugin 才是真正的业务。

例如：

```
ETF Plugin

Crypto Plugin

Macro Plugin

News Plugin

Bond Plugin

Commodity Plugin

13F Plugin

Options Plugin
```

每个 Plugin 提供：

## Data Collector

负责采集数据。

例如：

ETF Plugin：

* ETF资金流
* ETF规模
* ETF价格

---

## Metric Definition

定义有哪些 Metric。

例如：

```
gold.etf.net_inflow

gold.etf.aum

gold.price
```

Crypto：

```
btc.exchange.balance

btc.realized_price

eth.staking_ratio
```

---

## Default Detector

告诉系统：

哪些异常适合这个领域。

例如：

BTC：

连续下降

黄金：

资金持续流入

ETF：

历史分位

等等。

---

## Context Builder

点击 Alert 后：

默认展示哪些关联指标。

例如：

黄金：

自动展开：

* COMEX
* DXY
* 美债
* 黄金矿业
* 相关新闻

---

## Ontology Provider

声明本 Plugin 拥有的 Entity 和 Relation。

例如：

ETF Plugin 声明：

* gold_etf（instrument）→ leads → gold_etf 价格
* gold_etf_flow（flow）→ leads → gold_etf
* gold_etf → tracks → comex_gold

这些关系在 Alert 触发后，驱动 Research 页面的关联展开。

---

Plugin = 一个完整的领域能力包（Capability Package）。

---

# 10. Metric

Metric 是整个系统最核心的数据模型。

建议统一格式：

```
<namespace>.<entity>.<metric>
```

例如：

```
gold.etf.net_inflow

gold.price

btc.exchange.balance

btc.hashrate

fed.balance_sheet

usd.index

nasdaq.pe

china.northbound.flow
```

每个 Metric 至少包含：

```
id

name

description

unit

frequency

plugin

tags

value

timestamp
```

全部进入 Time Series Database。

---

## 10.1 Ontology（实体关系层）

Metric 命名规范是表层语法，Ontology 定义底层语义。

Metric 是观测值。Ontology 是观测对象之间的结构。

### 问题

`gold.etf.net_inflow` 和 `gold.price` 同属 gold namespace，但系统如何知道 ETF 资金流可能领先金价变化？

`china.northbound.flow` 和 `usd.index` 在不同市场，系统如何认识它们之间的关联？

没有 Ontology，Alert 是孤立的。有了 Ontology，Alert 是一张网的入口。

### 核心模型

**Entity（实体）**：被观测的对象，Metric 依附于 Entity。

```
Entity:
  id:           string
  name:         string
  namespace:    string
  entity_type:  EntityType    # asset / index / instrument / flow / institution / indicator / market
  tags:         []string
  metadata:     map
```

**Relation（关系）**：两个 Entity 之间的有向连接。

```
Relation:
  source:        string        # 起始 Entity
  target:        string        # 目标 Entity
  relation_type: RelationType  # 见下方
  direction:     Direction     # forward / backward / bidirectional
  confidence:    float         # 0-1
  typical_lag:   Duration      # 典型滞后时间
  description:   string
```

### RelationType

```
因果类：  causes / leads / signals
相关类：  correlates / inversely_correlates
结构类：  component_of / tracks / hedges
竞争类：  competes
```

### Ontology 在系统中的位置

```
Plugin 声明 Entity + Relation
  ↓
Collect → Metric
  ↓
Detect → Alert
  ↓
Ontology Lookup  ← Alert 后介入
  ↓
Research Page（展开关联 Entity + Relation）
```

### 示例：黄金生态

```
Entities:
  comex_gold        (asset)
  gold_etf          (instrument)
  gold_etf_flow     (flow)
  dxy               (index)
  us_10y_yield      (indicator)
  fed               (institution)

Relations:
  gold_etf_flow  →leads→    gold_etf       # 资金流领先ETF价格
  gold_etf       →tracks→   comex_gold     # ETF追踪期货
  dxy            →causes→   comex_gold     # 美元影响金价
  us_10y_yield   →correlates→ comex_gold   # 利率影响金价
  fed            →causes→   us_10y_yield   # 美联储影响利率
```

### Alert 关联推理

当 Alert「黄金ETF连续流入15天」触发时，系统查询 Ontology：

```
gold_etf_flow →[leads]→ gold_etf
comex_gold ←[tracks]← gold_etf
dxy →[causes]→ comex_gold
```

自动展开：美元指数过去15天下降3.2%，美债收益率下降12bp。用户自行判断：这是利率驱动还是避险情绪。

### Plugin 接口

```go
type OntologyProvider interface {
    Entities() []EntityDeclaration
    Relations() []RelationDeclaration
}
```

### 设计原则

1. Ontology 是 Plugin 声明的，Core 不包含任何金融知识
2. Ontology 是有向图，方向性是推理的基础
3. Ontology 支持但不替代 Detector——Detector 检测异常，Ontology 提供关联上下文
4. MVP 只做已知关系的查询，未来逐步自动发现

详细设计见 `docs/ontology-design.md`。

---

# 11. Detector

Detector 独立于 Plugin。

任何 Detector 可以作用于任何 Metric。

MVP 支持：

## Threshold Detector

超过阈值。

---

## Percentile Detector

历史95%

历史99%

---

## Trend Detector

连续上涨

连续下跌

---

## Volatility Detector

波动率突然增加。

---

## Moving Average Detector

偏离均值。

---

未来：

Correlation

Seasonality

Machine Learning

全部可以新增。

---

# 12. Alert

Alert 包含：

```
Title

Summary

Severity

Metric

Detector

Timestamp

Plugin

Context
```

例如：

```
Title

黄金ETF连续流入15天

Summary

当前连续流入达到历史99%分位

Severity

Critical
```

---

# 13. Research Page

Alert 点击进入。

页面自动生成：

```
异常说明

↓

相关指标

↓

关联资产

↓

相关新闻

↓

时间线

↓

历史对比
```

Research 页面永远不出现：

建议买入。

---

# 14. 首页

首页只有一件事情：

Capital Radar

例如：

```
Critical

Gold ETF

连续流入15天

Warning

BTC

交易所余额创新低

Warning

日本银行ETF

成交量异常

Info

美元指数

突破半年趋势
```

用户不需要搜索。

系统主动发现。

---

# 15. MVP

第一阶段：

Plugin：

* ETF
* Macro
* Crypto

Detector：

* Threshold
* Percentile
* Trend

Ontology：

* 已知关系查询（Plugin 声明）
* Alert 关联展开（Research 页面）

Research：

简单版。

Alert：

首页列表。

即可。

---

# 16. 不做什么

第一阶段：

不做：

* AI预测
* AI荐股
* 自动交易
* 策略回测
* K线分析
* 技术指标推荐

全部不做。

---

# 17. 技术架构建议

Backend：

Go

---

Database：

PostgreSQL

---

Time Series：

TimescaleDB

（PostgreSQL Extension）

---

Cache：

Redis

---

Message Queue：

NATS

---

Workflow：

Temporal

（后期）

---

Search：

OpenSearch

（Research 使用）

---

Frontend：

Next.js

React

Tailwind

---

Chart：

ECharts

---

Deploy：

Docker

Kubernetes

---

# 18. 推荐目录结构

```
sonde/

cmd/

core/

    metric/

    detector/

    alert/

    ontology/          # 实体关系图谱：Entity, Relation, 查询服务

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

Core 永远不依赖 Plugin。

Plugin 依赖 Core。

---

# 19. 长期愿景

未来。

任何开发者都可以开发自己的 Plugin。

例如：

```
Glassnode Plugin

Bloomberg Plugin

Wind Plugin

Yahoo Plugin

SEC Plugin

Tushare Plugin

TradingEconomics Plugin
```

安装后。

自动获得：

* Collector
* Metric
* Detector
* Context

整个系统不断成长。

---

# 20. Mission

> **Monitor Capital Metrics. Detect Capital Anomalies.**

持续监控资本指标。

自动发现资本异常。

帮助投资者第一时间发现值得研究的机会。

---

# 21. 给 Codex 的开发原则（必须遵守）

为了保证架构长期可扩展，开发过程中必须遵守以下原则：

1. **Core 与 Plugin 完全解耦**：Core 不允许出现任何 ETF、股票、BTC、黄金等业务概念。
2. **Everything is Metric**：所有 Detector、Alert 都只依赖 Metric 接口，不依赖具体数据来源。
3. **Plugin 是能力，不只是数据源**：Plugin 负责定义 Metric、采集数据、默认 Detector、Ontology、Research Context。
4. **Alert 是入口，不是结论**：任何地方都不要输出“建议买入”“建议卖出”等投资建议。
5. **先抽象接口，再写实现**：每增加一个 Plugin，先设计接口，再开发 Collector 和 Metric。
6. **MVP 优先**：先让 ETF、Macro、Crypto 三个 Plugin 跑通完整链路（Collect → Metric → Detect → Alert → Research），再扩展新的能力。
7. **Ontology 是关系图，不是规则引擎**：Ontology 声明 Entity 之间的结构关系，不包含推理逻辑。关联展开是查询，不是推断。
