# ADR-003: Relation Data Querying and Graph Search

**Status:** Partially implemented — Phase 3 (recursive CTE traversal) shipped in v0.17.0. Phases 1-2 (JSONB queries, relation anchors) deferred.
**Date:** 2026-03-19
**Author:** Pascal Laenen

## Context

EntityStore v0.8 introduced typed proto data on relations (`DataType` + `Data` as `json.RawMessage`). However, this data is currently opaque — you can store an `Employment{Role: "CEO", StartDate: "2023-01-15"}` on a `works_at` relation, but there is no way to query by those fields.

Real-world entity resolution needs graph-aware queries:

- *"Find all people who work at company X as CEO"* → filter relations by `data.role`
- *"Find the employment relationship that started most recently"* → sort by `data.start_date`
- *"Find all companies connected to this person through 2+ hops"* → multi-hop traversal
- *"Find duplicate entity clusters"* → connected component detection

This ADR evaluates how to add relation data querying and graph search capabilities.

## Options Evaluated

### Option 1: PostgreSQL JSONB queries on existing `data` column

Add a GIN index on `entity_relations.data` and expose SQLC queries that use JSONB operators.

```sql
-- GIN index for containment queries
CREATE INDEX idx_rel_data_gin ON entity_relations USING GIN (data);

-- Find relations where data matches field values
SELECT * FROM entity_relations
WHERE relation_type = 'works_at'
  AND data @> '{"role": "CEO"}';
```

**Pros:**
- Zero infrastructure changes — works with existing PostgreSQL
- GIN index makes containment queries fast
- SQLC can generate type-safe Go wrappers
- No new dependencies

**Cons:**
- JSONB queries use `protojson` camelCase keys (e.g., `startDate` not `start_date`) — callers must be aware
- No type safety on query predicates — querying wrong field names silently returns no results
- Range queries on JSONB values (dates, numbers) require casting and are less ergonomic
- Limited to single-hop queries without recursive CTEs

### Option 2: Relation anchors (indexed field extraction)

Mirror the entity anchor pattern for relations. Extract key fields from relation data into a separate indexed table at write time.

```sql
CREATE TABLE relation_anchors (
    relation_id UUID NOT NULL REFERENCES entity_relations(id) ON DELETE CASCADE,
    anchor_field TEXT NOT NULL,
    normalized_value TEXT NOT NULL,
    PRIMARY KEY (relation_id, anchor_field)
);
CREATE INDEX idx_rel_anchor_lookup ON relation_anchors (anchor_field, normalized_value);
```

```go
type UpsertRelationOp struct {
    // ...
    Data    proto.Message
    Anchors []RelationAnchor  // indexed queryable fields extracted from Data
}
```

**Pros:**
- Consistent with entity anchor pattern — familiar API
- Indexed, fast, type-safe at write time
- Works with existing SQLC/pgx stack
- Proto field names (snake_case) used as anchor keys — no camelCase confusion
- Could be auto-derived from proto annotations (Option 4 synergy)

**Cons:**
- More storage (separate table)
- Caller must specify which fields to index (or annotations auto-derive them)
- Still single-hop unless combined with recursive CTEs

### Option 3: PostgreSQL recursive CTEs for multi-hop traversal

Use PostgreSQL's built-in recursive CTEs for graph traversal queries.

```sql
-- Find all entities connected within 3 hops
WITH RECURSIVE connected AS (
    SELECT target_id AS id, 1 AS depth, ARRAY[source_id] AS path
    FROM entity_relations
    WHERE source_id = $1
    UNION ALL
    SELECT r.target_id, c.depth + 1, c.path || r.source_id
    FROM entity_relations r
    JOIN connected c ON r.source_id = c.id
    WHERE c.depth < 3
      AND NOT r.target_id = ANY(c.path)  -- cycle detection
)
SELECT DISTINCT e.* FROM connected c
JOIN entities e ON e.id = c.id;
```

**Pros:**
- No additional infrastructure or extensions
- Works with existing pgx/SQLC setup
- Adequate for 1-3 hop traversals (entity resolution's main use case)
- PostgreSQL 17+ pushes predicates into CTEs for better performance

**Cons:**
- Verbose syntax compared to Cypher
- Performance degrades with deep recursion (5+ hops) or wide fan-out
- No built-in graph algorithms (shortest path, community detection, PageRank)
- Manual cycle detection required

### Option 4: Proto annotations for relation data messages

Extend `protoc-gen-entitystore` to support annotations on relation data messages. The same `anchor` annotation that indexes entity fields would also work for relation data fields.

```protobuf
message Employment {
  string role = 1 [(entitystore.v1.field) = {
    anchor: true  // indexed for relation queries
  }];
  string start_date = 2 [(entitystore.v1.field) = {
    anchor: true
  }];
  float salary = 3;  // not indexed
}
```

The generated code would produce a `EmploymentRelationAnchors(msg proto.Message) []RelationAnchor` function that extracts indexed fields. The store layer calls this automatically during `UpsertRelation`.

**Pros:**
- Fully declarative — annotations drive indexing
- Type-safe — proto field names are validated at compile time
- Consistent with entity matching pattern
- Auto-derived anchors — no manual specification by caller

**Cons:**
- Requires codegen extension
- Only useful if relation data messages are annotated (opt-in)
- Doesn't solve multi-hop traversal

### Option 5: Apache AGE (PostgreSQL graph extension)

Add Apache AGE as a PostgreSQL extension alongside pgvector. AGE adds openCypher query support directly in PostgreSQL.

```sql
-- Load AGE
LOAD 'age';
SET search_path = ag_catalog, "$user", public;

-- Create a graph
SELECT create_graph('entity_graph');

-- Cypher queries
SELECT * FROM cypher('entity_graph', $$
    MATCH (p:Person)-[r:works_at]->(c:Company)
    WHERE r.role = 'CEO'
    RETURN p, r, c
$$) AS (p agtype, r agtype, c agtype);
```

**Pros:**
- Cypher expressiveness for complex graph patterns
- Runs in the same PostgreSQL instance — no separate database
- Can coexist with pgvector
- Apache 2.0 license

**Cons:**
- Go driver uses `database/sql` (not pgx) — different connection management from rest of codebase
- Requires maintaining a parallel graph representation (AGE has its own vertex/edge storage)
- Data must be synced between EntityStore tables and AGE graph — dual-write complexity
- Extension maturity and PostgreSQL version compatibility concerns
- Adds operational complexity (extension installation, upgrades)

### Option 6: Dgraph as secondary graph store

Run Dgraph alongside PostgreSQL. Sync relation data to Dgraph for graph queries.

**Pros:**
- Purpose-built for graph queries, Go-native (gRPC client)
- Distributed, scalable
- Apache 2.0 license

**Cons:**
- Separate database to operate (significant infrastructure cost)
- Data synchronization between PostgreSQL and Dgraph (dual-write, eventual consistency)
- Two ownership changes in two years (Dgraph Labs → Hypermode → Istari Digital) — governance stability risk
- Overkill for the entity resolution use case

### Option 7: Neo4j as secondary graph store

**Cons:** GPLv3 + Commons Clause licensing (not truly open source by OSI standards). Expensive Enterprise license. Separate database. Same dual-write challenges as Dgraph. Best Go driver quality, but doesn't justify the operational overhead.

### Option 8: In-memory graph library for cluster analysis

Use `dominikbraun/graph` (pure Go, BSD license) to load entity relationship subgraphs into memory for algorithms like connected component detection, cluster analysis, and deduplication scoring.

```go
g := graph.New(graph.StringHash, graph.Directed())
// Load entities as vertices, relations as edges
// Run connected components, shortest path, etc.
```

**Pros:**
- Zero infrastructure — pure Go library
- Good for batch analysis (find duplicate clusters, detect cycles)
- No operational overhead
- Complements PostgreSQL queries for algorithmic work

**Cons:**
- Ephemeral — graph must be reconstructed from DB on each operation
- Memory-bound — doesn't scale to millions of entities per query
- Not a query engine — good for algorithms, not ad-hoc queries

## Decision

**Phased approach combining Options 1, 2, 4, 3, and 8:**

### Phase 1: JSONB queries + GIN index (immediate)
Add a GIN index on `entity_relations.data` and expose SQLC query methods for filtering relations by data field values. This gives immediate queryability with zero schema changes.

New store methods:
```go
FindRelationsByData(ctx, relationType string, dataFilter json.RawMessage) ([]StoredRelation, error)
FindRelatedEntities(ctx, entityID string, relationType string, dataFilter json.RawMessage) ([]StoredEntity, error)
```

### Phase 2: Relation anchors + proto annotations (near-term)
Add the `relation_anchors` table and extend proto annotations to auto-derive indexed fields from relation data messages. This gives type-safe, indexed queries on relation data.

### Phase 3: Recursive CTEs for multi-hop traversal (near-term)
Add store methods for 1-3 hop graph traversals using recursive CTEs. This covers the primary entity resolution use cases (find clusters, follow relationship chains).

New store methods:
```go
FindConnectedWithinHops(ctx, entityID string, maxHops int, relationTypes []string) ([]StoredEntity, error)
FindPath(ctx, sourceID string, targetID string, maxHops int) ([]StoredRelation, error)
```

### Phase 4: In-memory graph analysis (future)
Add optional integration with `dominikbraun/graph` for batch operations like connected component detection, duplicate cluster identification, and cycle detection. This would be a separate package (`graph/` or `analysis/`) that loads subgraphs from the store.

### Not adopted (for now)
- **Apache AGE** — the dual-write complexity and `database/sql` driver limitation outweigh the Cypher expressiveness for our use case. Reconsider if PostgreSQL adds native SQL/PGQ support (SQL:2023 standard) or if AGE's Go driver moves to pgx.
- **Dgraph / Neo4j / Neptune** — separate database operational overhead is not justified. EntityStore is a library, not a platform. Adding a second database dependency would limit adoption.

## Consequences

### Positive
- **Progressive complexity** — start with JSONB (zero effort), add indexed anchors (moderate), add traversals (moderate), add algorithms (optional). Each phase is independently useful.
- **Single database** — everything stays in PostgreSQL. No dual-write, no sync, no additional infrastructure.
- **Consistent with existing patterns** — relation anchors mirror entity anchors. Proto annotations drive both.
- **Library stays embeddable** — no mandatory external services.

### Negative
- **Not a graph database** — deep traversals (5+ hops) and complex graph algorithms will be slower than a dedicated graph DB. This is acceptable for entity resolution where 1-3 hops cover 95% of use cases.
- **JSONB query ergonomics** — callers must use camelCase keys (from protojson) in JSONB filters. Can be mitigated with helper functions.

### Risks
- **JSONB GIN index size** — GIN indexes on large JSONB payloads can be significant. Mitigated by the fact that relation data is typically small (5-10 fields).
- **Recursive CTE performance** — wide fan-out graphs (entities with thousands of relations) can produce slow recursive queries. Mitigated with `maxHops` limits and proper indexing.

## Future considerations

- **SQL/PGQ** — the SQL:2023 standard includes Property Graph Queries. When PostgreSQL adds native support, it would replace recursive CTEs with Cypher-like syntax without needing AGE.
- **Apache AGE v2** — if AGE releases a pgx-native Go driver and tighter pgvector integration, it becomes a more attractive option for Phase 3+.
- **Dedicated graph package** — if graph capabilities grow significantly, consider extracting to a `github.com/laenen-partners/entitystore/graph` package (similar to the `extraction` package split).
