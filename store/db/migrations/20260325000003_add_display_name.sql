-- migrate:up
ALTER TABLE entities ADD COLUMN display_name TEXT NOT NULL DEFAULT '';

-- migrate:down
ALTER TABLE entities DROP COLUMN IF EXISTS display_name;
