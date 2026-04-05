-- migrate:up
ALTER TABLE entity_events ADD COLUMN entity_type TEXT NOT NULL DEFAULT '';

-- Backfill from existing entities where possible.
UPDATE entity_events SET entity_type = e.entity_type
FROM entities e WHERE entity_events.entity_id = e.id
AND entity_events.entity_type = '';

CREATE INDEX idx_events_entity_type ON entity_events(entity_type) WHERE entity_type != '';

-- migrate:down
DROP INDEX IF EXISTS idx_events_entity_type;
ALTER TABLE entity_events DROP COLUMN IF EXISTS entity_type;
