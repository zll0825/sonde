-- Preserve Core-inferred review history while restoring the old plugin-only
-- schema. The archival identity is deliberately unhealthy/non-running so it
-- cannot be mistaken for a live data provider after rollback.
INSERT INTO plugins (id, name, version, description, state, healthy, registration_version)
VALUES (
    'plg_core_candidate_history',
    'core-candidate-history',
    '1',
    'Rollback archive for Core-inferred relation candidates',
    'stopped',
    FALSE,
    0
)
ON CONFLICT (id) DO NOTHING;

UPDATE relation_suggestions
SET plugin_id = 'plg_core_candidate_history',
    review_reason = concat_ws('; ',
        NULLIF(review_reason, ''),
        'source=system_inferred archived by migration 008 rollback'
    ),
    updated_at = NOW()
WHERE plugin_id IS NULL AND source = 'system_inferred';

DROP INDEX IF EXISTS uq_relation_suggestions_system_candidate;

ALTER TABLE relation_suggestions
    DROP CONSTRAINT IF EXISTS chk_relation_suggestions_source;

ALTER TABLE relation_suggestions
    DROP COLUMN source;

ALTER TABLE relation_suggestions
    ALTER COLUMN plugin_id SET NOT NULL;
