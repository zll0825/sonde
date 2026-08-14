# Capital Observatory

ETF / Macro / Crypto 资本数据观测系统：采集 → 质量评估 → 规则检测 → 告警通知 → 研究组装 → 前端展示。

## 投产验证（Soak）操作手册

目标：真实数据下跑 1–2 周，观察误报率并将全系统告警校准至 **≤ 10 条/天**（噪音预算）。

```bash
# 1. 清空旧的 dev 数据（历史 mock 数据与旧 metric ID 会污染校准，必须重建）
make clean-data

# 2. 配置密钥（FRED 必需；Telegram 可选但强烈建议——否则告警只落库不推送）
export FRED_API_KEY=            # 免费注册 https://fred.stlouisfed.org
export ALPHAVANTAGE_API_KEY=    # Commodities 黄金现货；免费层 25 次/天
export TELEGRAM_BOT_TOKEN=      # @BotFather 创建 bot 获取
export TELEGRAM_CHAT_ID=        # @userinfobot 获取数字 chat ID

# 3. 启动全栈（DB + Core + API + ETF/Macro/Crypto/Commodities 四插件）
make dev-up

# 4. 验证链路（应看到 fred / alpha_vantage / coingecko / yahoo 来源的观测入库）
psql postgres://capital:capital_dev@localhost:5432/capital_observatory \
  -c "SELECT metric_uid, value, source_provider, quality_grade, ingested_at
      FROM observations ORDER BY ingested_at DESC LIMIT 20;"
```

Soak 期间的观察点：

```sql
-- 告警产出（核心校准对象：按日统计是否 ≤ 10 条）
SELECT date_trunc('day', triggered_at) AS day, count(*)
FROM alerts GROUP BY 1 ORDER BY 1 DESC;

-- 各来源数据是否持续流入（某来源长时间无新增 = 采集端出问题）
SELECT source_provider, max(ingested_at) FROM observations GROUP BY 1;

-- 命令通路健康度
SELECT * FROM command_log ORDER BY created_at DESC LIMIT 10;
```

Core 日志每小时输出一次噪音预算状态，超预算时打 WARN（`make dev-logs` 关注 `noise budget EXCEEDED`）。
误报多的规则在前端（http://localhost:8080/）或数据库中调阈值，改后观察次日效果。

**Soak 注意事项：**

- `FRED_API_KEY` 缺失时 macro/commodities 插件按设计**快速失败**；`ALPHAVANTAGE_API_KEY` 缺失时 commodities 同样快速失败。真实模式绝不回退到 mock。
- Commodities 默认每 2 小时采集一次 XAUUSD 现货黄金（每天 12 次），为 Alpha Vantage 免费层的 25 次/天限制保留重连、验证和显式回填余量；历史回填不参与周期调度。
- `btc.ass.exchange_balance` 无免费真实源，实时路径仍为 mock 随机游走，且挂有两条规则（阈值 + 7 天连跌趋势）。**该指标产生的告警不计入校准结论**；若干扰明显，建议禁用这两条规则。
- 回填命令（Backfill）对 crypto 的 price / hash_rate 有意不产出历史（拒绝用 mock 造假基线），percentile / trend 规则依赖 soak 期自然积累约 5 个周期后生效。

## 快速开始

```bash
make dev-up      # 构建并启动全栈（含迁移）
make dev-logs    # 查看服务日志
make dev-down    # 停止全栈（保留数据卷）
make clean-data  # 停止并删除数据卷（破坏性）
```

启动后访问 http://localhost:8080/ 查看 Capital Radar 前端。

## 环境变量

| 变量 | 作用域 | 必需 | 说明 |
|------|--------|------|------|
| `FRED_API_KEY` | macro / commodities 插件 | ✅ | FRED 数据源密钥，缺失则插件快速失败 |
| `ALPHAVANTAGE_API_KEY` | commodities 插件 | ✅ | XAUUSD 黄金现货/历史 API 密钥；免费层 25 次/天 |
| `TELEGRAM_BOT_TOKEN` / `TELEGRAM_CHAT_ID` | core | 可选 | 两者齐备时启用 Telegram 告警推送 |
| `WEBHOOK_URL` / `WEBHOOK_TOKEN` | core | 可选 | 通用 webhook 通道（Telegram 优先级更高） |
| `API_TOKEN` | api | 建议 | 控制端点（sync/backfill）的 Bearer 令牌；未配置时写操作被拒绝 |
| `PROVIDER` | 各插件 | 可选 | 设为 `mock` 切换到合成数据（离线开发用） |
| `COLLECTION_INTERVAL` | 各插件 | 可选 | 采集间隔（Go duration 格式，如 `1h`、`60s`） |
| `CORE_ADDR` | 各插件 | 可选 | Core gRPC 地址，默认 `:50051` |
| `DATABASE_URL` | core / api | 可选 | Postgres 连接串，compose 内已配置 |

通知通道解析顺序：Telegram → Webhook → Nop（无配置时告警仅落库，启动时打 WARN 提示）。

## 服务端口

| 服务 | 端口 | 协议 |
|------|------|------|
| PostgreSQL (TimescaleDB) | 5432 | TCP |
| Core gRPC | 50051 | gRPC |
| API + 前端 | 8080 | HTTP |

## 本地开发（无 Docker 全栈）

```bash
# 1. 仅启动数据库
docker compose -f deployments/docker-compose.yml up -d timescaledb

# 2. 应用迁移
make migrate-up

# 3. 运行 Core（Air 热重载）
make dev

# 4. 运行 API（新终端）
make run-api

# 5. 按需运行插件（新终端；*-mock 为离线合成数据版）
make run-etf
make run-macro        # 需要 FRED_API_KEY
make run-macro-mock
make run-commodities  # 需要 FRED_API_KEY + ALPHAVANTAGE_API_KEY
make run-commodities-mock
make run-crypto       # CoinGecko + mempool.space，无需密钥
make run-crypto-mock
```

## 系统链路

```
Plugin (gRPC stream)
  → MetricSnapshot
    → Core: M2 入库 (InsertObservation + QualityScore + 覆盖矩阵)
      → M3 规则检测 (Threshold / Percentile / Trend，频率感知回看窗口)
        → M4 告警 (AlertEngine → 去重 → 自动 resolve)
          → Outbox → 通知 (Telegram / Webhook) + 噪音预算记账
          → M4 研究 (Research Assemble → Snapshot)
            → M5 前端 (API → Capital Radar)

控制通路:
API command_log → CommandDispatcher → Sync/BackfillCommand → Plugin → 采集 → Push → Ack → command_log completed/resolved
```

## 数据源真实性

| 插件 | 指标 | 来源 | 真实性 |
|------|------|------|--------|
| etf | `gld.ass.price` | Yahoo Finance | ✅ 真实 |
| etf | `gld.ass.daily_flow` / `eth.ass.daily_flow` | 合成 | ⚠️ mock（无免费源） |
| macro | `fed.ins.balance_sheet` (WALCL) | FRED | ✅ 真实（周频） |
| macro | `us.mkt.ten_year_yield` (DGS10) | FRED | ✅ 真实（日频） |
| macro | `us.mkt.dollar_index` (DTWEXBGS) | FRED | ✅ 真实（日频） |
| macro | `us.mkt.usd_cny` (DEXCHUS) | FRED | ✅ 真实（日频） |
| crypto | `btc.ass.price` | CoinGecko | ✅ 真实 |
| crypto | `btc.ass.hash_rate` | mempool.space | ✅ 真实 |
| crypto | `btc.ass.exchange_balance` | 合成 | ⚠️ mock（免费源仅付费的 Glassnode/CryptoQuant 提供） |

macro 支持真实历史回填（FRED 原生窗口查询，单次上限 90 天）；crypto 的 price / hash_rate 有意不支持 mock 回填（见 Soak 注意事项）。

## 测试

```bash
make test        # 全量测试（全 workspace 模块）
make lint        # gofmt + go vet + buf lint
make build       # 编译 core + api
```

## 项目状态

| 里程碑 | 状态 | 备注 |
|--------|------|------|
| M0 Repository | ✅ | compose / migrations / CI |
| M1 Plugin Registration | ✅ | gRPC session + 共享骨架 `pkg/pluginrunner` |
| M2 Observation Ingest | ✅ | QualityScore + 覆盖矩阵 |
| M3 Rule Evaluation | ✅ | Threshold / Percentile / Trend 三类探测器 |
| M4 Research Read | ✅ | Assembly + Snapshot + Review |
| M5 Control + Frontend | ✅ | 命令通路 + Capital Radar |
| 真实数据源接入 | ✅ | Yahoo / FRED / CoinGecko / mempool.space |
| 告警通知通道 | ✅ | Telegram / Webhook（outbox 重试托管） |
| B6 噪音预算校准 | ✅ | 72 小时风险验收通过；真实告警每天 ≤10 条 |

## 已知限制

- 控制端点（`/api/control/*`）强制要求 `API_TOKEN`（未配置时拒绝写操作）；读端点无鉴权，仅适合本机 / 可信内网部署。
- `btc.ass.exchange_balance` 与 ETF flow 为合成数据（无免费真实源）。
- 通知失败由 outbox 重试机制托管（退避 + 终态失败阈值），无独立死信告警。

## 文档

| 文档 | 内容 |
|------|------|
| [架构运行图（中文）](docs/architecture-zh.md) | as-built 架构：部署/数据流/时序 Mermaid 图 + 表职责 + 代码地图 |
| [PRD v4](docs/prd-v4.md) | 产品定义（v4.1，与架构对齐） |
| [System Architecture](docs/system-architecture.md) | 系统架构（冻结，v1.1） |
| [Domain Model v1.0](docs/domain-model.md) | 领域模型 |
| [Plugin Protocol v1.0](docs/plugin-protocol.md) | gRPC 接口定义 |
| [Database Schema v1.0](docs/database-schema.md) | 数据库 Schema |
| [ADR](docs/adr.md) | 架构决策记录（ADR-1…7） |
| [Conventions](docs/conventions.md) | Go 代码约定 |
| [Real-Data Onboarding](docs/milestone/real-data-onboarding.md) | 真实数据接入里程碑 |
| [MVP v0.1.0 发布记录](docs/releases/mvp-v0.1.0.md) | 验收证据、迁移与回滚边界、残余风险 |
