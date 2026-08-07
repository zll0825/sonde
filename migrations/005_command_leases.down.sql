DROP INDEX IF EXISTS idx_command_log_dispatchable;

ALTER TABLE command_log
    DROP COLUMN IF EXISTS updated_at,
    DROP COLUMN IF EXISTS last_error,
    DROP COLUMN IF EXISTS lease_expires_at,
    DROP COLUMN IF EXISTS last_dispatched_at,
    DROP COLUMN IF EXISTS attempts;
