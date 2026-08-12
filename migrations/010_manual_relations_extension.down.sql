-- migration 010 rollback

DROP INDEX IF EXISTS idx_manual_relations_extension;

ALTER TABLE manual_relations DROP COLUMN extension;
