-- migrate:up
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX IF NOT EXISTS idx_entities_display_name_trgm ON entities USING GIN (display_name gin_trgm_ops);

-- migrate:down
DROP INDEX IF EXISTS idx_entities_display_name_trgm;
