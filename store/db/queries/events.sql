-- name: InsertEvent :exec
INSERT INTO entity_events (id, event_type, payload_type, payload, entity_id, relation_key, tags)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: GetLastEventTime :one
SELECT occurred_at FROM entity_events ORDER BY occurred_at DESC LIMIT 1;

-- name: CountUnpublishedEvents :one
SELECT count(*) FROM entity_events WHERE published_at IS NULL;

-- name: GetPublisherLock :one
SELECT holder_id, acquired_at, expires_at, renewed_at FROM publisher_lock WHERE id = 'singleton';

-- name: GetLastPublishedTime :one
SELECT published_at FROM entity_events WHERE published_at IS NOT NULL ORDER BY published_at DESC LIMIT 1;

-- name: GetEventByID :one
SELECT id, event_type, payload_type, payload, entity_id, relation_key, tags, occurred_at, published_at
FROM entity_events
WHERE id = $1;

-- name: GetAllEvents :many
SELECT ev.id, ev.event_type, ev.payload_type, ev.payload, ev.entity_id, ev.relation_key, ev.tags, ev.occurred_at, ev.published_at,
       COALESCE(e.display_name, '') AS entity_display_name
FROM entity_events ev
LEFT JOIN entities e ON e.id = ev.entity_id
WHERE (cardinality(@event_types::text[]) = 0 OR ev.event_type = ANY(@event_types))
  AND (sqlc.narg('cursor')::timestamptz IS NULL OR ev.occurred_at < sqlc.narg('cursor')::timestamptz)
ORDER BY ev.occurred_at DESC
LIMIT @max_results;

-- name: GetEventsForEntity :many
SELECT id, event_type, payload_type, payload, entity_id, relation_key, tags, occurred_at, published_at
FROM entity_events
WHERE entity_id = @entity_id
  AND (cardinality(@event_types::text[]) = 0 OR event_type = ANY(@event_types))
  AND (@since::timestamptz = '0001-01-01' OR occurred_at > @since)
ORDER BY occurred_at DESC
LIMIT @max_results;
