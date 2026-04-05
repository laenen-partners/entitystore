-- name: GetEventsAfterCursor :many
SELECT id, event_type, payload_type, payload, entity_id, relation_key, tags, entity_type, occurred_at, published_at
FROM entity_events
WHERE occurred_at > @after_at
   OR (occurred_at = @after_at AND id > @after_id)
ORDER BY occurred_at, id
LIMIT @batch_size;

-- name: GetConsumerCursor :one
SELECT last_event_at, last_event_id, holder_id, acquired_at, expires_at, updated_at,
       consecutive_failures, last_error, backoff_until
FROM entity_event_consumers
WHERE name = @name;

-- name: AdvanceConsumerCursor :exec
UPDATE entity_event_consumers
SET last_event_at = @last_event_at,
    last_event_id = @last_event_id,
    consecutive_failures = 0,
    last_error = NULL,
    backoff_until = NULL,
    updated_at = now()
WHERE name = @name AND holder_id = @holder_id;

-- name: UpdateConsumerFailureState :exec
UPDATE entity_event_consumers
SET consecutive_failures = @consecutive_failures,
    last_error = @last_error,
    backoff_until = sqlc.narg('backoff_until')::timestamptz,
    updated_at = now()
WHERE name = @name AND holder_id = @holder_id;

-- name: TryAcquireConsumerLock :execresult
INSERT INTO entity_event_consumers (name, holder_id, acquired_at, expires_at)
VALUES (@name, @holder_id, now(), now() + @ttl::interval)
ON CONFLICT (name) DO UPDATE
SET holder_id = EXCLUDED.holder_id,
    acquired_at = now(),
    expires_at = EXCLUDED.expires_at
WHERE entity_event_consumers.expires_at IS NULL
   OR entity_event_consumers.expires_at < now();

-- name: RenewConsumerLock :execresult
UPDATE entity_event_consumers
SET expires_at = now() + @ttl::interval
WHERE name = @name AND holder_id = @holder_id;

-- name: ReleaseConsumerLock :exec
UPDATE entity_event_consumers
SET holder_id = NULL, expires_at = NULL
WHERE name = @name AND holder_id = @holder_id;

-- name: ListConsumers :many
SELECT name, last_event_at, last_event_id, holder_id, acquired_at, expires_at, updated_at,
       consecutive_failures, last_error, backoff_until
FROM entity_event_consumers
ORDER BY name;
