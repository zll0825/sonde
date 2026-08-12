-- Allow Core's statistical discovery pipeline to use the same explicit review
-- queue as plugin suggestions without inventing a synthetic plugin identity.

ALTER TABLE relation_suggestions
    ALTER COLUMN plugin_id DROP NOT NULL;

ALTER TABLE relation_suggestions
    ADD COLUMN source TEXT NOT NULL DEFAULT 'plugin_suggested';

ALTER TABLE relation_suggestions
    ADD CONSTRAINT chk_relation_suggestions_source
    CHECK (source IN ('plugin_suggested', 'system_inferred'));

CREATE UNIQUE INDEX uq_relation_suggestions_system_candidate
    ON relation_suggestions (source_id, target_id, relation_type)
    WHERE plugin_id IS NULL AND source = 'system_inferred';

COMMENT ON COLUMN relation_suggestions.plugin_id IS
    'Declaring plugin; NULL only for Core system-inferred candidates';
COMMENT ON COLUMN relation_suggestions.source IS
    'Candidate origin: plugin_suggested or system_inferred';
