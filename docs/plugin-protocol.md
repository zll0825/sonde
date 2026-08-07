# Capital Observatory — Plugin Protocol

版本：1.0
状态：Frozen（Sprint 0.2）

---

## 说明

本文档定义 Plugin ↔ Core 的完整 gRPC 接口。

定义顺序：
1. 枚举和基础类型
2. 嵌套消息（自底向上）
3. RPC 服务

每个字段旁标注对应 Domain Model 的对象/字段，确保 proto ↔ domain 可追溯。

---

## 枚举

```protobuf
// Domain: Entity.EntityType
enum EntityType {
  ENTITY_TYPE_UNSPECIFIED = 0;
  ENTITY_TYPE_ASSET = 1;
  ENTITY_TYPE_INSTRUMENT = 2;
  ENTITY_TYPE_FLOW = 3;
  ENTITY_TYPE_INSTITUTION = 4;
  ENTITY_TYPE_INDICATOR = 5;
  ENTITY_TYPE_INDEX = 6;
  ENTITY_TYPE_MARKET = 7;
}

// Domain: Relation.Layer
enum RelationLayer {
  RELATION_LAYER_UNSPECIFIED = 0;
  RELATION_LAYER_STRUCTURAL = 1;
  RELATION_LAYER_SEMANTIC = 2;
  RELATION_LAYER_STATISTICAL = 3;
  RELATION_LAYER_CAUSAL = 4;
}

// Domain: Relation.Direction
enum Direction {
  DIRECTION_UNSPECIFIED = 0;
  DIRECTION_FORWARD = 1;
  DIRECTION_BACKWARD = 2;
  DIRECTION_BIDIRECTIONAL = 3;
}

// Domain: Rule.Severity / Alert.Severity
enum Severity {
  SEVERITY_UNSPECIFIED = 0;
  SEVERITY_CRITICAL = 1;
  SEVERITY_WARNING = 2;
  SEVERITY_INFO = 3;
}

// Domain: Observation.quality_grade
enum QualityGrade {
  QUALITY_GRADE_UNSPECIFIED = 0;
  QUALITY_GRADE_REALTIME = 1;
  QUALITY_GRADE_DELAYED = 2;
  QUALITY_GRADE_ESTIMATED = 3;
  QUALITY_GRADE_PRELIMINARY = 4;
  QUALITY_GRADE_REVISED = 5;
}
```

---

## 嵌套消息

### 基础

```protobuf
// Domain: Plugin
message PluginInfo {
  string name = 1;                    // "etf"
  string version = 2;                 // semver, "1.2.0"
  string description = 3;
  int32 collection_interval_secs = 4; // how often this plugin collects
  int32 min_backfill_window_days = 5; // max backfill range
}
```

### 注册 — Entity

```protobuf
// Domain: Entity
message EntityDeclaration {
  string id = 1;             // "gold_etf_flow"
  string name = 2;           // "黄金ETF资金流"
  string namespace = 3;      // "etf"
  EntityType entity_type = 4;
  repeated string tags = 5;
  map<string, string> metadata = 6;
}
```

### 注册 — Metric

```protobuf
// Domain: Metric
message MetricDeclaration {
  string id = 1;             // "gold.etf.net_inflow"
  string name = 2;           // "黄金ETF净流入"
  string description = 3;
  string unit = 4;           // "USD", "%", "count", "bps"
  string frequency = 5;      // "daily", "hourly", "realtime", "weekly", "quarterly"
  string entity_id = 6;      // → Entity.id
  map<string, string> tags = 7;
}
```

### 注册 — Relation（Suggestion）

```protobuf
// Domain: Relation (suggestion phase)
message RelationSuggestion {
  string source_id = 1;
  string target_id = 2;
  string relation_type = 3;       // "tracks", "causes", "leads", ...
  Direction direction = 4;
  double confidence = 5;
  int64 typical_lag_secs = 6;
  string description = 7;
  string evidence = 8;            // free-text justification

  RelationLayer suggested_layer = 10;  // Plugin's suggestion; Core overrides via taxonomy
  StatisticalEvidence statistical_evidence = 11; // required for statistical layer
}

message StatisticalEvidence {
  string method = 1;          // "pearson", "spearman", "granger", "cointegration"
  int32 window_days = 2;
  double coefficient = 3;
  double p_value = 4;
  int32 sample_size = 5;
}

message RelationReview {
  string source_id = 1;
  string target_id = 2;
  string relation_type = 3;
  string decision = 4;        // "accepted" | "rejected" | "merged" | "modified"
  string reason = 5;
  RelationSuggestion merged_into = 6;
}
```

### 注册 — Rule（Suggestion）

```protobuf
// Domain: Rule (suggestion phase)
message RuleSuggestion {
  string name = 1;
  string metric_id = 2;           // → Metric.id
  string detector_name = 3;       // "threshold" | "percentile" | "trend" | "volatility" | "moving_average"
  Severity severity = 4;
  bytes config = 5;               // protojson-encoded detector config
  string description = 6;
}

message RuleReview {
  string name = 1;
  string decision = 2;            // "accepted" | "rejected" | "modified"
  string reason = 3;
}
```

### 注册 — 请求/响应

```protobuf
message RegisterPluginRequest {
  PluginInfo info = 1;
  repeated EntityDeclaration entities = 2;
  repeated RelationSuggestion relations = 3;
  repeated MetricDeclaration metrics = 4;
  repeated RuleSuggestion rules = 5;
  string change_log = 10;         // upgrade changelog, e.g. "v1.1→v1.2: ..."
}

message RegisterPluginResponse {
  string plugin_id = 1;           // "plg_etf"
  int32 registration_version = 2; // monotonic
  bool success = 3;
  string message = 4;
  repeated RelationReview reviewed_relations = 5;
  repeated RuleReview reviewed_rules = 6;
}
```

### 数据推送

```protobuf
enum SourceClass {
  SOURCE_CLASS_UNSPECIFIED = 0; // absent/legacy/invalid -> unknown
  SOURCE_CLASS_REAL = 1;
  SOURCE_CLASS_MOCK = 2;
  SOURCE_CLASS_TEST = 3;
}

// Domain: Observation snapshot
message MetricSnapshot {
  string metric_id = 1;                // "gold.etf.net_inflow"
  double value = 2;
  int64 timestamp = 3;                 // unix seconds, UTC
  map<string, string> labels = 4;

  string source_plugin = 5;            // "etf"
  string source_plugin_version = 6;    // "1.2.0"
  string source_provider = 7;          // "yahoo_finance", "glassnode", "fred"
  int64 source_fetched_at = 8;

  QualityGrade quality_grade = 9;
  double quality_confidence = 10;      // Plugin self-assessment
  SourceClass source_class = 11;       // explicit per snapshot; never inferred from names
}

message PushSnapshotsRequest {
  string plugin_id = 1;
  repeated MetricSnapshot snapshots = 2;
}

message PushSnapshotsResponse {
  bool success = 1;
  int32 inserted = 2;       // newly written
  int32 deduplicated = 3;   // skipped due to idempotency
  int32 rejected = 4;       // validation failures
  string message = 5;
}
```

### 心跳

```protobuf
message HeartbeatRequest {
  string plugin_id = 1;
  int64 timestamp = 2;
  PluginStatus status = 3;
}

message PluginStatus {
  string state = 1;                    // "starting" | "running" | "degraded" | "stopping" | "error"
  int64 last_collect_at = 2;
  int32 last_collect_duration_ms = 3;
  int32 last_collect_count = 4;
  string last_collect_error = 5;
  int32 consecutive_errors = 6;
  map<string, string> runtime = 7;     // "go_version", "goroutines", "mem_alloc_mb", "uptime_secs"
}

message HeartbeatResponse {
  bool ok = 1;
  bool has_pending_command = 2;        // Core tells Plugin: "I have a command for you"
}
```

### 命令

```protobuf
message SyncCommand {
  string command_id = 1;
  string reason = 2;                   // "manual_trigger" | "post_upgrade" | "scheduled"
  repeated string metric_ids = 3;      // empty = all
}

message BackfillCommand {
  string command_id = 1;
  string reason = 2;
  int64 window_start = 3;
  int64 window_end = 4;
  repeated string metric_ids = 5;
}

message CommandAck {
  string command_id = 1;
  string status = 2;           // "accepted" | "running" | "completed" | "failed"
  string message = 3;
  int64 timestamp = 4;
  int32 collected_count = 5;   // only when completed
  string error = 6;            // only when failed
}
```

### Bidirectional Stream

```protobuf
// Plugin → Core (on stream)
message PluginMessage {
  oneof payload {
    PushSnapshotsRequest push_snapshots = 1;
    HeartbeatRequest heartbeat = 2;
    CommandAck command_ack = 3;
  }
}

// Core → Plugin (on stream)
message CoreMessage {
  oneof payload {
    SyncCommand sync = 1;
    BackfillCommand backfill = 2;
  }
}
```

---

## 服务定义

```protobuf
service PluginHost {
  // One-time registration (unary)
  rpc RegisterPlugin(RegisterPluginRequest) returns (RegisterPluginResponse);

  // Data push + command delivery (bidirectional streaming)
  rpc MaintainSession(stream PluginMessage) returns (stream CoreMessage);

  // Lightweight liveness check (unary, stream fallback)
  rpc Heartbeat(HeartbeatRequest) returns (HeartbeatResponse);
}
```
