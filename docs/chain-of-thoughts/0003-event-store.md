# Spec: Event Store — Replace Provenance with Proto-First Events

**Status:** Proposed
**Date:** 2026-03-25

## Problem

The `entity_provenance` table is a narrow extraction log. It only records "entity data came from source X via model Y." There is no audit trail for:

- Entity updates, merges, deletes, or restores
- Relation creates, updates, or deletes
- Tag mutations
- Custom domain events (e.g. "person verified", "invoice approved")

Relations have only a `source_urn` field — no provenance table at all.

This limits analytics, compliance, debugging, and downstream integration. We need a unified, schema-checked event stream that covers the full entity lifecycle and supports caller-defined domain events.

## Design Principles

1. **Proto messages as events** — every event is a `proto.Message`. Schema evolution, type safety, and JSON marshaling come for free.
2. **Standard + custom events** — entitystore emits lifecycle events automatically; callers attach domain events via `WithEvents()`.
3. **Event type derived from proto name** — `entitystore.events.v1.EntityCreated` → event type `entitystore.events.EntityCreated` (version segment stripped for routing/subscription stability).
4. **Outbox-ready** — `published_at` column from day one, consumer built later.
5. **No backwards compatibility** — provenance table is replaced entirely.

## Standard Event Protos

Live in `proto/entitystore/events/v1/events.proto`:

```protobuf
syntax = "proto3";
package entitystore.events.v1;

option go_package = "github.com/laenen-partners/entitystore/gen/entitystore/events/v1;eventsv1";

// EntityCreated is emitted when a new entity is created via WriteActionCreate.
message EntityCreated {
  string entity_id = 1;
  string entity_type = 2;       // e.g. "persons.v1.Person"
  double confidence = 3;
  repeated string tags = 4;
  string actor = 5;             // source URN, user ID, or system identifier
  string model_id = 6;
  repeated string fields = 7;   // fields that were populated
}

// EntityUpdated is emitted when an existing entity is updated via WriteActionUpdate.
message EntityUpdated {
  string entity_id = 1;
  string entity_type = 2;
  double confidence = 3;
  repeated string tags = 4;
  string actor = 5;
  string model_id = 6;
  repeated string fields = 7;   // fields that changed
}

// EntityMerged is emitted when two entities are merged via WriteActionMerge.
message EntityMerged {
  string winner_id = 1;
  string loser_id = 2;
  string entity_type = 3;
  string match_method = 4;
  double match_confidence = 5;
  string actor = 6;
  string model_id = 7;
}

// EntityDeleted is emitted on soft delete (DeleteEntity).
message EntityDeleted {
  string entity_id = 1;
  string entity_type = 2;
  string actor = 3;
}

// EntityHardDeleted is emitted on permanent removal (HardDeleteEntity).
message EntityHardDeleted {
  string entity_id = 1;
  string entity_type = 2;
  string actor = 3;
}

// RelationCreated is emitted when a new relation is created via UpsertRelation
// (when the relation did not previously exist).
message RelationCreated {
  string source_id = 1;
  string target_id = 2;
  string relation_type = 3;
  double confidence = 4;
  string actor = 5;
}

// RelationUpdated is emitted when an existing relation is updated via UpsertRelation
// (when the relation already existed).
message RelationUpdated {
  string source_id = 1;
  string target_id = 2;
  string relation_type = 3;
  double confidence = 4;
  string actor = 5;
}

// RelationDeleted is emitted when a relation is removed via DeleteRelationByKey.
message RelationDeleted {
  string source_id = 1;
  string target_id = 2;
  string relation_type = 3;
  string actor = 4;
}
```

### Event Type Derivation

Given a proto message with full name `entitystore.events.v1.EntityCreated`:
- **`payload_type`** = `entitystore.events.v1.EntityCreated` (full name, for deserialization)
- **`event_type`** = `entitystore.events.EntityCreated` (version stripped, for routing/subscriptions)

Derivation logic:

```go
func eventType(msg proto.Message) string {
    full := string(proto.MessageName(msg))
    // "entitystore.events.v1.EntityCreated" → ["entitystore", "events", "v1", "EntityCreated"]
    parts := strings.Split(full, ".")
    // Remove the version segment (second-to-last): "entitystore.events.EntityCreated"
    stripped := append(parts[:len(parts)-2], parts[len(parts)-1])
    return strings.Join(stripped, ".")
}
```

This means a caller-defined `acme.hiring.v1.CandidateApproved` becomes event type `acme.hiring.CandidateApproved`. Version bumps (`v1` → `v2`) don't break downstream consumers subscribed to the event type.

## IDs: UUIDv7

Event IDs use **UUIDv7** (`uuid.NewV7()` from `github.com/google/uuid`) instead of UUIDv4:

- **Time-sortable** — the first 48 bits encode a millisecond Unix timestamp, so `ORDER BY id` = `ORDER BY occurred_at` (within millisecond precision)
- **No new dependency** — already using `github.com/google/uuid` throughout the codebase
- **Native UUID type** — works with PostgreSQL `UUID` columns as-is, no encoding changes
- **Index-friendly** — sequential inserts avoid B-tree page splits that random UUIDv4 causes on append-heavy tables

Event IDs are generated in Go (`uuid.NewV7()`), not by the database. The `occurred_at` column remains for explicit timestamp queries and partitioning, but `id` alone is sufficient for ordering.

## Database Schema

New migration replaces `entity_provenance`:

```sql
-- Drop provenance table (no backwards compatibility needed)
DROP TABLE IF EXISTS entity_provenance;
DROP INDEX IF EXISTS idx_provenance_entity;
DROP INDEX IF EXISTS idx_provenance_document;

-- Event store (IDs generated as UUIDv7 in Go, not by the database)
CREATE TABLE entity_events (
    id              UUID PRIMARY KEY,       -- UUIDv7: time-sortable, generated in Go
    event_type      TEXT NOT NULL,           -- "entitystore.events.EntityCreated" (routing key)
    payload_type    TEXT NOT NULL,           -- "entitystore.events.v1.EntityCreated" (deserialization)
    payload         JSONB NOT NULL,          -- protojson-encoded proto message
    entity_id       UUID,                    -- set for entity events, nullable for system events
    relation_key    TEXT,                    -- "source_id:target_id:relation_type" for relation events
    tags            TEXT[],                  -- tag snapshot at event time (enables scoped queries)
    occurred_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at    TIMESTAMPTZ             -- outbox: NULL = not yet published
) PARTITION BY RANGE (occurred_at);

-- Default partition (covers all time until explicit monthly partitions are created)
CREATE TABLE entity_events_default PARTITION OF entity_events DEFAULT;

-- Indexes
CREATE INDEX idx_events_entity ON entity_events (entity_id, occurred_at DESC) WHERE entity_id IS NOT NULL;
CREATE INDEX idx_events_type ON entity_events (event_type, occurred_at DESC);
CREATE INDEX idx_events_relation ON entity_events (relation_key, occurred_at DESC) WHERE relation_key IS NOT NULL;
CREATE INDEX idx_events_unpublished ON entity_events (occurred_at) WHERE published_at IS NULL;
CREATE INDEX idx_events_tags ON entity_events USING GIN (tags);
```

### Why partitioned?

Events are append-only and grow fast. Partitioning by `occurred_at` allows:
- Dropping old partitions without vacuum pressure
- Efficient range scans on `occurred_at`
- Future retention policies per partition

The default partition catches everything until explicit monthly partitions are created — no data loss if partition management isn't set up immediately.

## API Changes

### WriteEntityOp

```go
type WriteEntityOp struct {
    Action          WriteAction
    ID              string
    Data            proto.Message
    Confidence      float64
    Tags            []string
    MatchedEntityID string
    Anchors         []matching.AnchorQuery
    Tokens          map[string][]string
    Embedding       []float32
    Events          []proto.Message     // replaces Provenance
}
```

### UpsertRelationOp

```go
type UpsertRelationOp struct {
    SourceID     string
    TargetID     string
    RelationType string
    Confidence   float64
    Evidence     string
    Implied      bool
    SourceURN    string
    Data         proto.Message
    Events       []proto.Message     // NEW
}
```

### WriteOpOption

```go
// WithEvents appends caller-defined events to the operation.
func WithEvents(events ...proto.Message) WriteOpOption {
    return func(op *WriteEntityOp) { op.Events = append(op.Events, events...) }
}
```

`WithProvenance` and `Provenance()` are removed entirely.

### Event Emission

Standard events are emitted **automatically** by the write path — callers don't need to attach them:

| Operation | Standard event emitted |
|-----------|----------------------|
| `WriteActionCreate` | `EntityCreated` |
| `WriteActionUpdate` | `EntityUpdated` |
| `WriteActionMerge` | `EntityMerged` |
| `DeleteEntity` | `EntityDeleted` |
| `HardDeleteEntity` | `EntityHardDeleted` |
| `UpsertRelation` (new) | `RelationCreated` |
| `UpsertRelation` (existing) | `RelationUpdated` |
| `DeleteRelationByKey` | `RelationDeleted` |

Caller-defined events from `WithEvents()` are inserted in the same transaction, immediately after the standard event.

### Event Insertion (Write Path)

```go
func insertEvents(ctx context.Context, q *dbgen.Queries, entityID uuid.UUID, tags []string, events []proto.Message) error {
    for _, evt := range events {
        payload, err := protojson.Marshal(evt)
        if err != nil {
            return fmt.Errorf("marshal event: %w", err)
        }
        id, err := uuid.NewV7()
        if err != nil {
            return fmt.Errorf("generate event id: %w", err)
        }
        fullName := string(proto.MessageName(evt))
        if _, err := q.InsertEvent(ctx, dbgen.InsertEventParams{
            ID:          id,           // UUIDv7: time-sortable
            EventType:   deriveEventType(fullName),
            PayloadType: fullName,
            Payload:     payload,
            EntityID:    entityID,     // wired from write result
            Tags:        tags,         // snapshot at write time
        }); err != nil {
            return fmt.Errorf("insert event: %w", err)
        }
    }
    return nil
}
```

### Reading Events

```go
// GetEventsForEntity returns events for the given entity, newest first.
// Optional filter by event type prefix (e.g. "entitystore.events." for lifecycle only).
func (es *EntityStore) GetEventsForEntity(ctx context.Context, entityID string, opts *EventQueryOpts) ([]Event, error)

// EventQueryOpts filters event queries.
type EventQueryOpts struct {
    EventTypes []string    // filter by exact event types
    Since      time.Time   // only events after this time
    Limit      int         // max results (default 100)
}

// Event is a stored event with its metadata.
type Event struct {
    ID          string
    EventType   string
    PayloadType string
    Payload     proto.Message   // deserialized proto message
    EntityID    string
    RelationKey string
    Tags        []string
    OccurredAt  time.Time
    PublishedAt *time.Time
}
```

For the `Payload` field to return a deserialized `proto.Message`, the caller's proto types must be registered in the global proto registry (which happens automatically for any imported generated proto package). If the type isn't registered, we return the raw `JSONB` as a `structpb.Struct` fallback.

### EntityStorer Interface

```go
type EntityStorer interface {
    // ... existing methods ...

    // Events
    GetEventsForEntity(ctx context.Context, entityID string, opts *EventQueryOpts) ([]Event, error)
}
```

### ScopedStore

`GetEventsForEntity` in ScopedStore:
1. Verify entity visibility (same as `GetEntity` — check tags)
2. Delegate to inner store
3. Optionally filter returned events by scope tags (events carry tag snapshots)

### TxStore

Events emitted within a transaction are visible within that transaction (same tx isolation). Standard events for `WriteEntity` and `UpsertRelation` are inserted in the same tx.

```go
type TxStore struct {
    // ... existing fields ...
}

// GetEventsForEntity reads events within the transaction.
func (tx *TxStore) GetEventsForEntity(ctx context.Context, entityID string, opts *EventQueryOpts) ([]Event, error)
```

## Usage Examples

### Basic: Create with automatic lifecycle event

```go
// EntityCreated is emitted automatically — no caller action needed.
results, _ := es.BatchWrite(ctx, []entitystore.BatchWriteOp{
    {WriteEntity: &entitystore.WriteEntityOp{
        Action: entitystore.WriteActionCreate,
        Data:   &personsv1.Person{Email: "alice@example.com"},
    }},
})
// entity_events now contains one EntityCreated row
```

### Domain events via WithEvents

```go
results, _ := es.BatchWrite(ctx, []entitystore.BatchWriteOp{
    {WriteEntity: &entitystore.WriteEntityOp{
        Action: entitystore.WriteActionCreate,
        Data:   &personsv1.Person{Email: "alice@example.com"},
        Events: []proto.Message{
            &hiringv1.CandidateSourced{
                Source:    "linkedin",
                Recruiter: "bob@acme.com",
            },
        },
    }},
})
// entity_events now contains:
//   1. entitystore.events.EntityCreated  (automatic)
//   2. hiring.CandidateSourced           (caller-defined)
```

### Domain events via generated WriteOp

```go
op := jobsv1.JobPostingWriteOp(posting, entitystore.WriteActionCreate,
    entitystore.WithTags("ws:acme"),
    entitystore.WithEvents(
        &pipelinev1.ExtractionCompleted{
            Source:  "email:inbox/msg-42",
            ModelId: "gpt-4o",
            Fields:  []string{"reference", "title"},
        },
    ),
)
```

### Querying events

```go
events, _ := es.GetEventsForEntity(ctx, entityID, &entitystore.EventQueryOpts{
    EventTypes: []string{"entitystore.events.EntityCreated", "entitystore.events.EntityUpdated"},
    Since:      time.Now().Add(-24 * time.Hour),
    Limit:      50,
})

for _, e := range events {
    switch evt := e.Payload.(type) {
    case *eventsv1.EntityCreated:
        fmt.Printf("Created by %s via %s\n", evt.Actor, evt.ModelId)
    case *eventsv1.EntityUpdated:
        fmt.Printf("Updated fields: %v\n", evt.Fields)
    }
}
```

## Migration Path

### Phase 1: Schema + Standard Events
1. Define `proto/entitystore/events/v1/events.proto` with standard lifecycle events
2. Run `buf generate` to produce Go types
3. Add `entity_events` migration (drops `entity_provenance`)
4. Add `Events []proto.Message` field to `WriteEntityOp` and `UpsertRelationOp`
5. Add `WithEvents()` option function, remove `WithProvenance()` and `Provenance()`
6. Update `BatchWrite` write path to emit standard events automatically + caller events
7. Update `DeleteEntity`, `HardDeleteEntity`, `DeleteRelationByKey` to emit standard events
8. Add `GetEventsForEntity` to `Store`, `EntityStore`, `ScopedStore`, `TxStore`, `EntityStorer`
9. Remove all provenance code: `ProvenanceEntry`, `GetProvenanceForEntity`, `insertProvenance`, SQLC queries

### Phase 2: Outbox Consumer (Later)
10. Build a poller that reads `published_at IS NULL` rows, delivers to downstream, marks published
11. Only when there's a real consumer (webhooks, analytics pipeline, CDC)

## What Changes for Downstream Consumers

| Before | After |
|--------|-------|
| `entitystore.ProvenanceEntry` | Removed — use events |
| `entitystore.WithProvenance(p)` | `entitystore.WithEvents(msgs...)` |
| `store.Provenance(urn, model)` | Construct domain-specific event proto |
| `es.GetProvenanceForEntity(ctx, id)` | `es.GetEventsForEntity(ctx, id, opts)` |
| No relation audit trail | `RelationCreated`/`Updated`/`Deleted` automatic |
| No delete audit trail | `EntityDeleted`/`EntityHardDeleted` automatic |

## Non-Goals

- **Outbox consumer** — `published_at` column is ready, but no consumer built in this phase
- **Event sourcing** — events are supplementary, not the source of truth. Entity state lives in the entities table.
- **Event subscriptions/streaming** — no in-process pub/sub. Events are queryable via `GetEventsForEntity`.
- **Partition management** — default partition handles everything. Monthly partitions are an ops concern.
- **Replay/projection** — not in scope. Events are for audit, analytics, and downstream delivery.
