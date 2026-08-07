DROP INDEX IF EXISTS idx_alerts_source_class_triggered;

ALTER TABLE alerts
    DROP CONSTRAINT IF EXISTS alerts_dedup_count_check,
    DROP CONSTRAINT IF EXISTS alerts_source_class_check,
    DROP COLUMN IF EXISTS last_deduplicated_at,
    DROP COLUMN IF EXISTS dedup_count,
    DROP COLUMN IF EXISTS source_class,
    DROP COLUMN IF EXISTS source_provider;

ALTER TABLE observations
    DROP CONSTRAINT IF EXISTS observations_source_class_check,
    DROP COLUMN IF EXISTS source_class;
