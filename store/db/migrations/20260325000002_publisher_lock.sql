-- migrate:up
CREATE TABLE publisher_lock (
    id          TEXT PRIMARY KEY DEFAULT 'singleton',
    holder_id   TEXT NOT NULL,
    acquired_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL,
    renewed_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- migrate:down
DROP TABLE IF EXISTS publisher_lock;
