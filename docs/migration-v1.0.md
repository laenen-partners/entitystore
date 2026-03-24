# Migration Guide: v0.20 → v1.0

v1.0 is the first stable release. It includes breaking changes to relation query signatures, soft deletes replacing hard deletes, and several new features. All changes were introduced incrementally in v0.19 and v0.20.

## Breaking changes

### 1. `GetRelationsFromEntity` and `GetRelationsToEntity` — new pagination parameters

These methods now require `pageSize` and `cursor` for pagination, matching the pattern used by `FindConnectedByType` and `GetEntitiesByType`.

**Before:**
```go
rels, err := es.GetRelationsFromEntity(ctx, entityID)
rels, err := es.GetRelationsToEntity(ctx, entityID)
```

**After:**
```go
rels, err := es.GetRelationsFromEntity(ctx, entityID, 0, nil)   // 0 = default (1000), nil = first page
rels, err := es.GetRelationsToEntity(ctx, entityID, 100, nil)   // explicit page size

// Paginate with cursor:
rels, _ := es.GetRelationsFromEntity(ctx, entityID, 50, nil)
if len(rels) == 50 {
    lastCreatedAt := rels[len(rels)-1].CreatedAt
    nextPage, _ := es.GetRelationsFromEntity(ctx, entityID, 50, &lastCreatedAt)
}
```

### 2. `DeleteEntity` is now a soft delete

`DeleteEntity` sets `deleted_at` instead of removing the row. The entity's data, relations, anchors, tokens, and provenance are preserved for audit. All read queries automatically filter soft-deleted entities.

**Before:**
```go
es.DeleteEntity(ctx, id) // permanently removes entity + CASCADE
```

**After:**
```go
es.DeleteEntity(ctx, id)     // soft delete — sets deleted_at, data preserved
es.HardDeleteEntity(ctx, id) // permanent removal (new method for cleanup)
```

If your code depends on `DeleteEntity` immediately freeing storage or cascading relation deletes, switch to `HardDeleteEntity`.

### 3. `ConnectedEntities` now has a default limit of 1000

Previously returned all connected entities with no limit (two unbounded queries). Now uses a single UNION query with LIMIT 1000. If you have entities with more than 1000 connections, use `FindConnectedByType` with explicit pagination.

## Database migration

Two new migrations run automatically via `Migrate()` or `WithAutoMigrate()`:

**Migration 3** — Missing indexes:
```sql
CREATE INDEX idx_rel_target_type ON entity_relations (target_id, relation_type);
CREATE INDEX idx_rel_source_urn ON entity_relations (source_urn) WHERE source_urn IS NOT NULL;
```

**Migration 4** — Soft deletes:
```sql
ALTER TABLE entities ADD COLUMN deleted_at TIMESTAMPTZ;
CREATE INDEX idx_entities_deleted ON entities (deleted_at) WHERE deleted_at IS NOT NULL;
```

No manual intervention needed. Existing entities get `deleted_at = NULL` (not deleted).

## New features

### EntityStorer interface

Both `EntityStore` and `ScopedStore` satisfy `EntityStorer`. Use it for dependency injection:

```go
type MyService struct {
    es entitystore.EntityStorer
}

// In production:
svc := &MyService{es: entityStore}
svc := &MyService{es: entityStore.Scoped(cfg)}

// In tests:
svc := &MyService{es: mockStore}
```

The interface includes all read, write, tag, embedding, traversal, and stats methods.

### GetByAnchor convenience method

Single-anchor lookup that returns one entity or `ErrNotFound`:

```go
entity, err := es.GetByAnchor(ctx, "persons.v1.Person", "email", "alice@example.com", nil)
if errors.Is(err, entitystore.ErrNotFound) {
    // not found
}
```

### TxStore read methods

Transactions now support reads alongside writes:

```go
tx, _ := es.Tx(ctx)
defer tx.Rollback(ctx)

entity, _ := tx.GetEntity(ctx, id)
matches, _ := tx.FindByAnchors(ctx, entityType, anchors, nil)
rels, _ := tx.GetRelationsFromEntity(ctx, id, 0, nil)
rels, _ = tx.GetRelationsToEntity(ctx, id, 0, nil)

tx.WriteEntity(ctx, &op)
tx.Commit(ctx)
```

### Stats and count queries

```go
stats, _ := es.Stats(ctx)
// stats.TotalEntities    — total non-deleted entities
// stats.TotalRelations   — total relations
// stats.SoftDeleted      — count of soft-deleted entities
// stats.EntityTypes      — [{Type: "persons.v1.Person", Count: 1234}, ...]
// stats.RelationTypes    — [{Type: "works_at", Count: 567}, ...]

// Individual counts:
count, _ := es.CountEntitiesByType(ctx, "persons.v1.Person")
relCount, _ := es.CountRelationsForEntity(ctx, entityID)
```

### Input validation

- **Tags:** max 255 characters per tag, max 100 tags per entity, empty strings rejected
- **Relation types:** max 255 characters, empty strings rejected
- **BatchWrite:** max 1000 operations per call (`entitystore.MaxBatchSize`)

Validation errors are returned immediately before any database operations.

### Structured logging

```go
es, _ := entitystore.New(
    entitystore.WithPgStore(pool),
    entitystore.WithLogger(slog.Default()),
)
```

Debug-level logging for `FindByAnchors` (match count), `BatchWrite` (op count), and `Traverse` (depth, result count).

### Generated WriteOp, Tokens, EmbedText, Anchors

See [Migration Guide v0.17→v0.18](migration-v0.18.md) for the `protoc-gen-entitystore` codegen enhancements (WriteOp builders, typed token/anchor extraction, WriteOpOption helpers).

## Migration checklist

- [ ] Update dependency: `go get github.com/laenen-partners/entitystore@v1.0.0`
- [ ] Run migrations: `entitystore.Migrate(ctx, pool)` — applies migration 3 (indexes) and 4 (soft deletes)
- [ ] Update `GetRelationsFromEntity` calls — add `0, nil` for default pagination
- [ ] Update `GetRelationsToEntity` calls — add `0, nil` for default pagination
- [ ] Review `DeleteEntity` usage — now soft deletes; use `HardDeleteEntity` if permanent removal needed
- [ ] Adopt `EntityStorer` interface for dependency injection (optional but recommended)
- [ ] Regenerate proto code if using `protoc-gen-entitystore` codegen: `buf generate`
- [ ] Run tests
