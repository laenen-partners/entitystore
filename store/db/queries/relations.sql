-- name: UpsertRelation :one
INSERT INTO entity_relations (source_id, target_id, relation_type, confidence, evidence, implied, source_urn, data_type, data)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (source_id, target_id, relation_type) WHERE source_urn IS NULL
DO UPDATE SET confidence = EXCLUDED.confidence, evidence = EXCLUDED.evidence, data_type = EXCLUDED.data_type, data = EXCLUDED.data
RETURNING id, source_id, target_id, relation_type, confidence, evidence, implied, source_urn, data_type, data, created_at;

-- name: GetRelationsFromEntity :many
SELECT id, source_id, target_id, relation_type, confidence, evidence, implied, source_urn, data_type, data, created_at
FROM entity_relations
WHERE source_id = @source_id
  AND (sqlc.narg('cursor')::timestamptz IS NULL OR created_at < sqlc.narg('cursor')::timestamptz)
ORDER BY created_at DESC
LIMIT @page_size;

-- name: GetRelationsToEntity :many
SELECT id, source_id, target_id, relation_type, confidence, evidence, implied, source_urn, data_type, data, created_at
FROM entity_relations
WHERE target_id = @target_id
  AND (sqlc.narg('cursor')::timestamptz IS NULL OR created_at < sqlc.narg('cursor')::timestamptz)
ORDER BY created_at DESC
LIMIT @page_size;

-- name: GetRelationsByType :many
SELECT id, source_id, target_id, relation_type, confidence, evidence, implied, source_urn, data_type, data, created_at
FROM entity_relations
WHERE relation_type = $1
ORDER BY created_at DESC;

-- name: GetRelationsForSource :many
SELECT id, source_id, target_id, relation_type, confidence, evidence, implied, source_urn, data_type, data, created_at
FROM entity_relations
WHERE source_urn = $1
ORDER BY created_at DESC;

-- name: DeleteRelation :exec
DELETE FROM entity_relations WHERE id = $1;

-- name: DeleteRelationByKey :exec
DELETE FROM entity_relations
WHERE source_id = @source_id
  AND target_id = @target_id
  AND relation_type = @relation_type;

-- name: UpdateRelationData :one
UPDATE entity_relations
SET data_type = @data_type, data = @data
WHERE source_id = @source_id
  AND target_id = @target_id
  AND relation_type = @relation_type
RETURNING id, source_id, target_id, relation_type, confidence, evidence, implied, source_urn, data_type, data, created_at;

-- name: DeleteRelationsForEntity :exec
DELETE FROM entity_relations WHERE source_id = $1 OR target_id = $1;

-- name: ConnectedEntities :many
SELECT DISTINCT e.id, e.entity_type, e.data, e.confidence, e.tags, e.created_at, e.updated_at
FROM (
    SELECT r.target_id AS connected_id FROM entity_relations r WHERE r.source_id = @entity_id
    UNION
    SELECT r.source_id AS connected_id FROM entity_relations r WHERE r.target_id = @entity_id
) AS conns
JOIN entities e ON e.id = conns.connected_id
WHERE e.deleted_at IS NULL
LIMIT @page_size;

-- name: FindConnectedByTypeOutbound :many
SELECT e.id, e.entity_type, e.data, e.confidence, e.tags, e.created_at, e.updated_at
FROM entity_relations r
JOIN entities e ON e.id = r.target_id
WHERE r.source_id = @entity_id
  AND e.deleted_at IS NULL
  AND (@entity_type::text = '' OR e.entity_type = @entity_type::text)
  AND (cardinality(@relation_types::text[]) = 0 OR r.relation_type = ANY(@relation_types::text[]))
  AND (cardinality(@tags::text[]) = 0 OR e.tags @> @tags::text[])
  AND (cardinality(@any_tags::text[]) = 0 OR e.tags && @any_tags::text[])
  AND (@exclude_tag = '' OR NOT (@exclude_tag = ANY(e.tags)) OR e.tags && @unless_tags::text[])
  AND (sqlc.narg('cursor')::timestamptz IS NULL OR r.created_at < sqlc.narg('cursor')::timestamptz)
ORDER BY r.created_at DESC
LIMIT @page_size;

-- name: FindConnectedByTypeInbound :many
SELECT e.id, e.entity_type, e.data, e.confidence, e.tags, e.created_at, e.updated_at
FROM entity_relations r
JOIN entities e ON e.id = r.source_id
WHERE r.target_id = @entity_id
  AND e.deleted_at IS NULL
  AND (@entity_type::text = '' OR e.entity_type = @entity_type::text)
  AND (cardinality(@relation_types::text[]) = 0 OR r.relation_type = ANY(@relation_types::text[]))
  AND (cardinality(@tags::text[]) = 0 OR e.tags @> @tags::text[])
  AND (cardinality(@any_tags::text[]) = 0 OR e.tags && @any_tags::text[])
  AND (@exclude_tag = '' OR NOT (@exclude_tag = ANY(e.tags)) OR e.tags && @unless_tags::text[])
  AND (sqlc.narg('cursor')::timestamptz IS NULL OR r.created_at < sqlc.narg('cursor')::timestamptz)
ORDER BY r.created_at DESC
LIMIT @page_size;

-- name: FindEntitiesByRelationSource :many
SELECT e.id, e.entity_type, e.data, e.confidence, e.tags, e.created_at, e.updated_at
FROM entity_relations r
JOIN entities e ON e.id = r.source_id
WHERE e.entity_type = @entity_type
  AND e.deleted_at IS NULL
  AND r.relation_type = @relation_type
  AND (cardinality(@tags::text[]) = 0 OR e.tags @> @tags::text[])
  AND (cardinality(@any_tags::text[]) = 0 OR e.tags && @any_tags::text[])
  AND (@exclude_tag::text = '' OR NOT (@exclude_tag::text = ANY(e.tags)) OR e.tags && @unless_tags::text[])
LIMIT @page_size;

-- name: FindEntitiesByRelationTarget :many
SELECT e.id, e.entity_type, e.data, e.confidence, e.tags, e.created_at, e.updated_at
FROM entity_relations r
JOIN entities e ON e.id = r.target_id
WHERE e.entity_type = @entity_type
  AND e.deleted_at IS NULL
  AND r.relation_type = @relation_type
  AND (cardinality(@tags::text[]) = 0 OR e.tags @> @tags::text[])
  AND (cardinality(@any_tags::text[]) = 0 OR e.tags && @any_tags::text[])
  AND (@exclude_tag::text = '' OR NOT (@exclude_tag::text = ANY(e.tags)) OR e.tags && @unless_tags::text[])
LIMIT @page_size;
