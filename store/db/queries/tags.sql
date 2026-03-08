-- name: SetEntityTags :exec
UPDATE entities
SET tags = @tags::text[], updated_at = now()
WHERE id = @entity_id;

-- name: AddEntityTags :exec
UPDATE entities
SET tags = (
    SELECT ARRAY(SELECT DISTINCT unnest(tags || @tags::text[]))
), updated_at = now()
WHERE id = @entity_id;

-- name: RemoveEntityTag :exec
UPDATE entities
SET tags = array_remove(tags, @tag), updated_at = now()
WHERE id = @entity_id;
