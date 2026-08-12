-- migration 007

DROP INDEX IF EXISTS idx_manual_relations_source;
DROP INDEX IF EXISTS idx_manual_relations_target;
DROP INDEX IF EXISTS idx_manual_relations_active;
DROP INDEX IF EXISTS uq_manual_active_pair;
DROP TABLE IF EXISTS manual_relations;
