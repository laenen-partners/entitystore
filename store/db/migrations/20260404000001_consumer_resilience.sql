-- migrate:up

-- Dead letter table for events that fail processing after MaxRetries.
CREATE TABLE entity_event_dead_letters (
    id              BIGSERIAL PRIMARY KEY,
    consumer_name   TEXT NOT NULL,
    event_id        UUID NOT NULL,
    event_type      TEXT NOT NULL,
    payload_type    TEXT NOT NULL,
    entity_id       TEXT,
    payload         JSONB,
    error_message   TEXT NOT NULL,
    retry_count     INT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_dead_letters_consumer ON entity_event_dead_letters(consumer_name, created_at DESC);
CREATE INDEX idx_dead_letters_event ON entity_event_dead_letters(event_id);

-- Extend consumer state with failure tracking (persisted across restarts).
ALTER TABLE entity_event_consumers
    ADD COLUMN consecutive_failures INT NOT NULL DEFAULT 0,
    ADD COLUMN last_error TEXT,
    ADD COLUMN backoff_until TIMESTAMPTZ;

-- migrate:down
ALTER TABLE entity_event_consumers
    DROP COLUMN IF EXISTS consecutive_failures,
    DROP COLUMN IF EXISTS last_error,
    DROP COLUMN IF EXISTS backoff_until;

DROP TABLE IF EXISTS entity_event_dead_letters;
