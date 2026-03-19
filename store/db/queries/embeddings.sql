-- name: FindByEmbedding :many
SELECT e.id, e.entity_type, e.data, e.confidence, e.tags, e.created_at, e.updated_at
FROM entities e
WHERE (cardinality(@entity_types::text[]) = 0 OR e.entity_type = ANY(@entity_types))
  AND e.embedding IS NOT NULL
  AND (cardinality(@tags::text[]) = 0 OR e.tags @> @tags::text[])
  AND (cardinality(@any_tags::text[]) = 0 OR e.tags && @any_tags::text[])
ORDER BY e.embedding <=> @embedding::vector
LIMIT @top_k;

-- name: UpdateEntityEmbedding :exec
UPDATE entities
SET embedding = @embedding::vector, updated_at = now()
WHERE id = @entity_id;
