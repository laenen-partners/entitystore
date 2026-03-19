-- name: FindByAnchors :many
SELECT e.id, e.entity_type, e.data, e.confidence, e.tags, e.created_at, e.updated_at
FROM entity_anchors a
JOIN entities e ON e.id = a.entity_id
WHERE a.entity_type = $1 AND a.anchor_field = $2 AND a.normalized_value = $3
  AND (cardinality(@tags::text[]) = 0 OR e.tags @> @tags::text[])
  AND (cardinality(@any_tags::text[]) = 0 OR e.tags && @any_tags::text[]);

-- name: UpsertAnchor :exec
INSERT INTO entity_anchors (entity_id, entity_type, anchor_field, normalized_value)
VALUES ($1, $2, $3, $4)
ON CONFLICT (entity_type, anchor_field, normalized_value) DO UPDATE
SET entity_id = EXCLUDED.entity_id;

-- name: DeleteAnchorsForEntity :exec
DELETE FROM entity_anchors WHERE entity_id = $1;
