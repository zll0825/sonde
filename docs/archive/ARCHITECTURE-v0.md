# Capital Observatory Architecture

## 一、系统总览

```
┌─────────────────────────────────────────────────────────────────┐
│                         Web Frontend                            │
│                    Capital Radar / Research                      │
└─────────────────────────────────────────────────────────────────┘
                                 │
                                 ▼
┌─────────────────────────────────────────────────────────────────┐
│                           Core                                  │
│  ┌───────────┐  ┌───────────┐  ┌───────────┐  ┌───────────┐    │
│  │ Collector │→ │  Observer │→ │  Detector │→ │  Research  │   │
│  │  (调度)    │  │  (时序存储) │  │  (异常检测) │  │  (上下文)   │  │
│  └───────────┘  └───────────┘  └───────────┘  └───────────┘    │
│        ↑              ↑             ↑              ↑            │
│        └──────────────┴─────────────┴──────────────┘            │
│                          Plugin Interface                        │
└─────────────────────────────────────────────────────────────────┘
        ↑              ↑             ↑              ↑
   ┌────┴────┐    ┌────┴────┐   ┌────┴────┐    ┌────┴────┐
   │ETF Plugin│    │Crypto   │   │Macro    │    │Future   │
   │         │    │Plugin   │   │Plugin   │    │Plugins  │
   └─────────┘    └─────────┘   └─────────┘    └─────────┘
```

**核心约束**：
- Core 不包含任何金融概念（不认识 BTC、黄金、ETF）
- Core 只认识五种抽象：`Metric`、`Detector`、`Alert`、`Relation`、`ResearchContext`
- 所有领域的业务知识由 Plugin 提供

---

## 二、数据流

```
Plugin
  │
  │  提供 Metric 定义 + 采集逻辑
  ▼
Collect（调度层）
  │
  │  按频率调度采集任务
  │  产出：DataPoint { metric_id, value, timestamp }
  ▼
Observer（指标存储层）
  │
  │  写入时序数据库
  │  维护 Metric 元数据
  ▼
Detector（检测层）
  │
  │  加载 Metric 最新值
  │  加载该 Metric 关联的 Detector 配置
  │  运行检测算法
  │  产出：Alert { metric_id, severity, evidence }
  ▼
Research（研究层）
  │
  │  接收 Alert
  │  调用 Plugin 注册的 ContextBuilder
  │  聚合关联指标、历史数据、新闻事件
  │  产出：ResearchView
  ▼
Alert Store / API → Frontend Capital Radar
```

---

## 三、Core 层设计

### 3.1 Core 的五个核心抽象

```go
// Metric 是最小的数据单元
type Metric struct {
    ID          string            // 全局唯一 ID，如 "etf.gold.net_inflow"
    Name        string            // 可读名称
    Description string
    Unit        string            // 单位：USD / BTC / % / count
    Frequency   time.Duration     // 采集频率
    Plugin      string            // 来源插件
    Tags        map[string]string // 元信息标签
    CreatedAt   time.Time
}

// DataPoint 是一个观测值
type DataPoint struct {
    MetricID  string
    Value     float64
    Timestamp time.Time
}

// Detector 定义一种异常检测逻辑
type Detector struct {
    ID          string            // 全局唯一 ID
    Type        DetectorType      // threshold / trend / percentile / ...
    MetricID    string            // 绑定的 Metric
    Parameters  map[string]any    // 检测器参数（因类型而异）
    Severity    Severity          // 触发时的告警级别
    Enabled     bool
}

// Alert 是一次异常事件
type Alert struct {
    ID          string
    DetectorID  string
    MetricID    string
    Severity    Severity
    Title       string
    Summary     string
    Evidence    map[string]any    // 检测器产出的证据
    Timestamp   time.Time
    Status      AlertStatus       // active / acknowledged / resolved
}

// ResearchContext 描述一个 Alert 该怎么展开研究
type ResearchContext struct {
    AlertID       string
    RelatedMetrics []string       // 需要同时展示的指标
    RelatedAssets []string       // 关联资产
    NewsKeywords  []string       // 新闻搜索关键词
    TimeRange     time.Duration  // 时间线范围
}
```

### 3.2 Core 层模块职责

| 模块 | 职责 | 暴露给 Plugin 的接口 |
|------|------|---------------------|
| **Scheduler** | 定时触发采集任务 | `RegisterCollector(metricID, interval, fn)` |
| **MetricStore** | Metric 元数据 CRUD + DataPoint 读写 | `WriteDatapoints([]DataPoint)` |
| **DetectorEngine** | 加载 Detector 配置、执行检测、生成 Alert | `RegisterDetector(detector)` |
| **AlertStore** | Alert 持久化、状态管理 | `GetActiveAlerts()` |
| **ResearchEngine** | 根据 Alert 调用插件构建上下文 | `BuildResearchView(alert) ResearchView` |

### 3.3 Core 不知道什么

- 不知道 `gold.etf.net_inflow` 是黄金 ETF 资金流入，只知道它是一个 metric_id
- 不知道 `percentile` 检测器里用的是 95 还是 99，只知道它有参数表
- 不知道 Alert 相关的资产意味着什么，只知道需要展示哪些 metric_id

---

## 四、Plugin 层设计

### 4.1 Plugin 接口

每个 Plugin 必须实现：

```go
type Plugin interface {
    // 插件自身信息
    Info() PluginInfo

    // 注册所有能提供的 Metric 定义
    DefineMetrics() []MetricDef

    // 实现采集逻辑（被 Scheduler 调度）
    Collect(ctx, metricID) ([]DataPoint, error)

    // 注册默认的 Detector 配置
    DefineDetectors() []DetectorDef

    // 注册跨 Metric 的关联关系（可选，MVP 可跳过）
    DefineRelations() []RelationDef

    // 给定一个 Alert，生成研究页面所需的上下文
    BuildResearchContext(alert Alert) ResearchContext
}
```

### 4.2 Plugin 注册机制（MVP）

MVP 阶段 Plugin 直接编译进主程序，通过静态注册：

```go
// plugin/registry.go
var registry = map[string]Plugin{
    "etf":    etf.New(),
    "crypto": crypto.New(),
    "macro":  macro.New(),
}
```

启动时遍历 registry，让每个 Plugin 注册自身的 Metric、Detector、Relation。

### 4.3 一个 Plugin 的内部结构（以 ETF 为例）

```
plugin/etf/
  plugin.go           // 实现 Plugin 接口
  metrics.go          // Metric 定义列表
  collector.go        // 数据采集逻辑
  detector.go         // 默认检测器定义
  context.go          // Research 上下文构建
  sources/            // 数据源适配器
    etfconnect.go
    yahoofinance.go
    tushare.go
```

---

## 五、采集系统

### 5.1 采集调度

```
                  ┌─── Collector A (ETF, freq=1h)
Scheduler ────────┼─── Collector B (Crypto, freq=5m)
                  └─── Collector C (Macro, freq=1d)
```

- Scheduler 是一个时间轮（timing wheel），按 Metric 的 Frequency 轮转
- 每个采集任务在 goroutine 中执行
- 失败重试 + 告警（采集失败本身也是一种异常，但不走 Detector 体系）

### 5.2 DataSource 抽象

```go
type DataSource interface {
    Fetch(ctx, query) ([]DataPoint, error)
    Health() error
}
```

不同 Provider（Yahoo Finance / Tushare / Glassnode）实现同一接口，Plugin 内部根据配置选择具体 DataSource。

好处：同一个 Plugin 可以灵活降档——主数据源挂了自动切备源。

---

## 六、检测系统

### 6.1 检测器参数示例

```yaml
# 阈值检测
type: threshold
metric: etf.gold.net_inflow
params:
  operator: ">"
  value: 1000000000   # 10 亿美元
severity: critical

# 趋势检测
type: trend
metric: etf.gold.net_inflow
params:
  consecutive_days: 10
  direction: increase
severity: warning

# 历史分位检测
type: percentile
metric: crypto.btc.exchange_balance
params:
  window_days: 365
  percentile: 99
  direction: below    # 创新低
severity: critical
```

### 6.2 检测流程

```
Metric 新数据写入
      │
      ▼
触发 Metric 关联的所有 Detector
      │
      ▼
每个 Detector 加载历史窗口数据
      │
      ▼
运行算法 → 是否触发？
      │
      ├── 否 → 结束
      │
      └── 是 → 写入 Alert Store
               │
               ▼
          通知 Frontend（推送 / 轮询）
```

---

## 七、存储设计

### 7.1 存储分层

| 数据类型 | 存储选型 | 原因 |
|---------|---------|------|
| Metric 元数据 | PostgreSQL | 强一致、按 tag/namespace 查询 |
| DataPoint（时序）| TimescaleDB | PG 扩展，支持降采样、连续聚合 |
| Alert | PostgreSQL | 业务表，事务性操作多 |
| 关系/本体（如有）| PostgreSQL | 图结构弱，关系型够用 |
| 新闻全文缓存 | Elasticsearch / OpenSearch | 全文搜索（MVP 可先不做） |

### 7.2 为什么用 TimescaleDB 而不是 InfluxDB

- 和 Metadata / Alert 共用 PG 生态，减少运维复杂度
- 支持标准 SQL，查询灵活
- 连续聚合（Continuous Aggregate）开箱即用
- MVP 阶段不需要集群，单实例够

---

## 八、API 设计

### 8.1 核心 API

| 路径 | 方法 | 用途 |
|------|------|------|
| `/api/metrics` | GET | 列出所有 Metric |
| `/api/metrics/{id}/data` | GET | 查询时序数据（时间范围 + 降采样） |
| `/api/alerts` | GET | 列出 Alert（可过滤 severity / status / 时间） |
| `/api/alerts/{id}` | GET | Alert 详情 |
| `/api/alerts/{id}/research` | GET | 研究页面所需全部数据 |
| `/api/detectors` | GET/POST | Detector 配置管理 |

### 8.2 设计原则

- RESTful，资源导向
- 查询走 GET，参数走 query string
- 分页用 cursor（时序数据用时间戳做 offset）
- 返回结构统一：`{ data, meta: { page, total } }`

---

## 九、前端设计

### 9.1 页面结构

```
Capital Radar（首页）
  └── 异常流（卡片列表，按 severity 排序）
        └── 点击进入
              └── Research Page
                    ├── 异常摘要
                    ├── 主时间线图
                    ├── 关联指标对比图
                    ├── 相关资产
                    ├── 新闻列表
                    └── 历史相似模式
```

### 9.2 状态

前端不需要复杂状态管理，SSR + 客户端轮询 Alert 列表即可。

---

## 十、长期演进方向

### 10.1 Plugin 独立化

现在 Plugin 编译进主进程 → 未来可拆分为独立进程 + gRPC 通信。

迁移路径：Plugin 接口已经是进程内的 interface，变成 RPC 接口时只需替换 transport 层。

### 10.2 本体论（Ontology）

在 Metric 层之上构建实体关系网络：

```
Entity: Gold
  ├── Metric: etf.gold.net_inflow
  ├── Metric: etf.gold.aum
  ├── Metric: price.xau
  └── Relation: [etf.gold.net_inflow] --lead_lag--> [price.xau]
```

系统可基于 Relation 做跨 Metric 检测（领先滞后、背离、传导链）。

### 10.3 社区 Plugin

基于 gRPC + 注册表上线 Plugin 市场，第三方开发者发布自己的 Plugin。

---

## 十一、MVP 交付清单

| 模块 | MVP 包含 |
|------|---------|
| Mount 3 个 Plugin | ETF / Crypto / Macro |
| 3 种 Detector | Threshold / Trend / Percentile |
| 1 个 Storage | TimescaleDB |
| 1 套 API | 6 个核心端点 |
| 1 个页面 | Capital Radar + 简单 Research |
| 不做什么 | 不做本体 / 不做插件热加载 / 不做新闻搜索 |

