DROP INDEX IF EXISTS idx_plugins_capabilities;
ALTER TABLE plugins DROP COLUMN IF EXISTS capabilities;
