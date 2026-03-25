-- name: TryAcquireLock :execresult
INSERT INTO publisher_lock (holder_id, expires_at, renewed_at)
VALUES (@holder_id, now() + @ttl::interval, now())
ON CONFLICT (id) DO UPDATE
SET holder_id = EXCLUDED.holder_id,
    acquired_at = now(),
    expires_at = EXCLUDED.expires_at,
    renewed_at = now()
WHERE publisher_lock.expires_at < now();

-- name: RenewLock :execresult
UPDATE publisher_lock
SET expires_at = now() + @ttl::interval, renewed_at = now()
WHERE holder_id = @holder_id;

-- name: ReleaseLock :exec
DELETE FROM publisher_lock WHERE holder_id = @holder_id;

-- name: GetUnpublishedEvents :many
SELECT id, event_type, payload_type, payload, entity_id, relation_key, tags, occurred_at, published_at
FROM entity_events
WHERE published_at IS NULL
ORDER BY occurred_at
LIMIT @batch_size
FOR UPDATE SKIP LOCKED;

-- name: MarkEventsPublished :exec
UPDATE entity_events
SET published_at = now()
WHERE id = ANY(@ids::uuid[])
  AND occurred_at = ANY(@occurred_ats::timestamptz[]);
