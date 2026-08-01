# M1: Plugin 生命周期管理

**里程碑：M1 — Plugin Registration Path**
**版本：1.0**
**状态：Draft**
**关联文档：** [prd-v4.md](../prd-v4.md) · [system-architecture.md](../system-architecture.md) · [domain-model.md](../domain-model.md) · [plugin-protocol.md](../plugin-protocol.md) · [adr.md](../adr.md)

---

## 一、目标

M1 的核心目标是建立 Plugin 与 Core 之间的**可信通信通道**，实现 Plugin 从启动、注册、持续心跳到异常断连重连的完整生命周期管理。具体包括：

1. **注册通道**：Plugin 启动后通过 unary RPC 一次性注册，Core 分配 `plugin_id` 和 `registration_version`。
2. **会话通道**：注册成功后 Plugin 通过 Bidirectional Stream 维持长连接，传输 PushSnapshots、Heartbeat、CommandAck。
3. **心跳检测**：Core 通过心跳超时机制判定 Plugin 在线/离线/降级状态。
4. **断连重连**：Plugin 在 stream 断开后自动退避重连，Core 在重连期间维护已注册的 Ontology 声明不丢失。
5. **Token 认证**：每个 Plugin 持有唯一 token，Core 在注册和 stream 建立时验证身份（最小权限，不含用户级 auth）。

M1 的检测链路（Detector / AlertEngine）尚未就绪；观测数据写入（Observations）在 M2 实现。M1 只关心"Plugin 能连上、能注册、能被发现离线"。

---

## 二、用户故事

### 故事 1：运维启动 Plugin
> 作为运维工程师，我启动一个 ETF Plugin 容器后，能在 Core 日志中看到它注册成功并进入 RUNNING 状态，核心 API 能查询到它的 Plugin 记录。

**验收条件：**
- Plugin 启动后 5 秒内完成 `RegisterPlugin` 调用
- Core 返回 `success=true`，分配 `plugin_id`（格式 `plg_etf`）
- `plugins` 表 `state='running'`，`healthy=true`

### 故事 2：Plugin 断网后自动恢复
> 作为系统，当一个 Plugin 因网络抖动 stream 断开，我希望它在 30 秒内自动重连并恢复注册，不需要人工干预。

**验收条件：**
- Plugin 端实现指数退避重连（1s → 2s → 4s → 最大 30s）
- Core 侧 session 清理延迟 ≥ 2 × heartbeat_interval（避免误判瞬断）
- 重连后使用已有 `plugin_id` + token 重新注册，不产生重复 Plugin 记录

### 故事 3：未授权 Plugin 拒绝接入
> 作为安全机制，Core 必须拒绝没有有效 token 的 Plugin 注册请求。

**验收条件：**
- Plugin 请求不携带 token 或 token 无效时，Core 返回 `success=false` 并记录安全事件
- Token 通过环境变量注入（`PLUGIN_TOKEN`），不硬编码
- 同一 token 不被两个并发 session 接受

---

## 三、领域模型变化

M1 不引入新领域对象。已冻结的以下领域对象在 M1 首次被写入：

### 3.1 Plugin

| 字段 | 说明 |
|------|------|
| `id` | Core 分配，`plg_` + name |
| `state` | 状态机：`starting → running → degraded → error` |

状态转换由心跳和 stream 事件驱动（详见 system-architecture.md §1.3）。

### 3.2 Entity / Metric / Relation（注册声明阶段）

M1 的注册过程写入以下数据：
- `entities_v2`：Plugin 声明的 Entity（version=1, effective_from=NOW()）
- `metric_definitions_v2`：Plugin 声明的 Metric（Core 分配 `uid`，version=1）
- `relation_suggestions`：Plugin 建议的 Relation（status=pending/accepted，按四层 taxonomy 审核）
- `rule_suggestions`：Plugin 建议的 Rule（status=pending/accepted）
- `relations_v2`：Core 审核通过的 Relation（version=1）
- `rules_v2`：Core 审核通过的 Rule（version=1）

M1 **不写入** observations（那是 M2 的职责）。

---

## 四、数据流

### 4.1 注册流

```
Plugin 启动
  │
  ├─ 加载自身声明（Entity/Metric/Relation/Rule）+ Token
  │
  ├─ unary RPC: RegisterPlugin(RegisterPluginRequest)
  │   └─ Core 收到请求
  │       ├─ 验证 Token（环境变量配置的白名单 or DB tokens 表）
  │       ├─ 分配 plugin_id + registration_version
  │       ├─ 写入 plugins 表
  │       ├─ 遍历 Entity 声明 → INSERT entities_v2 (version=1)
  │       ├─ 遍历 Metric 声明 → 分配 metric_uid → INSERT metric_definitions_v2 (version=1)
  │       ├─ 遍历 RelationSuggestion → RelationManager.Review()
  │       │   ├─ structural → 自动接受 → INSERT relations_v2
  │       │   ├─ semantic → 自动接受 → INSERT relations_v2
  │       │   ├─ statistical → 校验 p<0.05 ∧ n≥30 → 通过则接受
  │       │   └─ causal → pending → 暂不写入 relations_v2
  │       ├─ 遍历 RuleSuggestion → RuleManager.Review()
  │       │   ├─ 自动接受 → INSERT rules_v2
  │       │   └─ 冲突 → 按三方优先级决策
  │       └─ 返回 RegisterPluginResponse{plugin_id, registration_version, success=true,
  │                                       reviewed_relations, reviewed_rules}
  │
  ├─ 打开 MaintainSession stream
  │
  └─ 进入 RUNNING 状态
```

### 4.2 心跳流

```
Core 端 (heartbeat_interval=30s, heartbeat_timeout=90s)
  ├─ 每 30s 检查所有 session 的最后心跳时间
  ├─ 超时未收到心跳 → state='degraded'
  ├─ 连续 3 次超时 → state='error'，清理 session
  └─ 恢复心跳 → state='running'

Plugin 端
  ├─ 每 25s（< interval）通过 stream 发送 HeartbeatRequest
  ├─ 包含 PluginStatus(state, last_collect_at, consecutive_errors, runtime)
  └─ Core 回复 HeartbeatResponse{ok=true, has_pending_command=false}
```

### 4.3 断连重连流

```
Plugin 检测到 stream 断开（gRPC error）
  ├─ 进入重连循环:
  │   ├─ delay = min(2^attempt, 30) 秒
  │   ├─ 等待 delay 后重新 dial
  │   ├─ 再次调用 RegisterPlugin（携带 plugin_id + token）
  │   └─ 成功 → 恢复 RUNNING
  └─ 连续重连失败超过 10 次 → 进程退出（由外部 orchestrator 重启）

Core 侧:
  ├─ session.Context.Done() 触发
  ├─ 延迟清理（2 × heartbeat_timeout = 180s）
  │   └─ 若 Plugin 在此期间重连 → 保留已注册的 entities/metrics
  └─ 超时未重连 → 标记 Plugin state='error', 清理 session
```

---

## 五、数据库表变更

M1 基于 `migrations/001_baseline.up.sql` 中已定义的表结构，**不新增表**。新增索引/约束见下：

### 5.1 plugins 表增强

```sql
-- 去重：同一 token 同时只允许一个 RUNNING session
-- Plugin 重连时复用 plugin_id
ALTER TABLE plugins ADD COLUMN token_hash TEXT;
CREATE UNIQUE INDEX idx_plugins_token_active ON plugins(token_hash) WHERE state IN ('starting', 'running');
```

### 5.2 注册相关索引（baseline 已有，此处确认）

- `idx_entities_current` — 部分索引 `WHERE effective_to IS NULL`，保证查当前生效 Entity
- `idx_metric_def_current` — 同上

### 5.3 command_log 表

已存在于 baseline DDL，M1 仅写入 `SyncCommand` / `BackfillCommand` 的生命周期记录，实际命令执行在 M5。

---

## 六、API/协议变更

### 6.1 proto 变更（相对 baseline）

M1 的 plugin.proto 已在 adr.md 中冻结（含 push_ack）。M1 确认以下消息：

```protobuf
// 注册 — 新增 token 认证字段
message RegisterPluginRequest {
  PluginInfo info = 1;
  repeated EntityDeclaration entities = 2;
  repeated RelationSuggestion relations = 3;
  repeated MetricDeclaration metrics = 4;
  repeated RuleSuggestion rules = 5;
  string auth_token = 6;          // M1: Plugin 持有的认证 token
  string change_log = 10;
}

message RegisterPluginResponse {
  string plugin_id = 1;
  int32 registration_version = 2;
  bool success = 3;
  string message = 4;
  repeated RelationReview reviewed_relations = 5;
  repeated RuleReview reviewed_rules = 6;
  string stream_token = 7;        // M1: 短期有效的 stream 建立凭证
}
```

### 6.2 心跳增强

```protobuf
message HeartbeatRequest {
  string plugin_id = 1;
  int64 timestamp = 2;
  PluginStatus status = 3;
  string stream_token = 4;        // 连续性验证
}
```

### 6.3 新增 unary RPC（去重预防）

```protobuf
service PluginHost {
  rpc RegisterPlugin(RegisterPluginRequest) returns (RegisterPluginResponse);
  rpc MaintainSession(stream PluginMessage) returns (stream CoreMessage);
  rpc Heartbeat(HeartbeatRequest) returns (HeartbeatResponse);
  rpc HealthCheck(HealthCheckRequest) returns (HealthCheckResponse);  // M1: Core 自身健康探针
}
```

---

## 七、验收标准

1. **注册成功**：3 个 Plugin（ETF、Crypto、Macro）各自启动后，Core 日志中可见 `registered: plg_etf, registration_version=1`，`plugins` 表对应行 `state='running'`。
2. **Entity/Metric 注册验证**：ETF Plugin 注册后，`entities_v2` 至少有 3 行，`metric_definitions_v2` 至少有 5 行，所有行的 `effective_to IS NULL` 且 `version=1`。
3. **心跳保活**：正常运行 5 分钟，Core metrics 显示 `plugin_heartbeat_total` 递增，无 timeout 错误。
4. **断连重连**：手动断开 Plugin 网络（`docker network disconnect`），30 秒内 Plugin 自动重连，`plugins.state` 短暂变为 `degraded` 后恢复 `running`。
5. **Token 拒绝**：使用错误 token 调用 `RegisterPlugin`，Core 返回 `success=false, message="invalid token"`，日志输出 security event。

---

## 八、边界/局限

- **不做 Observations 写入**：M1 不实现 `PushSnapshots` → observations 的写入逻辑；stream 上的 PushSnapshots 消息会被接收但不处理（返回空 ack）。M2 补全。
- **不做 Detector/Alert**：M1 不运行检测引擎；Rule/Relation 已注册但不会被执行。
- **不做 UI**：M1 只验证后端注册链路；前端页面在 M5。
- **不做 Plugin 升级**：M1 只处理首次注册（`registration_version=1`）；升级流程（versioned re-registration）在后续里程碑补充。
- **Token 认证为最小实现**：M1 token 通过环境变量注入，不支持轮换、撤销、RBAC。
- **不做多级部署**：M1 仅验证单机 Docker Compose 部署；K8s / 多节点不在范围。
- **Relation 审核仅覆盖 structural/semantic/statistical/auto**：causal 层仍 pending，需人工审核流程后续实现。

---

## 九、与后续里程碑的接口

| 下游里程碑 | M1 提供的能力 |
|-----------|--------------|
| M2 Observation Ingest | `MaintainSession` stream 上的 `push_snapshots` 消息承接；`plugin_id` → `tokens` 流的 source_plugin 关联 |
| M3 Rule Evaluation | 已注册的 `rules_v2` 数据被 Detector Engine 轮询执行 |
| M4 Research Assembly | 已注册的 `entities_v2` + `relations_v2` 构成 ontology 图的基础数据 |
| M5 Control + Frontend | `command_log` 表的 Sync/Backfill 命令实际执行；Plugin state 展示 |
