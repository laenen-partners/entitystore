-- name: FindByTokenOverlap :many
SELECT e.id, e.entity_type, e.data, e.confidence, e.tags, e.created_at, e.updated_at
FROM entity_tokens t
JOIN entities e ON e.id = t.entity_id
WHERE t.entity_type = $1 AND t.tokens && $2::text[]
  AND (cardinality(@tags::text[]) = 0 OR e.tags @> @tags::text[])
  AND (cardinality(@any_tags::text[]) = 0 OR e.tags && @any_tags::text[])
ORDER BY array_length(
    ARRAY(SELECT unnest(t.tokens) INTERSECT SELECT unnest($2::text[])),
    1
) DESC NULLS LAST
LIMIT $3;

-- name: UpsertTokens :exec
INSERT INTO entity_tokens (entity_id, entity_type, token_field, tokens)
VALUES ($1, $2, $3, $4)
ON CONFLICT (entity_id, token_field) DO UPDATE
SET tokens = EXCLUDED.tokens;

-- name: DeleteTokensForEntity :exec
DELETE FROM entity_tokens WHERE entity_id = $1;
