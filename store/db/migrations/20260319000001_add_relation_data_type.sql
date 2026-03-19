-- migrate:up
ALTER TABLE entity_relations ADD COLUMN data_type TEXT NOT NULL DEFAULT '';

-- migrate:down
ALTER TABLE entity_relations DROP COLUMN data_type;
