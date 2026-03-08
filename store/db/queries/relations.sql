-- name: UpsertRelation :one
INSERT INTO entity_relations (source_id, target_id, relation_type, confidence, evidence, implied, document_id, data)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (source_id, target_id, relation_type) WHERE document_id IS NULL
DO UPDATE SET confidence = EXCLUDED.confidence, evidence = EXCLUDED.evidence, data = entity_relations.data || EXCLUDED.data
RETURNING id, source_id, target_id, relation_type, confidence, evidence, implied, document_id, data, created_at;

-- name: GetRelationsFromEntity :many
SELECT id, source_id, target_id, relation_type, confidence, evidence, implied, document_id, data, created_at
FROM entity_relations
WHERE source_id = $1
ORDER BY created_at DESC;

-- name: GetRelationsToEntity :many
SELECT id, source_id, target_id, relation_type, confidence, evidence, implied, document_id, data, created_at
FROM entity_relations
WHERE target_id = $1
ORDER BY created_at DESC;

-- name: GetRelationsByType :many
SELECT id, source_id, target_id, relation_type, confidence, evidence, implied, document_id, data, created_at
FROM entity_relations
WHERE relation_type = $1
ORDER BY created_at DESC;

-- name: GetRelationsForDocument :many
SELECT id, source_id, target_id, relation_type, confidence, evidence, implied, document_id, data, created_at
FROM entity_relations
WHERE document_id = $1
ORDER BY created_at DESC;

-- name: DeleteRelation :exec
DELETE FROM entity_relations WHERE id = $1;

-- name: DeleteRelationsForEntity :exec
DELETE FROM entity_relations WHERE source_id = $1 OR target_id = $1;

-- name: ConnectedEntities :many
SELECT DISTINCT e.id, e.entity_type, e.data, e.confidence, e.tags, e.created_at, e.updated_at
FROM entity_relations r
JOIN entities e ON e.id = CASE WHEN r.source_id = $1 THEN r.target_id ELSE r.source_id END
WHERE r.source_id = $1 OR r.target_id = $1;

-- name: FindConnectedByType :many
SELECT DISTINCT e.id, e.entity_type, e.data, e.confidence, e.tags, e.created_at, e.updated_at
FROM entity_relations r
JOIN entities e ON e.id = CASE WHEN r.source_id = @entity_id THEN r.target_id ELSE r.source_id END
WHERE (r.source_id = @entity_id OR r.target_id = @entity_id)
  AND e.entity_type = @entity_type
  AND (cardinality(@relation_types::text[]) = 0 OR r.relation_type = ANY(@relation_types::text[]))
  AND (cardinality(@tags::text[]) = 0 OR e.tags @> @tags::text[])
ORDER BY e.updated_at DESC;

-- name: FindEntitiesByRelation :many
SELECT DISTINCT e.id, e.entity_type, e.data, e.confidence, e.tags, e.created_at, e.updated_at
FROM entity_relations r
JOIN entities e ON e.id = r.source_id OR e.id = r.target_id
WHERE e.entity_type = @entity_type
  AND r.relation_type = @relation_type
  AND (cardinality(@tags::text[]) = 0 OR e.tags @> @tags::text[])
ORDER BY e.updated_at DESC;
