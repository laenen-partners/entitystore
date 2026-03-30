# Spec: EntityStore — LISTEN/NOTIFY for Realtime + Named Consumers for Projections

## Context

Two consumers need to process entity events independently:

1. **Notifier** (realtime, fire-and-forget) — publishes change notifications to pubsub for SSE stream watches. Needs sub-second latency. Currently polls every 5s.
2. **Projector** (durable, heavyweight) — runs projection handlers (embeddings, context building). Polling at 5s is fine, but must track its own processing state independently from the notifier.

Both need the entitystore to support:
- **LISTEN/NOTIFY** for instant event delivery (notifier)
- **Named consumers** so multiple independent consumers can each track their own progress through the event stream

## Change 1: LISTEN/NOTIFY on Event Insert

### Migration

Add a trigger on `entity_events` that sends a Postgres notification on each insert:

```sql
-- migrate:up
CREATE OR REPLACE FUNCTION notify_entity_event() RETURNS TRIGGER AS $$
BEGIN
    PERFORM pg_notify('entity_events', NEW.id::text);
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER entity_events_notify
    AFTER INSERT ON entity_events
    FOR EACH ROW EXECUTE FUNCTION notify_entity_event();

-- migrate:down
DROP TRIGGER IF EXISTS entity_events_notify ON entity_events;
DROP FUNCTION IF EXISTS notify_entity_event();
```

### Go API

Add a `Listen` method that uses `pgxpool.Conn.WaitForNotification`:

```go
// Listen blocks until a new entity event is inserted, then returns.
// Use in a loop alongside polling as a fallback:
//
//   for {
//       select {
//       case <-ctx.Done():
//           return ctx.Err()
//       default:
//       }
//       es.Listen(ctx, 5*time.Second) // wakes on NOTIFY or timeout
//       processEvents()
//   }
func (es *EntityStore) Listen(ctx context.Context, timeout time.Duration) error
```

This replaces the `time.Ticker` in the publisher loop. The publisher wakes instantly on insert, with a timeout fallback (e.g. 5s) for missed notifications (Postgres NOTIFY is best-effort — notifications can be lost if the connection drops).

### Publisher Change

Replace `time.NewTicker(p.cfg.PollInterval)` with a hybrid loop:

```go
for {
    select {
    case <-ctx.Done():
        return ctx.Err()
    default:
    }
    // Blocks until NOTIFY or timeout — no busy polling.
    _ = p.listen(ctx, p.cfg.PollInterval)
    p.poll(ctx)
}
```

## Change 2: Named Consumers (for Projector)

### Problem

The current publisher uses `published_at IS NULL` and a singleton lock. A second consumer (projector) can't use the same marker — they'd conflict.

### Approach: Consumer Cursors Table

Each consumer tracks its own high-water mark through the event stream:

```sql
-- migrate:up
CREATE TABLE entity_event_consumers (
    name         TEXT PRIMARY KEY,
    last_event_at TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01',
    last_event_id UUID,
    holder_id    TEXT,
    acquired_at  TIMESTAMPTZ,
    expires_at   TIMESTAMPTZ,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- migrate:down
DROP TABLE IF EXISTS entity_event_consumers;
```

Each consumer (e.g. `"notifier"`, `"projector"`) has its own row. The lock fields (`holder_id`, `acquired_at`, `expires_at`) are inline — no separate lock table needed.

### Go API

```go
// Consumer reads entity events from a named cursor position.
type Consumer struct { ... }

type ConsumerConfig struct {
    Name         string        // e.g. "notifier", "projector"
    BatchSize    int           // default 100
    PollInterval time.Duration // default 5s
    LockTTL      time.Duration // default 30s
    Logger       *slog.Logger
}

// NewConsumer creates a named event consumer.
func (es *EntityStore) NewConsumer(fn ConsumerFunc, cfg ConsumerConfig) *Consumer

// ConsumerFunc receives a batch of events. Return nil to advance the cursor.
type ConsumerFunc func(ctx context.Context, events []Event) error

// Run starts the consumer loop with polling. Blocks until ctx is cancelled.
func (c *Consumer) Run(ctx context.Context) error

// RunWithNotify starts the consumer loop with LISTEN/NOTIFY + polling fallback.
// Use for low-latency consumers (notifier).
func (c *Consumer) RunWithNotify(ctx context.Context) error
```

### Query: Read Events After Cursor

```sql
-- name: GetEventsAfterCursor :many
SELECT id, event_type, payload_type, payload, entity_id, relation_key, tags, occurred_at, published_at
FROM entity_events
WHERE occurred_at > @after_at
   OR (occurred_at = @after_at AND id > @after_id)
ORDER BY occurred_at, id
LIMIT @batch_size;
```

### Query: Advance Cursor

```sql
-- name: AdvanceConsumerCursor :exec
UPDATE entity_event_consumers
SET last_event_at = @last_event_at,
    last_event_id = @last_event_id,
    updated_at = now()
WHERE name = @name AND holder_id = @holder_id;
```

### Lock Queries (per consumer)

```sql
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
```

## Migration Path

### Phase 1: Named Consumers (enables projector)
- Add `entity_event_consumers` table
- Add `NewConsumer` / `ConsumerFunc` API
- Both notifier and projector use `NewConsumer` with different names
- Existing `NewPublisher` / `published_at` can be deprecated

### Phase 2: LISTEN/NOTIFY (enables realtime)
- Add trigger on `entity_events`
- Add `RunWithNotify` to Consumer
- Notifier uses `RunWithNotify` for sub-second latency
- Projector keeps `Run` (polling at 5s is fine)

### Phase 3: Deprecate Old Publisher
- Remove `publisher_lock` table
- Remove `published_at` column (or keep for backwards compat)
- Remove `NewPublisher` (replaced by `NewConsumer`)

## Usage in Steward

```go
// Notifier — realtime, fire-and-forget, for SSE stream watches.
notifier := es.NewConsumer(notifyFunc(ps), entitystore.ConsumerConfig{
    Name: "notifier",
    PollInterval: 5 * time.Second, // fallback only
})
g.Go(func() error { return notifier.RunWithNotify(ctx) })

// Projector — durable, heavyweight, for embeddings/projections.
projector := es.NewConsumer(projectFunc(handlers), entitystore.ConsumerConfig{
    Name: "projector",
    PollInterval: 5 * time.Second,
})
g.Go(func() error { return projector.Run(ctx) })
```
