# 套用 DBX 分层纪律迭代 Sonde

状态：草案（2026-09-03）
来源：对照 [dbx](https://github.com/t8y2/dbx) 的架构，评估本仓库的扩展成本。

> 不要把 DBX 整套搬过来。Sonde 是持续运行的观测管道，DBX 是交互式客户端。
> 该套的是分层纪律，不是 Tauri、YAML 枚举 90 种库、也不是 20MB 体积约束。

## 一句话

DBX 能覆盖 90+ 连接，是因为协议相同的东西不当成新系统。
Sonde 以后要覆盖债券、外汇、更多商品，也应该让 **FRED 序列不当成新插件**。

## 两边已经对齐的部分

| DBX | Sonde | 结论 |
| --- | --- | --- |
| `dbx-core` 一份逻辑，外壳只做适配 | Core 管摄入检测告警，API 只读库、写 `command_log` | 已经对，别合并 |
| Agent 出进程 + JSON-RPC | Plugin 出进程 + 双向 gRPC | 已经对 |
| Core 认方言不认「黄金」 | Core 认 Ontology Contract，不认金融实体 | 守住 ADR-2 |
| 能力位隐藏做不到的功能 | `WindowedProvider` 运行时类型断言 | 半成品 |
| YAML 注册表生成类型 | `plugins/*/internal/definitions/` | 空目录，意图在、活没干 |
| 原生驱动 vs Agent | 共享 HTTP provider vs 各插件 collector | FRED 客户端抽了，拉取循环还复制了两份 |

## 现在最痛的三处

1. **插件 = 领域目录 + 采集器，缠在一起。** 加一条 FRED 序列要改 `buildRegistration()` 和 `fredSeriesList`。macro 与 commodities 各有约 400 行同构 FRED 拉取循环。
2. **能力没有声明。** API/前端不知道插件要不要密钥、能否回填、最大窗口、有没有 mock。状态页只能展示「此刻连上的」，不能展示「期望拓扑」。
3. **外壳开始发胖。** `web/index.html` 是单文件工作台；`cmd/api` 堆了 handler；没有 CLI/MCP 复用同一套只读查询。

## 目标分层

```text
Plugin   = 领域目录（entities / metrics / rules / relations / capabilities）  ← 数据
Provider = FRED / Yahoo / CoinGecko / Alpha Vantage                          ← 共享代码
Binding  = metric_id → series_id / scale / frequency                         ← 数据
Lifecycle = pkg/pluginrunner                                                 ← 已有，别动
```

建议落地形态：

```text
plugins/macro/manifest.yaml          # 领域声明
plugins/macro/bindings.yaml          # metric → provider 映射
pkg/provider/fred/                   # 唯一 FRED 采集实现
pkg/pluginrunner/                    # 读 YAML → RegisterPluginRequest
cmd/core                             # 不变
cmd/api                              # 薄 REST，读 catalog + DB
cmd/cli                              # 同一套只读查询（后续）
web/src/{status,alerts,research,ontology}
```

`manifest.yaml` 示例：

```yaml
name: macro
version: 0.3.0
capabilities:
  windowedBackfill: true
  maxBackfillDays: 3650
  requiresSecrets: [FRED_API_KEY]
  mockAvailable: true
entities:
  - id: FED
    type: institution
    namespace: fed
metrics:
  - id: fed.ins.balance_sheet
    entity: FED
    unit: USD
    frequency: weekly
    provider: fred
    series: WALCL
    scale: 1e6
rules:
  - name: balance_sheet_spike
    metric: fed.ins.balance_sheet
    detector: percentile
    config: { percentile: 90, consecutive: 1 }
```

验收标准：新增一条 FRED 序列不改 Go，只改 YAML + 测试夹具。

## 明确不套的

- Tauri / 桌面壳：这是 7×24 服务
- 把 proto 改成 YAML：Ontology 契约继续用 protobuf
- 把 Plugin 进程并回 Core
- 为每个新指标写新 Plugin
- 让 Core 认识「黄金」「BTC」

## 迭代顺序

### 第一刀：Provider 去重 + YAML 目录

- 把 macro/commodities 两份 FRED 循环收进 `pkg/provider/fred`
- `BuildRegistration` 改成读 manifest
- 能力字段随注册上报；`/api/status` 带上 `expectedPlugins` 和 `capabilities`
- 空的 `definitions/` 终于有东西

样板插件用 **macro**（指标最多、全是 FRED）。commodities 跟着迁，etf/crypto 可先留 Go 注册。

### 第二刀：控制面按能力开门

- 无 `windowedBackfill` 就禁用 Backfill
- 缺密钥在状态页标红，而不是等插件崩溃重连刷 registration version
- 规则/本体变更先出 diff 再提交

### 第三刀：外壳拆分，不加新运行时

- `web/index.html` 按 status / alerts / research / ontology 切开，先不必上 Vue
- `cmd/api` 的 handler 挪回 `internal/api`
- 加 `cmd/cli`，把 soak 手册里的 SQL 变成命令

### 第四刀（可选）：MCP

目录和能力稳定后，再给研究助手接只读 MCP：列告警、拉 research snapshot、读 ontology。不要让 MCP 直接 `backfill`。
