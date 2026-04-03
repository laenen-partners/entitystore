-- migrate:up
ALTER TABLE entities ADD COLUMN version INT NOT NULL DEFAULT 0;

-- migrate:down
ALTER TABLE entities DROP COLUMN IF EXISTS version;
