-- M0 Baseline — rollback
-- Drops all tables created in 001_baseline.up.sql (reverse order for FK safety)

DROP TABLE IF EXISTS command_log;
DROP TABLE IF EXISTS event_outbox;
DROP TABLE IF EXISTS research_snapshots;
DROP TABLE IF EXISTS alerts;
DROP TABLE IF EXISTS source_preferences;
DROP TABLE IF EXISTS pending_metrics;
DROP TABLE IF EXISTS observations;  -- hypertable
DROP TABLE IF EXISTS rules_v2;
DROP TABLE IF EXISTS rule_suggestions;
DROP TABLE IF EXISTS relations_v2;
DROP TABLE IF EXISTS relation_suggestions;
DROP TABLE IF EXISTS metric_definitions_v2;
DROP TABLE IF EXISTS entities_v2;
DROP TABLE IF EXISTS plugins;
