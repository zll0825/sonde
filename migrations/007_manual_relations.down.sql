-- migration 007

DROP INDEX IF EXISTS idx_manual_relations_source;
DROP INDEX IF EXISTS idx_manual_relations_target;
DROP INDEX IF EXISTS idx_manual_relations_active;
DROP TABLE IF EXISTS manual_relations;
