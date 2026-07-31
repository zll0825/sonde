# Architecture decision log.
# Format: ADR-{seq} {title}

## 2026-07-30

### ADR-1: Plugin ↔ Core via gRPC Bidirectional Stream
**Decision:** Plugin 和 Core 通过单一 gRPC Bidirectional Stream 通信。数据面 Push，控制面 Command。
**Rationale:** 不需要 Plugin 暴露端口、不需要 Core 回连、Docker 网络简单。
**Alternatives:** (a) Plugin 独立 gRPC server + Core 回连 — 运维复杂; (b) 纯 Pull — 调度不灵活。

### ADR-2: Relation/Rule ownership by Core
**Decision:** Plugin 只能 Suggest Relation 和 Rule，Core 的 RelationManager / RuleManager 做 accept/reject/merge/version。
**Rationale:** 否则 Plugin 越多 Ontology 一定冲突。Core 需要治理层。
**Key mechanism:** Relation 四层 taxonomy（structural/semantic/statistical/causal），每层不同审核策略。Rule 三方优先级（user_override > system_default > plugin_suggested）。

### ADR-3: Observation/Ontology strict isolation
**Decision:** observations 表没有任何 FK 到 entities 或 metric_definitions。Research 时临时 Join。
**Rationale:** Ontology 改了什么，所有历史 Observation 都不受影响。
**Trade-off:** 查询层复杂度略增，但隔离性优先。

### ADR-4: metric_uid as weak binding
**Decision:** 每条 Metric 有 `metric_uid`（Core 分配，终身不变）和 `metric_id`（人可读，可变）。
**Rationale:** observations 不 FK 但需要审计链路。metric 重命名时 uid 不变，历史数据可追溯。
**Not a FK:** 不强约束引用完整性。

### ADR-5: Tier 1 events via Transactional Outbox
**Decision:** MetricUpdated → Detection 不走 in-process channel。PushSnapshots 在同一事务写入 observations + outbox 后立即返回 ACK。Worker 异步轮询 outbox。
**Rationale:** 避免 gRPC handler 被慢检测阻塞造成恶性循环。保证"数据入库 ←→ 一定被检测"的原子性。
**Trade-off:** 检测延迟增加 5 秒（outbox 轮询间隔），可接受。

### ADR-6: gRPC Stream Single Writer Loop
**Decision:** 每个 StreamSession 维护专属 sendCh + 单写 Goroutine。禁止直接调用 stream.Send()。
**Rationale:** gRPC stream.Send() 非线程安全。多 Goroutine 并发写会 panic。

## 2026-07-31

### ADR-7: PRD v4 词表收敛为 v1.0 冻结词表
**Decision:** 领域模型采用 7 种 EntityType 与四层 13 种 RelationType，取代 PRD v4 §9.2 的 8 类实体 / 10 种关系词表。PRD 示例保留旧词表仅作意图说明；一切声明与校验以 `docs/domain-model.md` 与 taxonomy registry 为准。

**Entity 映射（PRD v4 → v1.0）：**

| PRD v4 | v1.0 | 说明 |
|--------|------|------|
| Asset | `asset` | 不变 |
| Vehicle | `instrument` | 改名，语义相同（ETF、期货、期权） |
| Market | `market` | 不变 |
| Actor | `institution` | 改名，收窄为机构（美联储、央行） |
| Channel | `flow` | 资金/信息通道统一建模为资金流实体 |
| Indicator | `indicator` | 不变 |
| Factor | `index` 或 `indicator` | 按对象归类：DXY → index，利率/通胀 → indicator |
| Jurisdiction | （删除） | 管辖区域用 `Entity.metadata` / `tags` 表达，不再是独立类型 |

**Relation 映射（PRD v4 → v1.0）：**

| PRD v4 | v1.0 | 层 |
|--------|------|-----|
| tracks | `tracks` | structural |
| hedges | `hedges` | semantic |
| leads / lags | `leads` / `lags` | statistical |
| correlates_with | `correlates` / `inversely_correlates` | statistical（拆分正负相关） |
| influences | `causes` | causal（改名，绑定人工审核门槛） |
| depends_on_regime | `depends_on_regime` | causal |
| flows_into / flows_out_of | （删除） | 资金流建模为 flow 实体 + structural 关系，不再是关系类型 |
| exposed_to | （删除） | 暂无对应；需要时经 taxonomy registry 扩展并显式定层 |

v1.0 新增（PRD 没有）：`component_of`、`issued_by`、`belongs_to`（structural）；`competes`、`signals`（semantic）。

**Rationale:** 每种关系必须归属四层之一才能获得确定的审核策略（ADR-2）。PRD 词表中方向模糊（exposed_to）或与实体建模重叠（flows_into/out）的类型被删除或改名。

---

## 待决事项（M1 plugin.proto 定稿前必须拍板）

1. **PushSnapshots 的 ack 通路**：流式推送（`PluginMessage.push_snapshots`）没有对应的 ack 载荷——`PushSnapshotsResponse` 当前不可达（`CoreMessage` oneof 无该变体，也无 unary push RPC），插件拿不到 inserted / deduplicated / rejected 计数。候选：(a) `CoreMessage` 增加 `push_ack` 变体（推荐，保持单连接语义与背压路径）；(b) 增加 unary `PushSnapshots` RPC 作为并行路径。
2. **ContextTemplates 是否入 v1 契约**：PRD §10.2 的 Context Builder 能力在 `RegisterPluginRequest` 中无字段。候选：(a) 推迟到 Phase 2（推荐，MVP Research 仅用 Ontology 展开，PRD 勘误已注明）；(b) M1 扩展 proto 增加 `ContextTemplate` 声明。

---

## 2026-07-31 (M0)

### ADR-8: Database migration tool = golang-migrate
**Decision:** Use [golang-migrate](https://github.com/golang-migrate/migrate) with raw SQL migration files under `migrations/`.
**Rationale:** Schema is already frozen in `docs/database-schema.md` as raw SQL — no DSL or code-generation step needed. golang-migrate is file-based, supports `.up.sql` / `.down.sql` pairs, and works with any PostgreSQL driver.
**Alternatives:** (a) Atlas — HCL DSL, generates diffs from live DB → unnecessary when schema is frozen; (b) goose — similar but less ecosystem traction.
**Convention:** Migration files named `NNN_description.up.sql` / `NNN_description.down.sql`. Applied via `make migrate-up` (reads `$DB_URL`).
**Baseline:** `001_baseline.up.sql` contains all 14 tables + indexes from `docs/database-schema.md`.
