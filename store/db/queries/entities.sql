-- name: GetEntity :one
SELECT id, entity_type, data, confidence, tags, created_at, updated_at
FROM entities
WHERE id = $1;

-- name: GetEntitiesByType :many
SELECT id, entity_type, data, confidence, tags, created_at, updated_at
FROM entities
WHERE entity_type = @entity_type
  AND (sqlc.narg('cursor')::timestamptz IS NULL OR updated_at < sqlc.narg('cursor')::timestamptz)
ORDER BY updated_at DESC
LIMIT @page_size;

-- name: GetEntitiesByTypeFiltered :many
SELECT id, entity_type, data, confidence, tags, created_at, updated_at
FROM entities
WHERE entity_type = @entity_type
  AND (sqlc.narg('cursor')::timestamptz IS NULL OR updated_at < sqlc.narg('cursor')::timestamptz)
  AND (cardinality(@tags::text[]) = 0 OR tags @> @tags::text[])
  AND (cardinality(@any_tags::text[]) = 0 OR tags && @any_tags::text[])
  AND (@exclude_tag = '' OR NOT (@exclude_tag = ANY(tags)) OR tags && @unless_tags::text[])
ORDER BY updated_at DESC
LIMIT @page_size;

-- name: InsertEntity :one
INSERT INTO entities (entity_type, data, confidence, tags)
VALUES ($1, $2, $3, $4)
RETURNING id, entity_type, data, confidence, tags, created_at, updated_at;

-- name: InsertEntityWithID :one
INSERT INTO entities (id, entity_type, data, confidence, tags)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, entity_type, data, confidence, tags, created_at, updated_at;

-- name: UpdateEntityData :exec
UPDATE entities
SET data = $2, confidence = $3, updated_at = now()
WHERE id = $1;

-- name: MergeEntityData :exec
UPDATE entities
SET data = data || $2, confidence = $3, updated_at = now()
WHERE id = $1;

-- name: DeleteEntity :exec
DELETE FROM entities WHERE id = $1;
