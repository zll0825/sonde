UPDATE event_outbox
SET status = 'failed'
WHERE event_type = 'detection.requested' AND status = 'pending';

DROP INDEX IF EXISTS idx_event_outbox_pending;
DROP INDEX IF EXISTS idx_event_outbox_dedup;

ALTER TABLE event_outbox
    DROP COLUMN IF EXISTS updated_at,
    DROP COLUMN IF EXISTS next_attempt_at,
    DROP COLUMN IF EXISTS last_error,
    DROP COLUMN IF EXISTS dedup_key;
