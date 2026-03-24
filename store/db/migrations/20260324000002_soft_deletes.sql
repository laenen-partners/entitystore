-- migrate:up
ALTER TABLE entities ADD COLUMN deleted_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_entities_deleted ON entities (deleted_at) WHERE deleted_at IS NOT NULL;

-- migrate:down
DROP INDEX IF EXISTS idx_entities_deleted;
ALTER TABLE entities DROP COLUMN IF EXISTS deleted_at;
