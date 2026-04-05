# Spec 0030: Publisher Entity Type Resolution

## Problem

The outbox publisher bridges entity events from the `entity_events` table to pubsub for SSE streaming. The pubsub topic includes the entity domain (e.g. `"message"`, `"project"`, `"task"`) so that `stream.Watch` in the UI can subscribe to specific entity types.

Currently the publisher extracts the domain by:
1. Looking for a `type:` tag on the event → **often absent** in newer entitystore versions
2. Parsing `entity_type` from the JSON payload via `domainFromPayload` → **fails** because the payload structure varies
3. Parsing the event type string `"chat.MessageSent"` → **fragile hack**, breaks when event naming conventions change

This is unreliable. The root cause: the `entity_events` table has no `entity_type` column — it's buried inside the JSON payload.

## Root Cause

The `entity_events` table schema:

```sql
CREATE TABLE entity_events (
    id           UUID NOT NULL,
    event_type   TEXT NOT NULL,       -- "entitystore.events.EntityCreated" or "chat.MessageSent"
    payload_type TEXT NOT NULL,       -- full proto message name
    payload      JSONB NOT NULL,      -- serialized proto as JSON
    entity_id    UUID,                -- nullable reference to entities.id
    relation_key TEXT,
    tags         TEXT[],
    occurred_at  TIMESTAMPTZ NOT NULL,
    published_at TIMESTAMPTZ
);
```

For `EntityCreated` events, `entity_type` is inside `payload.entity_type` (a field on the `EntityCreated` proto). But for custom domain events like `chat.MessageSent`, the payload has different fields (`conversation_id`, `sender_id`) — no `entity_type`.

## Proposed Fix: Add `entity_type` column to `entity_events`

### Migration

```sql
-- migrate:up
ALTER TABLE entity_events ADD COLUMN entity_type TEXT;

-- Backfill from existing entities where possible.
UPDATE entity_events SET entity_type = e.entity_type
FROM entities e WHERE entity_events.entity_id = e.id
AND entity_events.entity_type IS NULL;

-- migrate:down
ALTER TABLE entity_events DROP COLUMN entity_type;
```

### Write path

When `BatchWrite` inserts an event, set `entity_type` from the entity being written. The entity type is already known at write time — it's derived from `proto.MessageName(op.Data)`.

For custom domain events (via `store.WithEvents()`), the entity type should be carried forward from the parent write operation.

### Consumer path

The `Consumer` query becomes:

```sql
SELECT id, event_type, payload_type, payload, entity_id, entity_type, ...
FROM entity_events WHERE ...
```

The `Event` struct gets a new field:

```go
type Event struct {
    // ... existing fields ...
    EntityType string // e.g. "chat.v1.Message", "project.v1.Project"
}
```

### Publisher

The publisher simply reads `ev.EntityType` — no parsing, no guessing:

```go
domain := domainFromEntityType(ev.EntityType) // "chat.v1.Message" → "message"
```

Using `schema.DomainFromEntityType` which already exists in the domains SDK and handles the `"package.version.Entity"` → `"entity"` conversion correctly.

## Interim Fix (until entitystore is updated)

Until the entitystore migration lands, the publisher should use this fallback chain:

1. `ev.EntityType` (new field, when available)
2. Join entity_type from the `entities` table via `ev.EntityID`
3. Parse `entity_type` from the `EntityCreated` payload (legacy events)

The current string-parsing hack (`domainFromEventType`) should be removed once option 1 is available.

## Impact

- **entitystore**: New migration + schema change + write path update
- **steward publisher**: Simplified to one-line domain extraction
- **All consumers**: Get `EntityType` for free on every event — useful for projections too
