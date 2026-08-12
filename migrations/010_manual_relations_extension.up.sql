-- migration 010 — add extension JSONB column to manual_relations.
--
-- Statistical evidence (p_value, sample_size, lookback_days, lag_days,
-- evidence_refs) is stored here when a candidate is promoted to a manual
-- relation. Without this column the evidence discovered by the candidate
-- discovery pipeline is lost on acceptance (P1 #7).

ALTER TABLE manual_relations
    ADD COLUMN extension JSONB NOT NULL DEFAULT '{}';

CREATE INDEX IF NOT EXISTS idx_manual_relations_extension
    ON manual_relations USING GIN (extension);

COMMENT ON COLUMN manual_relations.extension IS
    'Statistical evidence: {p_value, sample_size, lookback_days, lag_days, evidence_refs}';
