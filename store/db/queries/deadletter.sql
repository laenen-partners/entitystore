-- name: InsertDeadLetter :exec
INSERT INTO entity_event_dead_letters (consumer_name, event_id, event_type, payload_type, entity_id, payload, error_message, retry_count)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: ListDeadLetters :many
SELECT * FROM entity_event_dead_letters
WHERE consumer_name = @consumer_name
  AND (cardinality(@event_types::text[]) = 0 OR event_type = ANY(@event_types))
ORDER BY created_at DESC
LIMIT @max_results;

-- name: CountDeadLetters :one
SELECT COUNT(*) FROM entity_event_dead_letters WHERE consumer_name = $1;

-- name: DeleteDeadLetter :exec
DELETE FROM entity_event_dead_letters WHERE id = $1;

-- name: DeleteDeadLettersByConsumer :exec
DELETE FROM entity_event_dead_letters WHERE consumer_name = $1;

-- name: GetDeadLetter :one
SELECT * FROM entity_event_dead_letters WHERE id = $1;

-- name: PurgeOldDeadLetters :exec
DELETE FROM entity_event_dead_letters WHERE created_at < $1;
