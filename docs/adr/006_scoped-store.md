# ADR-006: Scoped Entity Store

**Date**: 2026-03-23
**Status**: Accepted
**Authors**: Pascal Laenen

---

## Context

The `access.ScopedStore` in the domains repo wraps `*entitystore.EntityStore` with tag-based filtering for multi-tenant workspace scoping. However, this pattern is needed by every service that uses entitystore (inbox, jobs, majordomo, butler). Each service currently has to either:

1. Import `domains/access` just for `ScopedStore` (pulls in all 18 domain packages as transitive deps)
2. Reimplement the same tag-filtering wrapper

Neither is acceptable. The scoped store belongs in entitystore itself as a generic, tag-based filtering wrapper.

### Goals

- Eliminate the dependency on `domains/access` for tag-based scoping
- Provide a single, reusable scoping wrapper in entitystore
- Apply scope filters consistently to all read queries
- Auto-tag entities on creation
- Maintain `GetEntity` safety — do not leak existence of out-of-scope entities

---

## Decision

### Add `ScopeConfig` and `ScopedStore` to the entitystore package

```go
type ScopeConfig struct {
    RequireTags []string // reads: entity must have ALL of these (AND)
    ExcludeTag  string   // reads: hide entities with this tag
    UnlessTags  []string // reads: exempt from ExcludeTag if entity has any of these
    AutoTags    []string // writes: appended to Tags on WriteActionCreate
}
```

`ScopedStore` wraps `*EntityStore` and intercepts all read methods to inject scope filters via `QueryFilter`, and all `BatchWrite` create operations to append `AutoTags`.

### Read behaviour

All read methods that accept a `*QueryFilter` merge the scope config into the filter before calling the inner store:

- `RequireTags` → merged into `filter.Tags` (AND semantics)
- `ExcludeTag` → set on `filter.ExcludeTag`
- `UnlessTags` → merged into `filter.UnlessTags`

`GetEntity(id)` fetches first, then checks visibility post-fetch. Returns `ErrAccessDenied` when the entity exists but falls outside the scope. Callers can choose to disguise this as "not found" or surface it directly depending on their security requirements.

`GetEntitiesByType` delegates to `GetEntitiesByTypeFiltered` with the scope filter.

`ConnectedEntities` applies a post-fetch visibility filter since the underlying query has no filter parameter.

### Write behaviour

`BatchWrite` appends `cfg.AutoTags` to `WriteEntityOp.Tags` for `WriteActionCreate` operations only. Updates and merges are not modified — tags on existing entities are managed explicitly.

Relations, tag operations, embeddings, and provenance pass through unchanged.

### Transaction support

```go
func (s *ScopedStore) WithTx(tx pgx.Tx) *ScopedStore
```

Returns a new `ScopedStore` wrapping the transaction-scoped inner store, preserving the scope config.

---

## Consequences

### Benefits

- **Single implementation** — all services get tag-based scoping without importing domains
- **Consistent filtering** — scope filters applied uniformly, no risk of forgetting a filter
- **Explicit access control** — `GetEntity` returns `ErrAccessDenied` for out-of-scope entities; callers decide how to surface this
- **Backward compatible** — `ScopedStore` is a new type; existing `EntityStore` usage is unaffected

### Trade-offs

- **Post-fetch filtering on GetEntity/ConnectedEntities** — these methods fetch first, then filter. This is slightly less efficient than SQL-level filtering but avoids adding new SQL queries.
- **No relation scoping** — relations are graph edges, not entities. Scoping them would require traversal filtering which is out of scope.

### Migration path

1. Implement `ScopedStore` in entitystore (this ADR)
2. Update `domains/access.ScopedStore` to delegate to `entitystore.ScopedStore`
3. Other repos import entitystore directly for scoping
4. Deprecate `access.ScopedStore`

---

## References

- Chain of thought: `docs/chain-of-thoughts/0002-scoped-entity-store.md`
- `domains/access.ScopedStore` — original implementation being upstreamed
