-- name: UpsertRelation :one
INSERT INTO entity_relations (source_id, target_id, relation_type, confidence, evidence, implied, source_urn, data_type, data)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (source_id, target_id, relation_type) WHERE source_urn IS NULL
DO UPDATE SET confidence = EXCLUDED.confidence, evidence = EXCLUDED.evidence, data_type = EXCLUDED.data_type, data = EXCLUDED.data
RETURNING id, source_id, target_id, relation_type, confidence, evidence, implied, source_urn, data_type, data, created_at;

-- name: GetRelationsFromEntity :many
SELECT id, source_id, target_id, relation_type, confidence, evidence, implied, source_urn, data_type, data, created_at
FROM entity_relations
WHERE source_id = $1
ORDER BY created_at DESC;

-- name: GetRelationsToEntity :many
SELECT id, source_id, target_id, relation_type, confidence, evidence, implied, source_urn, data_type, data, created_at
FROM entity_relations
WHERE target_id = $1
ORDER BY created_at DESC;

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

-- name: DeleteRelationsForEntity :exec
DELETE FROM entity_relations WHERE source_id = $1 OR target_id = $1;

-- name: ConnectedEntitiesOutbound :many
SELECT e.id, e.entity_type, e.data, e.confidence, e.tags, e.created_at, e.updated_at
FROM entity_relations r
JOIN entities e ON e.id = r.target_id
WHERE r.source_id = $1;

-- name: ConnectedEntitiesInbound :many
SELECT e.id, e.entity_type, e.data, e.confidence, e.tags, e.created_at, e.updated_at
FROM entity_relations r
JOIN entities e ON e.id = r.source_id
WHERE r.target_id = $1;

-- name: FindConnectedByTypeOutbound :many
SELECT e.id, e.entity_type, e.data, e.confidence, e.tags, e.created_at, e.updated_at
FROM entity_relations r
JOIN entities e ON e.id = r.target_id
WHERE r.source_id = @entity_id
  AND e.entity_type = @entity_type
  AND (cardinality(@relation_types::text[]) = 0 OR r.relation_type = ANY(@relation_types::text[]))
  AND (cardinality(@tags::text[]) = 0 OR e.tags @> @tags::text[]);

-- name: FindConnectedByTypeInbound :many
SELECT e.id, e.entity_type, e.data, e.confidence, e.tags, e.created_at, e.updated_at
FROM entity_relations r
JOIN entities e ON e.id = r.source_id
WHERE r.target_id = @entity_id
  AND e.entity_type = @entity_type
  AND (cardinality(@relation_types::text[]) = 0 OR r.relation_type = ANY(@relation_types::text[]))
  AND (cardinality(@tags::text[]) = 0 OR e.tags @> @tags::text[]);

-- name: FindEntitiesByRelationSource :many
SELECT e.id, e.entity_type, e.data, e.confidence, e.tags, e.created_at, e.updated_at
FROM entity_relations r
JOIN entities e ON e.id = r.source_id
WHERE e.entity_type = @entity_type
  AND r.relation_type = @relation_type
  AND (cardinality(@tags::text[]) = 0 OR e.tags @> @tags::text[]);

-- name: FindEntitiesByRelationTarget :many
SELECT e.id, e.entity_type, e.data, e.confidence, e.tags, e.created_at, e.updated_at
FROM entity_relations r
JOIN entities e ON e.id = r.target_id
WHERE e.entity_type = @entity_type
  AND r.relation_type = @relation_type
  AND (cardinality(@tags::text[]) = 0 OR e.tags @> @tags::text[]);
