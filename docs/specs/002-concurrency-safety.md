# Spec: EntityStore Concurrency Safety

**Status:** Proposed
**Date:** 2026-04-03

## Problem

EntityStore has no protection against concurrent writes to the same entity. Multiple extraction pipelines or services can simultaneously update, merge, or delete the same entity, leading to silent data loss and inconsistent state.

## Risks

### 1. Lost updates (two writers, same entity)

```
Writer A: reads entity, confidence=0.9
Writer B: reads entity, confidence=0.9
Writer A: updates entity, sets confidence=0.95, commits
Writer B: updates entity, sets confidence=0.92, commits  ← overwrites A's change
```

`WriteActionUpdate` does a full JSONB replace — the last writer wins silently. For entity resolution this means valid extraction data is lost without any error or indication.

**Severity:** High. Most common concurrency issue.

### 2. Anchor races (two entities, same anchor)

```
Writer A: creates entity with email=alice@example.com
Writer B: creates entity with email=alice@example.com
```

The anchor UPSERT silently re-points the anchor to the second entity. The first entity becomes an orphan — has data but is unreachable by anchor lookup.

**Severity:** Medium. The matching engine should prevent this (FindByAnchors before create), but concurrent pipelines can race past the check.

### 3. Concurrent merge conflicts

```
Matcher A: decides to merge extracted data into entity X
Matcher B: decides to merge different data into entity X
Both call BatchWrite with WriteActionMerge
```

Both merges succeed. The JSONB gets `||` merged twice in unpredictable order. Fields from both extractions mix, potentially creating inconsistent state.

**Severity:** Medium. Merge is inherently a read-modify-write operation.

### 4. Soft delete + concurrent write

```
Writer A: soft-deletes entity (sets deleted_at)
Writer B: updates same entity (doesn't check deleted_at)
```

The update succeeds — the entity gets new data but remains soft-deleted. It's invisible to reads but mutated.

**Severity:** Low. Unlikely in practice but violates the soft-delete contract.

## Solution

### Change 1: Optimistic locking via version column

Add a `version` column to entities that increments on every write. Callers provide the expected version; the update fails if the version doesn't match (someone else wrote first).

#### Migration

```sql
-- migrate:up
ALTER TABLE entities ADD COLUMN version INT NOT NULL DEFAULT 0;
```

#### SQL changes

Update:
```sql
UPDATE entities
SET data = $2, confidence = $3, display_name = $4, version = version + 1, updated_at = now()
WHERE id = $1 AND version = @expected_version;
```

Merge:
```sql
UPDATE entities
SET data = data || $2, confidence = $3, display_name = $4, version = version + 1, updated_at = now()
WHERE id = $1 AND version = @expected_version;
```

If rows affected = 0, return `ErrConflict`.

Create:
```sql
INSERT INTO entities (..., version) VALUES (..., 0)
```

Creates always start at version 0. No conflict possible (new UUID).

#### Go API

Add `Version` field to `StoredEntity`:

```go
type StoredEntity struct {
    // ... existing fields ...
    Version int `json:"version"`
}
```

Add `Version` field to `WriteEntityOp`:

```go
type WriteEntityOp struct {
    // ... existing fields ...
    Version int // Required for update and merge. Ignored for create.
}
```

New sentinel error:

```go
var ErrConflict = fmt.Errorf("entitystore: version conflict (entity was modified by another writer)")
```

#### Caller pattern

```go
// Read
entity, _ := es.GetEntity(ctx, id)

// Modify
op := &entitystore.WriteEntityOp{
    Action:          entitystore.WriteActionUpdate,
    MatchedEntityID: entity.ID,
    Version:         entity.Version, // pass current version
    Data:            updatedData,
}

// Write — fails with ErrConflict if someone else updated
_, err := es.BatchWrite(ctx, []entitystore.BatchWriteOp{{WriteEntity: op}})
if errors.Is(err, entitystore.ErrConflict) {
    // Re-read entity and retry
}
```

#### Generated WriteOp

The generated `{Entity}WriteOp` sets `Version` automatically when used with `WithMatchedEntityID`:

```go
// The caller provides the version from the entity they read:
op := personv1.PersonWriteOp(person, store.WriteActionUpdate,
    store.WithMatchedEntityID(entity.ID),
    store.WithVersion(entity.Version),
)
```

### Change 2: Soft delete guard on updates

Add `AND deleted_at IS NULL` to the UPDATE and MERGE queries:

```sql
UPDATE entities
SET data = $2, confidence = $3, display_name = $4, version = version + 1, updated_at = now()
WHERE id = $1 AND version = @expected_version AND deleted_at IS NULL;
```

If the entity is soft-deleted, the update silently fails (rows affected = 0). Combined with version check, this returns `ErrConflict`, which callers already handle.

### Change 3: Document anchor race prevention

The anchor race is already preventable via preconditions:

```go
es.BatchWrite(ctx, []entitystore.BatchWriteOp{
    {
        WriteEntity: &entitystore.WriteEntityOp{
            Action: entitystore.WriteActionCreate,
            Data:   person,
        },
        PreConditions: []entitystore.PreCondition{
            {EntityType: "persons.v1.Person", Anchors: anchors, MustNotExist: true},
        },
    },
})
```

This is already documented but not enforced — callers can skip preconditions. Add a prominent warning in the documentation and examples.

### Change 4: Concurrent merge safety (future)

For the merge case, the proper fix is `SELECT FOR UPDATE` on the entity row before applying the merge. This serializes concurrent merges:

```go
// Inside BatchWrite transaction:
row := tx.QueryRow("SELECT * FROM entities WHERE id = $1 FOR UPDATE", entityID)
// Now we hold the row lock — other merges wait
// Apply merge logic
// Commit releases the lock
```

This is a more invasive change to `BatchWrite` internals. Defer to a follow-up after version column ships.

## Implementation Order

1. **Version column** — migration + SQL changes + `StoredEntity.Version` + `WriteEntityOp.Version` + `ErrConflict` + `WithVersion` option
2. **Soft delete guard** — add `AND deleted_at IS NULL` to UPDATE/MERGE queries
3. **Documentation** — anchor race prevention, concurrency best practices
4. **Concurrent merge safety** — `SELECT FOR UPDATE` in merge path (follow-up)

## Non-goals

- **Pessimistic locking on reads** — `SELECT FOR UPDATE` on every `GetEntity` would serialize all reads. Not worth the throughput cost.
- **Distributed locking** — EntityStore relies on PostgreSQL transaction isolation. External lock managers (Redis, etcd) are not needed.
- **Conflict-free merge** — CRDTs or OT for automatic conflict resolution. Overkill for entity resolution where the matcher already handles field-level conflict strategies.
