# Capital Observatory MVP v0.1.0

发布日期：2026-08-13

## 发布范围

`v0.1.0` 是 Capital Observatory 的首个可运行 MVP 基线，覆盖 M0-M5、
真实数据源接入、运行状态面板，以及稳定化阶段完成的持久检测、告警研究
关联、命令恢复、告警预算和数据语义修正。带注释标签 `v0.1.0` 指向权威
发布提交。

## 验收证据

- 72 小时风险验收窗口为
  `[2026-08-10 01:38, 2026-08-13 01:38) UTC`。CoinGecko 与
  mempool.space 各完成 72 次小时采集，FRED 完成 72 次采集并写入 5 个
  新值，Yahoo Finance 完成 3 次日采集。
- 窗口内无主机休眠或遥测缺口，六个容器的重启数均为 0；所有 UTC 日的
  真实告警均未超过每天 10 条。
- 最终审计的 outbox、command 和 research 链路均无待处理积压、终态失败、
  缺失、重复或孤立记录。
- 受控故障注入在隔离环境完成，覆盖服务/插件重连、数据源超时、检测与
  outbox 恢复、命令租约与幂等、研究关联修复、告警预算和积压恢复。
- 完整证据位于
  `.trellis/tasks/archive/2026-08/08-06-mvp-soak-reliability/`；质量门禁
  证据位于
  `.trellis/tasks/archive/2026-08/08-06-mvp-quality-gates/evidence.md`。

## 数据库迁移

发布包含迁移 1-6：基础 schema、中文注释、关系分类规范化、持久检测
outbox、命令租约，以及告警来源分类与预算审计。发布时应执行：

```bash
make migrate-up
make migrate-status
```

验收部署的最终 schema 版本为 `6`，且 dirty 标记为 false。

## 回滚边界

- 先回滚应用二进制，再评估 schema；迁移已被新 worker 使用后，不应直接
  向下回滚，应采用前向修复或数据协调。
- `005_command_leases` 与 `006_alert_source_budget` 引入的新状态和分类可能
  已被运行中的应用写入。回滚到不了解这些语义的旧二进制前，必须停止写入
  并验证兼容性。
- 禁止在 live soak 数据库上运行测试、清表或破坏性迁移。数据库测试使用
  独立的 `capital_observatory_hardening_test` 数据库。

## 发布复现

2026-08-13 的发布候选提交检查中，`git diff --check`、`make lint`、
`make build`、`make test-short`、独立数据库全量测试，以及根模块与三个插件
模块的 `-race -count=1` 测试全部通过。

在干净检出的 `v0.1.0` 上，以独立 TimescaleDB 测试库执行：

```bash
git diff --check
make lint
make build
make test-short
TEST_DATABASE_URL='postgres://capital:capital_dev@localhost:5432/capital_observatory_hardening_test?sslmode=disable' make test
TEST_DATABASE_URL='postgres://capital:capital_dev@localhost:5432/capital_observatory_hardening_test?sslmode=disable' go test -race -count=1 ./...
```

插件模块的 race gate 还需分别在 `plugins/etf`、`plugins/crypto` 和
`plugins/macro` 中运行 `go test -race -count=1 ./...`。

## 残余风险

- 72 小时窗口短于原先的 7 天标准，不能覆盖所有低频服务、网络或数据源
  故障，也不等同于七天可靠性证明。
- FRED 等低频或不规则发布的数据源可能在采集成功时仍没有新值；必须继续
  区分上游发布延迟与采集链路故障。
- 隔离环境的受控恢复覆盖已知故障路径，但无法复现长时间运行中的所有时序
  组合。
- 当前数据源覆盖和采集频率未因本次验收而扩展，发布后仍需持续监控。
