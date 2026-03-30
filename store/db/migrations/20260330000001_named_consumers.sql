-- migrate:up

-- Named event consumers with inline lock fields.
CREATE TABLE IF NOT EXISTS entity_event_consumers (
    name          TEXT PRIMARY KEY,
    last_event_at TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01',
    last_event_id UUID,
    holder_id     TEXT,
    acquired_at   TIMESTAMPTZ,
    expires_at    TIMESTAMPTZ,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- LISTEN/NOTIFY trigger for instant event delivery.
CREATE OR REPLACE FUNCTION notify_entity_event() RETURNS TRIGGER AS $$
BEGIN
    PERFORM pg_notify('entity_events', '');
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER entity_events_notify
    AFTER INSERT ON entity_events
    FOR EACH ROW EXECUTE FUNCTION notify_entity_event();

-- migrate:down
DROP TRIGGER IF EXISTS entity_events_notify ON entity_events;
DROP FUNCTION IF EXISTS notify_entity_event();
DROP TABLE IF EXISTS entity_event_consumers;
