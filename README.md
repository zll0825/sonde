# Capital Observatory

ETF / Macro / Crypto 数据观测系统：采集 → 评估 → 告警 → 研究 → 展示。

## 快速开始（一键起全栈）

```bash
make dev-up
```

该命令构建并启动：PostgreSQL + TimescaleDB / Core(gRPC) / API + 前端 / ETF Plugin。

```bash
# 查看服务日志
make dev-logs

# 停止全栈
make dev-down

# 查看告警
psql postgres://capital:capital_dev@localhost:5432/capital_observatory \
  -c "SELECT * FROM alerts ORDER BY triggered_at DESC LIMIT 10;"
```

启动后访问 http://localhost:8080/ 查看前端。

## 服务端口

| 服务 | 端口 | 协议 |
|------|------|------|
| PostgreSQL | 5432 | TCP |
| Core gRPC | 50051 | gRPC |
| API + 前端 | 8080 | HTTP |

## 本地开发（无 Docker）

```bash
# 1. 启动数据库
make dev-db

# 2. 应用迁移
make migrate-up

# 3. 运行 Core（热重载）
make dev

# 4. 运行 API（新终端）
make run-api

# 5. 运行 ETF Plugin（新终端）
make run-etf
```

## 系统链路

```
Plugin (gRPC stream)
  → MetricSnapshot
    → Core: M2 入库 (InsertObservation + QualityScore + 覆盖矩阵)
      → M3 阈值检测 (Threshold → 后续 Percentile/Trend)
        → M4 告警 (AlertEngine → 自动 resolve)
          → M4 研究 (Research Assemble → Snapshot)
            → M5 前端 (API → Web)

控制通路:
API command_log → CommandDispatcher → Sync/BackfillCommand → Plugin → 采集 → Push → Ack → command_log completed/resolved
```

## 数据通路

- **实时数据**：ETF 插件通过 Yahoo Finance API 获取 GLD 每日价格（公开 API，无需 Key）
- **采集间隔**：默认每日（符合 ETF 流量日频语义）；开发环境可用 `INTERVAL_SECONDS=60` 缩短
- **合成数据**：流量（flow）指标无公开源，暂用合成数据；设置 `PROVIDER=mock` 全部切换为合成

## 测试

```bash
go test ./...          # 全量测试
make lint              # gofmt + go vet + buf lint
```

## 项目状态

| 里程碑 | 状态 | 备注 |
|--------|------|------|
| M0 Repository | ✅ | compose / migrations / CI |
| M1 Plugin Registration | ✅ | gRPC session + single-connection |
| M2 Observation Ingest | ✅ | QualityScore + 覆盖矩阵（A3 实现） |
| M3 Rule Evaluation | ✅ | Threshold detector（percentile/trend Phase 2） |
| M4 Research Read | ✅ | Assembly + Snapshot + Review |
| M5 Control + Frontend | ✅ | 命令通路 (C7) + 静态托管 (D9) |
| B4 Alert 自动 resolve | ✅ | 有 active alert 未触发 → 自动 resolve |
| A1 真实数据源 | ✅ | ETF GLD 接 Yahoo Finance |
| C8 心跳健康 | ✅ | isPluginHealthy 接入真实活跃检测 |
| B6 噪音预算 | ⏳ | 操作调参——需真实数据跑一段后按误报率调至 ≤10 条/天 |

## 文档

| 文档 | 内容 |
|------|------|
| [PRD v4](docs/prd-v4.md) | 产品定义（v4.1，与架构对齐） |
| [System Architecture](docs/system-architecture.md) | 系统架构（冻结，v1.1） |
| [Domain Model v1.0](docs/domain-model.md) | 领域模型 |
| [Plugin Protocol v1.0](docs/plugin-protocol.md) | gRPC 接口定义 |
| [Database Schema v1.0](docs/database-schema.md) | 数据库 Schema |
| [ADR](docs/adr.md) | 架构决策记录（ADR-1…7） |
| [Conventions](docs/conventions.md) | Go 代码约定 |
