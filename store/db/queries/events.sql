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

-- name: GetEventsForEntity :many
SELECT id, event_type, payload_type, payload, entity_id, relation_key, tags, occurred_at, published_at
FROM entity_events
WHERE entity_id = @entity_id
  AND (cardinality(@event_types::text[]) = 0 OR event_type = ANY(@event_types))
  AND (@since::timestamptz = '0001-01-01' OR occurred_at > @since)
ORDER BY occurred_at DESC
LIMIT @max_results;
