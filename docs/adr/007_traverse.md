# ADR-007: Recursive CTE Graph Traversal

**Date**: 2026-03-24
**Status**: Accepted
**Authors**: Pascal Laenen

---

## Context

EntityStore relations are unidirectional edges (source to target), and existing queries handle both directions by running separate outbound+inbound queries. For RAG context enrichment and knowledge graph exploration, we need multi-hop traversal: "give me everything within N hops of this entity."

### Goals

- Multi-hop graph traversal from a starting entity
- Configurable direction (outbound, inbound, both)
- Filtering by relation type, entity type, confidence, and tags
- Cycle prevention (no infinite loops)
- Safety caps on depth and result count
- Works within existing transactions and scoped stores

---

## Decision

### Recursive CTE in PostgreSQL

Use a single `WITH RECURSIVE` CTE query rather than application-level BFS. This keeps all traversal logic in one round-trip and leverages PostgreSQL's built-in cycle detection via a `visited` UUID array.

### Why not a graph database?

- EntityStore already has all the data in PostgreSQL
- Recursive CTEs handle the traversal patterns we need (bounded multi-hop)
- No new infrastructure dependency
- Works within existing pgx transactions

### Why raw SQL instead of SQLC?

SQLC doesn't reliably support recursive CTEs with dynamic boolean parameters for direction control. The query uses raw `pgx.Query` with the store's transaction-aware connection selection (`s.tx` or `s.pool`).

---

## Implementation

### New types in `store/traverse.go`

- `Direction` — controls edge direction (Both, Outbound, Inbound)
- `TraverseOpts` — configures depth, results, filters
- `TraverseResult` — entity + depth + path edges
- `TraverseEdge` — single edge in a traversal path

### SQL structure

The CTE has two parts:
1. **Base case**: select the starting entity with depth 0
2. **Recursive case**: join edges and next entities, filtering by direction, depth, cycle detection, relation type, entity type, confidence, and tags

Path information is accumulated as a JSONB array through recursion. Cycle prevention uses a `uuid[]` visited array with `NOT (next_e.id = ANY(t.visited))`.

### Integration

- `EntityStore.Traverse()` delegates to `Store.Traverse()`
- `ScopedStore.Traverse()` merges scope filters into `TraverseOpts.Filter` before delegating
- Types re-exported from the root `entitystore` package for convenience

### Safety

- `MaxDepth` defaults to 2, capped at 10
- `MaxResults` defaults to 100
- Cycle detection prevents infinite recursion

---

## Consequences

- Single SQL round-trip for multi-hop traversal
- No new tables or migrations required (uses existing `entities` and `entity_relations`)
- Tag filtering stops traversal at invisible entities (scope boundary enforcement)
- Path information enables callers to understand how entities are connected
