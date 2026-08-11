CREATE TABLE IF NOT EXISTS manual_relations (
    relation_id TEXT PRIMARY KEY,
    source_id TEXT NOT NULL,
    target_id TEXT NOT NULL,
    relation_type TEXT NOT NULL,
    direction TEXT NOT NULL DEFAULT 'forward',
    description TEXT NOT NULL DEFAULT '',
    weight NUMERIC(5,4) NOT NULL DEFAULT 0.5 CHECK (weight >= 0 AND weight <= 1),
    user_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    active BOOLEAN NOT NULL DEFAULT TRUE,
    evidence TEXT NOT NULL DEFAULT '',
    last_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_manual_relations_source ON manual_relations (source_id) WHERE active;
CREATE INDEX IF NOT EXISTS idx_manual_relations_target ON manual_relations (target_id) WHERE active;
CREATE INDEX IF NOT EXISTS idx_manual_relations_active ON manual_relations (active);
COMMENT ON TABLE manual_relations IS '手动创建的用户关系（用户审核后生效）';
COMMENT ON COLUMN manual_relations.relation_id IS '主键，格式 rel_<hex>';
COMMENT ON COLUMN manual_relations.source_id IS '源实体 id';
COMMENT ON COLUMN manual_relations.target_id IS '目标实体 id';
COMMENT ON COLUMN manual_relations.relation_type IS '关系类型（必须在 taxonomy 中注册）';
COMMENT ON COLUMN manual_relations.direction IS 'forward | undirected';
COMMENT ON COLUMN manual_relations.weight IS '0..1，用户自定义强度';
COMMENT ON COLUMN manual_relations.user_id IS '创建该关系的用户';
COMMENT ON COLUMN manual_relations.active IS '软删除标记';
COMMENT ON COLUMN manual_relations.evidence IS 'JSON 形式的证据（p_value, sample_size, lookback_days）';
COMMENT ON COLUMN manual_relations.last_updated_at IS '最近一次更新时间';
