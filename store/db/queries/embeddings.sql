-- name: FindByEmbedding :many
SELECT e.id, e.entity_type, e.data, e.confidence, e.tags, e.created_at, e.updated_at
FROM entities e
WHERE e.entity_type = $1
  AND e.embedding IS NOT NULL
  AND (cardinality(@tags::text[]) = 0 OR e.tags @> @tags::text[])
ORDER BY e.embedding <=> @embedding::vector
LIMIT @top_k;

-- name: UpdateEntityEmbedding :exec
UPDATE entities
SET embedding = @embedding::vector, updated_at = now()
WHERE id = @entity_id;
