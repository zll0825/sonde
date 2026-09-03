-- Migration 012: persist declared plugin capabilities on registration.
-- Cut 1 of the DBX layering plan: /api/status.expected_plugins reads this
-- even when the plugin process is currently down.

ALTER TABLE plugins
    ADD COLUMN IF NOT EXISTS capabilities JSONB NOT NULL DEFAULT '{}';

CREATE INDEX IF NOT EXISTS idx_plugins_capabilities
    ON plugins USING GIN (capabilities);

COMMENT ON COLUMN plugins.capabilities IS
    '注册时声明的能力：{windowed_backfill, max_backfill_days, requires_secrets, mock_available}';
