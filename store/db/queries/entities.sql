-- name: GetEntity :one
SELECT id, entity_type, data, confidence, tags, display_name, created_at, updated_at
FROM entities
WHERE id = $1 AND deleted_at IS NULL;

-- name: GetEntitiesByType :many
SELECT id, entity_type, data, confidence, tags, display_name, created_at, updated_at
FROM entities
WHERE entity_type = @entity_type
  AND deleted_at IS NULL
  AND (sqlc.narg('cursor')::timestamptz IS NULL OR updated_at < sqlc.narg('cursor')::timestamptz)
ORDER BY updated_at DESC
LIMIT @page_size;

-- name: GetEntitiesByTypeFiltered :many
SELECT id, entity_type, data, confidence, tags, display_name, created_at, updated_at
FROM entities
WHERE entity_type = @entity_type
  AND deleted_at IS NULL
  AND (sqlc.narg('cursor')::timestamptz IS NULL OR updated_at < sqlc.narg('cursor')::timestamptz)
  AND (cardinality(@tags::text[]) = 0 OR tags @> @tags::text[])
  AND (cardinality(@any_tags::text[]) = 0 OR tags && @any_tags::text[])
  AND (@exclude_tag = '' OR NOT (@exclude_tag = ANY(tags)) OR tags && @unless_tags::text[])
ORDER BY updated_at DESC
LIMIT @page_size;

-- name: InsertEntity :one
INSERT INTO entities (entity_type, data, confidence, tags, display_name)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, entity_type, data, confidence, tags, display_name, created_at, updated_at;

-- name: InsertEntityWithID :one
INSERT INTO entities (id, entity_type, data, confidence, tags, display_name)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, entity_type, data, confidence, tags, display_name, created_at, updated_at;

-- name: UpdateEntityData :exec
UPDATE entities
SET data = $2, confidence = $3, display_name = $4, updated_at = now()
WHERE id = $1;

-- name: MergeEntityData :exec
UPDATE entities
SET data = data || $2, confidence = $3, display_name = $4, updated_at = now()
WHERE id = $1;

-- name: DeleteEntity :exec
UPDATE entities SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL;

-- name: HardDeleteEntity :exec
DELETE FROM entities WHERE id = $1;

-- name: CountEntitiesByType :one
SELECT count(*) FROM entities WHERE entity_type = @entity_type AND deleted_at IS NULL;

-- name: CountAllEntities :one
SELECT count(*) FROM entities WHERE deleted_at IS NULL;

-- name: CountRelationsForEntity :one
SELECT count(*) FROM entity_relations WHERE (source_id = @entity_id OR target_id = @entity_id);

-- name: CountAllRelations :one
SELECT count(*) FROM entity_relations;

-- name: CountEntityTypes :many
SELECT entity_type, count(*) AS count FROM entities WHERE deleted_at IS NULL GROUP BY entity_type ORDER BY count DESC;

-- name: CountRelationTypes :many
SELECT relation_type, count(*) AS count FROM entity_relations GROUP BY relation_type ORDER BY count DESC;

-- name: CountSoftDeleted :one
SELECT count(*) FROM entities WHERE deleted_at IS NOT NULL;
