-- 002 — 中文表与字段注释
-- 为全部 14 张表及其字段补充中文 COMMENT，供 psql \d+ 与各类数据库工具查看。
-- 仅注释，不改任何结构。

-- ============================================================
-- 1. plugins — 插件注册表
-- ============================================================
COMMENT ON TABLE plugins IS '插件注册表：每个数据采集插件一行，记录版本、健康状态与最近采集情况';
COMMENT ON COLUMN plugins.id IS '插件 ID（如 plg_etf），注册时由 Core 分配';
COMMENT ON COLUMN plugins.name IS '插件名（etf / macro / crypto）';
COMMENT ON COLUMN plugins.version IS '插件语义化版本号';
COMMENT ON COLUMN plugins.description IS '插件描述';
COMMENT ON COLUMN plugins.state IS '生命周期状态：starting / running / stopped';
COMMENT ON COLUMN plugins.healthy IS '健康标记，由心跳与采集结果综合判定';
COMMENT ON COLUMN plugins.last_heartbeat IS '最近一次心跳时间';
COMMENT ON COLUMN plugins.last_collect_at IS '最近一次采集时间';
COMMENT ON COLUMN plugins.last_collect_count IS '最近一次采集的快照条数';
COMMENT ON COLUMN plugins.last_collect_error IS '最近一次采集错误信息（成功则为空）';
COMMENT ON COLUMN plugins.consecutive_errors IS '连续失败次数，用于健康判定';
COMMENT ON COLUMN plugins.registration_version IS '注册内容版本号，注册负载变化时递增';

-- ============================================================
-- 2. entities — 实体（行级版本管理）
-- ============================================================
COMMENT ON TABLE entities IS '实体表：插件申报的观测对象（资产/机构/市场），行级版本管理，(id, version) 为主键';
COMMENT ON COLUMN entities.id IS '实体 ID（如 GLD、FED、US、BTC）';
COMMENT ON COLUMN entities.version IS '版本号，注册内容变化时新增一行';
COMMENT ON COLUMN entities.name IS '实体名称';
COMMENT ON COLUMN entities.namespace IS '命名空间（构成三段式指标 ID 的第一段，如 gld / fed / us / btc）';
COMMENT ON COLUMN entities.entity_type IS '实体类型：asset / institution / market';
COMMENT ON COLUMN entities.plugin_id IS '申报插件';
COMMENT ON COLUMN entities.tags IS '标签数组（JSONB）';
COMMENT ON COLUMN entities.metadata IS '扩展元数据（JSONB）';
COMMENT ON COLUMN entities.effective_from IS '本版本生效时间';
COMMENT ON COLUMN entities.effective_to IS '本版本失效时间；NULL 表示当前版本';
COMMENT ON COLUMN entities.supersedes IS '被本版本取代的旧版本号';
COMMENT ON COLUMN entities.change_log IS '版本变更说明';

-- ============================================================
-- 3. metric_definitions — 指标定义（行级版本管理）
-- ============================================================
COMMENT ON TABLE metric_definitions IS '指标定义表：三段式指标 ID <实体>.<类型缩写>.<指标>（如 gld.ass.price），行级版本管理';
COMMENT ON COLUMN metric_definitions.id IS '指标 ID，三段式：<实体>.<类型缩写>.<指标>（ass=asset、ins=institution、mkt=market）';
COMMENT ON COLUMN metric_definitions.uid IS '全局唯一 UID，observations 以此关联（指标改版时 uid 不变）';
COMMENT ON COLUMN metric_definitions.version IS '版本号';
COMMENT ON COLUMN metric_definitions.name IS '指标名称';
COMMENT ON COLUMN metric_definitions.description IS '指标描述';
COMMENT ON COLUMN metric_definitions.unit IS '计量单位（USD、percent、EH/s 等）';
COMMENT ON COLUMN metric_definitions.frequency IS '声明频率：realtime / hourly / daily / weekly，检测回看窗口据此推算';
COMMENT ON COLUMN metric_definitions.entity_id IS '所属实体 ID';
COMMENT ON COLUMN metric_definitions.plugin_id IS '申报插件';
COMMENT ON COLUMN metric_definitions.active IS '是否启用';
COMMENT ON COLUMN metric_definitions.effective_from IS '本版本生效时间';
COMMENT ON COLUMN metric_definitions.effective_to IS '本版本失效时间；NULL 表示当前版本';
COMMENT ON COLUMN metric_definitions.supersedes IS '被取代的旧版本号';
COMMENT ON COLUMN metric_definitions.change_log IS '版本变更说明';

-- ============================================================
-- 4. relation_suggestions — 关系建议（评审队列）
-- ============================================================
COMMENT ON TABLE relation_suggestions IS '关系建议队列：插件申报的实体关系先入此表，评审通过后写入权威表 relations（ADR-2）';
COMMENT ON COLUMN relation_suggestions.source_id IS '关系源实体 ID';
COMMENT ON COLUMN relation_suggestions.target_id IS '关系目标实体 ID';
COMMENT ON COLUMN relation_suggestions.relation_type IS '关系类型（需在 relationmgr 分类学注册表内）';
COMMENT ON COLUMN relation_suggestions.direction IS '方向：forward / bidirectional';
COMMENT ON COLUMN relation_suggestions.confidence IS '置信度 0–1';
COMMENT ON COLUMN relation_suggestions.typical_lag IS '典型传导时滞';
COMMENT ON COLUMN relation_suggestions.evidence IS '申报依据说明';
COMMENT ON COLUMN relation_suggestions.plugin_id IS '申报插件';
COMMENT ON COLUMN relation_suggestions.status IS '评审状态：pending / accepted / rejected / merged';
COMMENT ON COLUMN relation_suggestions.merged_into_id IS '被合并到的 relations 行 ID';
COMMENT ON COLUMN relation_suggestions.review_reason IS '评审结论说明';

-- ============================================================
-- 5. relations — 关系（权威表，行级版本管理）
-- ============================================================
COMMENT ON TABLE relations IS '实体关系权威表：评审通过的关系，行级版本管理，研究组装用它扩展告警上下文';
COMMENT ON COLUMN relations.source_id IS '关系源实体 ID';
COMMENT ON COLUMN relations.target_id IS '关系目标实体 ID';
COMMENT ON COLUMN relations.relation_type IS '关系类型';
COMMENT ON COLUMN relations.layer IS '所属层：macro（宏观）/ capital（资金）/ market（市场）';
COMMENT ON COLUMN relations.direction IS '方向：forward / bidirectional';
COMMENT ON COLUMN relations.confidence IS '置信度 0–1';
COMMENT ON COLUMN relations.typical_lag IS '典型传导时滞';
COMMENT ON COLUMN relations.source IS '来源：plugin_suggested / manual';
COMMENT ON COLUMN relations.version IS '版本号';
COMMENT ON COLUMN relations.effective_from IS '本版本生效时间';
COMMENT ON COLUMN relations.effective_to IS '本版本失效时间；NULL 表示当前版本';

-- ============================================================
-- 6. rule_suggestions — 规则建议（评审队列）
-- ============================================================
COMMENT ON TABLE rule_suggestions IS '规则建议队列：插件申报的检测规则先入此表，评审通过后写入权威表 rules（ADR-2）';
COMMENT ON COLUMN rule_suggestions.name IS '规则名';
COMMENT ON COLUMN rule_suggestions.metric_id IS '作用的指标 ID';
COMMENT ON COLUMN rule_suggestions.detector_name IS '探测器：threshold / percentile / trend';
COMMENT ON COLUMN rule_suggestions.severity IS '触发告警的严重级别：info / warning / critical';
COMMENT ON COLUMN rule_suggestions.config IS '探测器配置（JSONB：阈值、分位、连续期数等）';
COMMENT ON COLUMN rule_suggestions.plugin_id IS '申报插件';
COMMENT ON COLUMN rule_suggestions.status IS '评审状态：pending / accepted / rejected';
COMMENT ON COLUMN rule_suggestions.review_reason IS '评审结论说明';

-- ============================================================
-- 7. rules — 检测规则（权威表，行级版本管理）
-- ============================================================
COMMENT ON TABLE rules IS '检测规则权威表：探测引擎按启用规则评估观测；行级版本管理，调参产生新版本行';
COMMENT ON COLUMN rules.name IS '规则名';
COMMENT ON COLUMN rules.metric_id IS '作用的指标 ID';
COMMENT ON COLUMN rules.detector_name IS '探测器：threshold / percentile / trend';
COMMENT ON COLUMN rules.severity IS '触发告警的严重级别：info / warning / critical';
COMMENT ON COLUMN rules.config IS '探测器配置（JSONB）。threshold: operator+value；percentile: percentile+min_observations；trend: direction+consecutive';
COMMENT ON COLUMN rules.enabled IS '是否启用（禁用即静默该规则）';
COMMENT ON COLUMN rules.source IS '来源：plugin_suggested / manual';
COMMENT ON COLUMN rules.is_override IS '是否人工覆盖插件建议';
COMMENT ON COLUMN rules.version IS '版本号';
COMMENT ON COLUMN rules.effective_from IS '本版本生效时间';
COMMENT ON COLUMN rules.effective_to IS '本版本失效时间；NULL 表示当前版本';

-- ============================================================
-- 8. observations — 观测数据（TimescaleDB 超表）
-- ============================================================
COMMENT ON TABLE observations IS '观测数据超表（TimescaleDB，按 time 分片）：全部指标的时序数据，幂等键 (metric_uid, time, source_plugin, source_provider, labels_hash)';
COMMENT ON COLUMN observations.time IS '观测时间（数据本身的时间，非入库时间）';
COMMENT ON COLUMN observations.metric_id IS '指标 ID（三段式，冗余存储便于排查）';
COMMENT ON COLUMN observations.metric_uid IS '指标 UID，关联 metric_definitions.uid，查询一律用它';
COMMENT ON COLUMN observations.value IS '观测值';
COMMENT ON COLUMN observations.labels IS '维度标签（JSONB）';
COMMENT ON COLUMN observations.labels_hash IS '标签哈希，参与幂等唯一索引';
COMMENT ON COLUMN observations.source_plugin IS '来源插件名';
COMMENT ON COLUMN observations.source_plugin_version IS '来源插件版本';
COMMENT ON COLUMN observations.source_provider IS '数据提供方：yahoo / fred / coingecko / mempool_space / mock_*';
COMMENT ON COLUMN observations.source_class IS '来源类别：real / mock / test / unknown；由每条 provider snapshot 显式声明';
COMMENT ON COLUMN observations.source_fetched_at IS '插件从上游取数的时间';
COMMENT ON COLUMN observations.quality_grade IS '质量等级：realtime（实时）/ delayed（延迟发布）/ estimated（估算或合成）';
COMMENT ON COLUMN observations.quality_confidence IS '插件申报的置信度 0–1';
COMMENT ON COLUMN observations.system_quality_score IS 'Core 计算的综合质量分（来源优先级 + 等级 + 新鲜度）';
COMMENT ON COLUMN observations.ingested_at IS '入库时间';

-- ============================================================
-- 9. pending_metrics — 未申报指标登记
-- ============================================================
COMMENT ON TABLE pending_metrics IS '未申报指标登记：插件推送了未在注册中申报的指标时在此记录，等待人工处理';
COMMENT ON COLUMN pending_metrics.metric_id IS '未识别的指标 ID';
COMMENT ON COLUMN pending_metrics.first_seen_at IS '首次出现时间';
COMMENT ON COLUMN pending_metrics.first_seen_from_plugin IS '首次推送它的插件';
COMMENT ON COLUMN pending_metrics.status IS '处理状态：unknown_source / acknowledged / resolved';

-- ============================================================
-- 10. source_preferences — 来源优先级
-- ============================================================
COMMENT ON TABLE source_preferences IS '来源优先级：同一指标存在多个数据来源时的取舍依据，质量评分参考 priority';
COMMENT ON COLUMN source_preferences.metric_id IS '指标 ID';
COMMENT ON COLUMN source_preferences.source_plugin IS '来源插件';
COMMENT ON COLUMN source_preferences.source_provider IS '数据提供方';
COMMENT ON COLUMN source_preferences.priority IS '优先级，数值大者优先';
COMMENT ON COLUMN source_preferences.is_manual_override IS '是否人工指定（覆盖系统默认）';
COMMENT ON COLUMN source_preferences.reason IS '设定理由';
COMMENT ON COLUMN source_preferences.set_by IS '设定者：system / 人工标识';

-- ============================================================
-- 11. alerts — 告警
-- ============================================================
COMMENT ON TABLE alerts IS '告警表：规则触发经去重后落库；active 状态下同 dedup_key 唯一（部分唯一索引），条件恢复自动 resolve';
COMMENT ON COLUMN alerts.id IS '告警 ID';
COMMENT ON COLUMN alerts.title IS '告警标题';
COMMENT ON COLUMN alerts.summary IS '告警摘要';
COMMENT ON COLUMN alerts.severity IS '严重级别：info / warning / critical';
COMMENT ON COLUMN alerts.status IS '状态：active / resolved';
COMMENT ON COLUMN alerts.metric_id IS '触发指标 ID';
COMMENT ON COLUMN alerts.rule_id IS '触发规则 ID';
COMMENT ON COLUMN alerts.rule_version IS '触发时的规则版本（复盘时可还原当时配置）';
COMMENT ON COLUMN alerts.rule_effective_from IS '触发时规则版本的生效时间';
COMMENT ON COLUMN alerts.detector_name IS '产出触发的探测器';
COMMENT ON COLUMN alerts.dedup_key IS '去重键（metric_id + rule_id 派生）；active 状态下唯一';
COMMENT ON COLUMN alerts.source_provider IS '触发该告警的最新观测的数据提供方（冻结值）';
COMMENT ON COLUMN alerts.source_class IS '触发该告警的最新观测来源类别（冻结值）';
COMMENT ON COLUMN alerts.dedup_count IS 'active 告警被后续相同触发折叠的累计次数';
COMMENT ON COLUMN alerts.last_deduplicated_at IS '最近一次 active 告警去重发生时间';
COMMENT ON COLUMN alerts.window_start IS '评估窗口起点';
COMMENT ON COLUMN alerts.window_end IS '评估窗口终点';
COMMENT ON COLUMN alerts.evidence IS '触发证据（JSONB：metric_uid、观测值、阈值/分位等），研究组装从这里取 metric_uid';
COMMENT ON COLUMN alerts.plugin_id IS '数据来源插件';
COMMENT ON COLUMN alerts.triggered_at IS '触发时间';
COMMENT ON COLUMN alerts.resolved_at IS '解除时间（自动 resolve 或人工）';

-- ============================================================
-- 12. research_snapshots — 研究快照
-- ============================================================
COMMENT ON TABLE research_snapshots IS '研究快照：告警触发后异步组装的研究上下文（指标+实体+关系+近期趋势），前端详情页数据源';
COMMENT ON COLUMN research_snapshots.alert_id IS '关联告警 ID（一比一）';
COMMENT ON COLUMN research_snapshots.context IS '完整研究上下文（JSONB）';
COMMENT ON COLUMN research_snapshots.ontology_frozen_at IS '组装时本体的冻结时间点（保证快照可复现）';

-- ============================================================
-- 13. event_outbox — 事件出箱
-- ============================================================
COMMENT ON TABLE event_outbox IS '事件出箱（outbox 模式）：与业务写入同事务落表，worker 轮询派发到通知/研究等异步消费方，保证不丢事件';
COMMENT ON COLUMN event_outbox.event_type IS '事件类型（如 alert.triggered）';
COMMENT ON COLUMN event_outbox.payload IS '事件负载（JSONB）';
COMMENT ON COLUMN event_outbox.dedup_key IS '事件幂等键；同事件类型内唯一';
COMMENT ON COLUMN event_outbox.status IS '状态：pending / dispatched / failed';
COMMENT ON COLUMN event_outbox.attempts IS '已尝试派发次数（超过重试预算置 failed）';
COMMENT ON COLUMN event_outbox.last_error IS '最近一次派发错误';
COMMENT ON COLUMN event_outbox.next_attempt_at IS '下次允许重试时间（指数退避）';
COMMENT ON COLUMN event_outbox.updated_at IS '最近状态更新时间';

-- ============================================================
-- 14. command_log — 控制命令日志
-- ============================================================
COMMENT ON TABLE command_log IS '控制命令日志：API 写入 pending 命令，Core 轮询派发给目标插件（Sync 即时采集 / Backfill 窗口回填），Ack 回执更新状态';
COMMENT ON COLUMN command_log.command_id IS '命令 ID';
COMMENT ON COLUMN command_log.command_type IS '命令类型：sync / backfill';
COMMENT ON COLUMN command_log.target_plugin IS '目标插件名';
COMMENT ON COLUMN command_log.requested_by IS '发起者';
COMMENT ON COLUMN command_log.status IS '状态：pending / dispatched / completed / failed';
COMMENT ON COLUMN command_log.reason IS '发起原因';
COMMENT ON COLUMN command_log.metric_ids IS '指定的指标范围（JSONB 数组，空为全部）';
COMMENT ON COLUMN command_log.window_start IS 'Backfill 回填窗口起点';
COMMENT ON COLUMN command_log.window_end IS 'Backfill 回填窗口终点';
COMMENT ON COLUMN command_log.collected_count IS '实际采集条数（Ack 回执带回）';
COMMENT ON COLUMN command_log.error IS '失败信息';
COMMENT ON COLUMN command_log.requested_at IS '发起时间';
COMMENT ON COLUMN command_log.accepted_at IS '插件接受时间';
COMMENT ON COLUMN command_log.completed_at IS '完成时间';
COMMENT ON COLUMN command_log.attempts IS '原子领取命令的累计次数；最多 5 次';
COMMENT ON COLUMN command_log.last_dispatched_at IS '最近一次领取并准备派发的时间';
COMMENT ON COLUMN command_log.lease_expires_at IS 'dispatched 状态的租约到期时间；到期可被重新领取';
COMMENT ON COLUMN command_log.last_error IS '最近一次派发、租约或插件执行错误；完成后仍保留历史';
COMMENT ON COLUMN command_log.updated_at IS '最近一次命令生命周期状态更新时间';
