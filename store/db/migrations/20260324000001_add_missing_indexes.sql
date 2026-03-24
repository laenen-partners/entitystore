-- migrate:up
CREATE INDEX IF NOT EXISTS idx_rel_target_type ON entity_relations (target_id, relation_type);
CREATE INDEX IF NOT EXISTS idx_rel_source_urn ON entity_relations (source_urn) WHERE source_urn IS NOT NULL;

-- migrate:down
DROP INDEX IF EXISTS idx_rel_target_type;
DROP INDEX IF EXISTS idx_rel_source_urn;
