-- migrate:up
-- Event store: replaces entity_provenance with a proto-first event table.
-- Events use UUIDv7 (generated in Go) for time-sortable primary keys.

DROP TABLE IF EXISTS entity_provenance;

CREATE TABLE entity_events (
    id              UUID NOT NULL,
    event_type      TEXT NOT NULL,
    payload_type    TEXT NOT NULL,
    payload         JSONB NOT NULL,
    entity_id       UUID,
    relation_key    TEXT,
    tags            TEXT[],
    occurred_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at    TIMESTAMPTZ,
    PRIMARY KEY (id, occurred_at)
) PARTITION BY RANGE (occurred_at);

CREATE TABLE entity_events_default PARTITION OF entity_events DEFAULT;

CREATE INDEX idx_events_entity ON entity_events (entity_id, occurred_at DESC) WHERE entity_id IS NOT NULL;
CREATE INDEX idx_events_type ON entity_events (event_type, occurred_at DESC);
CREATE INDEX idx_events_relation ON entity_events (relation_key, occurred_at DESC) WHERE relation_key IS NOT NULL;
CREATE INDEX idx_events_unpublished ON entity_events (occurred_at) WHERE published_at IS NULL;
CREATE INDEX idx_events_tags ON entity_events USING GIN (tags);

-- migrate:down
DROP TABLE IF EXISTS entity_events_default;
DROP TABLE IF EXISTS entity_events;
