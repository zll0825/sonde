-- migration 007

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
    last_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- taxonomy-valid direction (P1 #12)
    CONSTRAINT chk_manual_direction CHECK (direction IN ('forward', 'undirected')),
    -- unique active pair+type to prevent duplicate active relations (P1 #12)
    CONSTRAINT uq_manual_active_pair UNIQUE (source_id, target_id, relation_type, direction)
        WHERE active
);

CREATE INDEX IF NOT EXISTS idx_manual_relations_source ON manual_relations (source_id) WHERE active;
CREATE INDEX IF NOT EXISTS idx_manual_relations_target ON manual_relations (target_id) WHERE active;
CREATE INDEX IF NOT EXISTS idx_manual_relations_active ON manual_relations (active);

COMMENT ON TABLE manual_relations IS 'User-defined / manually approved relations; active rows participate in the canonical graph';
COMMENT ON COLUMN manual_relations.relation_id IS 'Primary key, format rel_<hex>';
COMMENT ON COLUMN manual_relations.source_id IS 'Source entity id; REFERENCES entities(id) not enforced to allow cross-plugin staging';
COMMENT ON COLUMN manual_relations.target_id IS 'Target entity id';
COMMENT ON COLUMN manual_relations.relation_type IS 'Relation type; must be a key in internal/core/relationmgr/taxonomy.go';
COMMENT ON COLUMN manual_relations.direction IS 'forward | undirected — see lookup constraint below';
COMMENT ON COLUMN manual_relations.weight IS '0..1 strength, validated by CHECK(0<=weight AND weight<=1)';
COMMENT ON COLUMN manual_relations.user_id IS 'User that created the relation (audit)';
COMMENT ON COLUMN manual_relations.active IS 'Soft delete flag; TRUE means participates in graph';
COMMENT ON COLUMN manual_relations.evidence IS 'JSON evidence (p_value, sample_size, lookback_days, method)';
COMMENT ON COLUMN manual_relations.last_updated_at IS 'Last update timestamp';
