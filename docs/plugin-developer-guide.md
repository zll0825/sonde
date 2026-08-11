# Capital Observatory — Plugin Developer Guide

本文档面向**插件开发者**：写一个新的 domain plugin（例如 `commodities`、`bonds`、`equities`）之前，先读本文档。它涵盖布局、注册、采集器接口、provider safety 约定、测试模式，以及参考实现清单。

协议层参考见 [plugin-protocol.md](./plugin-protocol.md)（(proto ↔ domain 的完整字段清单)。

---

## 1. Why Plugins Are Separate Go Modules

每个 plugin 是一个**独立的 Go module** (`go.mod`)，位于 `plugins/<name>/`。原因：

- **独立发布**：plugin 可以独立于 Core 开发与发版（不同 deploy cadence）。
- **依赖隔离**：不同 plugin 可以用不同版本的第三方库，不影响 Core 或其它 plugin。
- **热替换升级**：session 断线重连时 Core 可以独立升级 plugin 进程。
- **独立测试环境**：每个 plugin 有独立的 mock / test fixture。

Worktree 在 `go.work` 中把每个 plugin 作为独立 module 保留（不用 vendor）；新 plugin 创建后必须先 `go mod tidy`，再把 `plugins/<name>` 加入 `go.work`。

---

## 2. Skeleton Layout

以 `plugins/commodities/` 为例（最小可用布局）：

```
plugins/<name>/
├── go.mod                       # module capital_observatory/plugins/<name>
├── go.sum                       # go mod tidy 生成，必须提交
├── cmd/
│   └── <name>/
│       └── main.go              # 程序入口 + BuildRegistration
└── internal/
    └── collector/
        ├── fred.go              # 真实采集器（FRED / Yahoo / CoinGecko …）
        ├── fred_test.go         # 真实采集器单元测试（mock HTTP transport）
        └── mock.go              # 离线 mock（Local dev / CI 兜底）
```

骨架约束：

- `cmd/<name>/main.go` 必须用 `pluginrunner.NewLifecycle(...)`，不要自行 dial gRPC。
- `main.go` 必须实现 `pluginrunner.Provider` 与可选的 `pluginrunner.WindowedProvider`。
- 添加 compile-time assertion：
  ```go
  var _ pluginrunner.Provider = (*collector.FREDCollector)(nil)
  var _ pluginrunner.WindowedProvider = (*collector.FREDCollector)(nil)
  ```
- `internal/collector/mock.go` 为**所有注册指标**返回 deterministic + bounded-random 数据；用于本地 `PROVIDER=mock` 模式。

---

## 3. Plugin Registration

`BuildRegistration()` 返回 `*pb.RegisterPluginRequest`，声明该 plugin 贡献的：

- `info` — name / version / description
- `entities` — 核心观察对象（instrument / institution / indicator …）
- `metrics` — 每个观测维度（必须有 entity_id → entity 对照）
- `relations` — 跨实体关系的建议（Core 自动 review 后写入 relations / relation_suggestions）
- `rules` — 推荐的检测规则（Core 按 source 优先级 review 写入 rules / rule_suggestions）

**命名约定**（避免个人命名风格）：
- entity id：简短大写，例如 `OIL` `COPPER` `GOLD` / `ETF_GOLD` `BTC_HASH`。
- metric id：`<domain>.<class>.<member>`，小写 + 下划线，例如 `oil.energy.wti`、`metal.industrial.copper`。Core 会自动派生 `MetricUID`（`mtr_xxx`）。
- relation direction：总是与 semantics 一致 — `OIL → GOLD` 用 `correlates_with`；不可逆关系尽量给出方向说明。

**维度覆盖入口**：每个新 domain 应该覆盖至少 3 个独立维度（否则缺乏研究价值），避免单一 source + 多 metrics 的"伪覆盖"。

---

## 4. Collector Interface & Safety

### 4.1 Provider / WindowedProvider

```go
type Provider interface {
    GetSnapshots(ctx context.Context) ([]Snapshot, error)
}

type WindowedProvider interface {
    Provider
    GetSnapshotsForWindow(ctx context.Context, start, end time.Time) ([]Snapshot, error)
}
```

`Snapshot` 字段语义：
- `MetricID` = 注册时 metric 的 `id`
- `Value` = 观测数值（注意 unit scale，例如 FRED WTI 是 USD/bbl、copper 是 ¢/lb）
- `Timestamp` = 源数据的 observation/event time，不要填 fetch time
- `FetchedAt` = fetch 完成时间
- `Provider` = provider name，例如 `"fred"` / `"yahoo_finance"` / `"coingecko"`
- `SourceClass` = `model.SourceClassReal | Mock | Test`（显式、绝不从 provider name 推断）
- `Grade` = `"realtime" | "delayed" | "estimated" | "preliminary"`

### 4.2 SafeHTTPClient

**所有外网请求**必须通过 `pkg/provider.SafeHTTPClient`，不要 `http.Get`：

```go
cfg := provider.FREDConfig()            // 预设：FREDConfig/YahooFinanceConfig/CoinGeckoConfig/…
client := provider.NewSafeHTTPClient(cfg)
// 或者注入 mock transport：
client := provider.NewSafeHTTPClientWithHTTPClient(cfg, &http.Client{Transport: mockRT})
```

`SafeHTTPClient` 已经整合：
- Per-provider token-bucket 限流（按 provider name 散列；burst + RPS 来自 preset）
- Circuit Breaker（Closed → Open → Half-Open，自动恢复）
- Bounded retry + 指数退避 + jitter + 429/Retry-After
- 带超期的 `context aware` 等待

**测试用固定 RPS** 配置避免超时：
```go
testCfg := provider.Config{
    ProviderName: "fred-<plugin>-test",
    Timeout:      5 * time.Second,
    RPS:          100,  // 高 RPS，避免 token bucket 延迟
    Burst:        10,
    MaxRetries:   1,
    BaseDelay:    10 * time.Millisecond,
    MaxDelay:     50 * time.Millisecond,
}
```

### 4.3 API Key 环境变量

每个外网来源应该通过环境变量注入：
- `FRED_API_KEY` — 用于 FRED (fred.stlouisfed.org)
- `COINGECKO_API_KEY` (optional; 免费版可缺省)

`NewXXXCollector()` **应该 fail-fast**（`os.Getenv == ""` 直接 `return error`）；不要写 silent fallback 到 mock。

### 4.4 Partial Failure

多指标采集必须：
- 跳过失败项、返回成功项（partial over nothing）
- `log.Warn()` 记录失败原因
- 只在**全部失败**时返回 error

```go
for _, entry := range seriesList {
    val, _, err := f.fetchLatest(ctx, entry.SeriesID, ...)
    if err != nil {
        logFetchSkip(entry.MetricID, entry.SeriesID, err)
        failed++
        continue
    }
    snaps = append(snaps, ...)
}
if len(snaps) == 0 {
    return nil, fmt.Errorf("all %d fetches failed", len(seriesList))
}
```

---

## 5. Relations & Rules (Research Detection Layer)

`relations` 建议让 Core 自动 review：
- structural / semantic 关系 → auto-accept（Core 拥有自己的 taxonomy）
- statistical 关系 → 写入 relation_suggestions（待用户确认后才 active）
- 无论如何必须给出 `description` 字段（引用来源/方法）

`rules` 建议让 Core 按 source priority 裁决：
- `plugin_suggested` 默认会被接受（首次注册时）
- user_override 会阻断 plugin 建议（写入 rule_suggestions）
- config 用 protojson 表示；阈值类用 `"operator":"gt|lt|gte|lte","value":<n>`；分位类用 `"percentile":<n>, "consecutive":<n>`；趋势用 `"direction":"up|down","consecutive":<n>`。

---

## 6. Testing

每个 collector 文件对应的 `*_test.go` 必须覆盖：
- mock HTTP transport（`roundTripFunc`）→ `SourceClass == Real`
- FRED 值解析（`.` / `""` / 数值 / 带 scale）
- Mock 返回的维度：每一次 `GetSnapshots()` 覆盖所有注册 metric
- `NewXXXCollector()` 无 API key 时 fail-fast
- `GetSnapshotsForWindow()` 返回符合期望数量的样本

测到网络是**反模式**：CI 不会走外网；mock transport 是唯一允许的 HTTP 路径。

测试命名参考：`TestXXX_ClassifiesUpstreamSnapshotsAsReal` / `TestParseXXX_Scaling` / `TestMock_GetSnapshots_CoversAllDimensions`。

---

## 7. Runbook

本地跑 plugin（默认 `:50051` 绑 Core）：
```bash
cd plugins/<name>
PROVIDER=mock go run ./cmd/<name>        # 离线 mock 模式
PROVIDER=real FRED_API_KEY=xxx go run ./cmd/<name>
COLLECTION_INTERVAL=30s go run ./cmd/<name>
```

跑测试：
```bash
cd plugins/<name>
go mod tidy
go test ./... -count=1
```

新 plugin 加入 worktree：
```bash
go work use ./plugins/<name>
```

---

## 8. Reference Implementations

| Plugin    | Source        | File                                   | Why参考它 |
|-----------|---------------|----------------------------------------|-----------|
| ETF       | Yahoo Finance | `plugins/etf/internal/collector/yahoo.go` | 单一 provider + SafeHTTPClient 基础用法 |
| Crypto    | CoinGecko / Mempool / Blockchain | `plugins/crypto/internal/collector/{coingecko,blockchain_info}.go` | 多 client、多 bearer/private token、单位缩放 |
| Macro     | FRED          | `plugins/macro/internal/collector/fred.go` | FRED 标准模式、系列化 fetchRange、429/Retry-After、弹性 unit scale |
| Commodities | FRED          | `plugins/commodities/internal/collector/fred.go` | 多 series、partial failure 兜底、`test-friendly` RPS 配置 |
