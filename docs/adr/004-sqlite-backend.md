# ADR-004: SQLite Backend for Desktop / Per-Family Databases

**Status:** Accepted (implementation deferred)
**Date:** 2026-03-20
**Author:** Pascal Laenen

## Context

EntityStore currently requires PostgreSQL 16+ with pgvector. This works well for server deployments but is not viable for the single-user desktop use case where each family gets their own database file — no Docker, no PostgreSQL server, just a `.db` file per family.

**Target deployment:**
- Family office desktop application (macOS, Windows, Linux)
- One SQLite database file per family
- Hundreds to low thousands of entities per family (not millions)
- Offline-first, with optional cloud sync
- Embedding API calls when online, core CRUD must work without

## Decision

Add a SQLite store implementation behind the existing `EntityStore` interface. The PostgreSQL store remains the primary backend for server deployments. Both backends are selectable at construction time.

```go
// Server deployment (existing)
es, _ := entitystore.New(entitystore.WithPgStore(pool))

// Desktop deployment (new)
es, _ := entitystore.New(entitystore.WithSQLiteStore("./families/dupont.db"))
```

The `matching`, `extraction`, and code generation packages are unchanged — they operate on `json.RawMessage` and `proto.Message` values, not SQL.

## Design

### Store interface is already backend-agnostic

The `EntityStore` struct delegates to `*store.Store`. All SQL is in the `store` package. The matching pipeline uses the `matching.EntityStore` interface (Go interface, not the struct). A SQLite store just needs to satisfy the same interfaces.

### Package structure

```
store/                      PostgreSQL store (existing)
store/db/migrations/        PostgreSQL migrations (existing)
store/db/queries/           PostgreSQL SQLC queries (existing)
store/internal/dbgen/       PostgreSQL generated code (existing)

sqlitestore/                SQLite store (new)
sqlitestore/migrations/     SQLite migrations (embedded)
sqlitestore/queries/        SQLite SQLC queries
sqlitestore/internal/dbgen/ SQLite generated code
```

Two separate SQLC configurations, two separate generated codebases. No shared SQL. This avoids contorting queries to work on both databases.

### Schema differences

| Feature | PostgreSQL | SQLite |
|---|---|---|
| Primary keys | `UUID DEFAULT gen_random_uuid()` | `TEXT NOT NULL` (UUID generated in Go) |
| JSON data | `JSONB` | `TEXT` (JSON string) |
| Tags | `TEXT[]` column with GIN index | `entity_tags` junction table |
| Tokens | `TEXT[]` column with GIN index | `entity_tokens_flat` junction table |
| Embeddings | `vector(768)` with HNSW index | `BLOB` (raw float32 bytes), no index |
| Timestamps | `TIMESTAMPTZ` | `TEXT` (ISO 8601) |
| JSON merge | `data || $2` (JSONB operator) | `json_patch(data, $2)` |
| Array containment | `tags @> $1::text[]` | `JOIN entity_tags` with `GROUP BY HAVING COUNT = N` |
| Array overlap | `tags && $1::text[]` | `JOIN entity_tags` with `EXISTS` |

### Tag storage: junction table

```sql
-- PostgreSQL (current): tags as array column
CREATE TABLE entities (
    ...
    tags TEXT[] NOT NULL DEFAULT '{}',
);
-- Query: WHERE tags @> ARRAY['ws:family']

-- SQLite (new): tags as junction table
CREATE TABLE entity_tags (
    entity_id TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    tag TEXT NOT NULL,
    PRIMARY KEY (entity_id, tag)
);
CREATE INDEX idx_entity_tags_tag ON entity_tags (tag);

-- Query: WHERE e.id IN (
--   SELECT entity_id FROM entity_tags WHERE tag IN ('ws:family')
--   GROUP BY entity_id HAVING COUNT(*) = 1
-- )
```

### Token storage: junction table

```sql
-- PostgreSQL (current): tokens as array column
CREATE TABLE entity_tokens (
    entity_id UUID NOT NULL,
    entity_type TEXT NOT NULL,
    token_field TEXT NOT NULL,
    tokens TEXT[] NOT NULL,
    PRIMARY KEY (entity_id, token_field)
);

-- SQLite (new): tokens as junction table
CREATE TABLE entity_token_values (
    entity_id TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    entity_type TEXT NOT NULL,
    token_field TEXT NOT NULL,
    token TEXT NOT NULL
);
CREATE INDEX idx_token_values_lookup ON entity_token_values (entity_type, token);
CREATE INDEX idx_token_values_entity ON entity_token_values (entity_id, token_field);
```

### Embedding storage: brute-force in-memory

At desktop scale (hundreds to low thousands of entities), brute-force cosine similarity in Go is fast enough — sub-millisecond for 10K 768-dim vectors.

```sql
-- SQLite: store raw bytes, no vector index
CREATE TABLE entity_embeddings (
    entity_id TEXT PRIMARY KEY REFERENCES entities(id) ON DELETE CASCADE,
    embedding BLOB NOT NULL  -- raw []float32 encoded as bytes
);
```

The SQLite store implements `EmbeddingStore` by:
1. Loading all embeddings for the entity type into memory
2. Computing cosine similarity in Go against each
3. Returning top-K results

This is O(N) per search but N < 10K for a family database, so it completes in <1ms.

If sqlite-vec matures, it can replace the brute-force approach later without changing the interface.

### Partial unique index workaround

PostgreSQL uses `CREATE UNIQUE INDEX idx_rel_dedup ON entity_relations (source_id, target_id, relation_type) WHERE source_urn IS NULL` for relation deduplication. SQLite doesn't support partial unique indexes.

**Workaround:** Split into two approaches:
1. Relations without `source_urn`: regular unique index on `(source_id, target_id, relation_type)` with an additional `CHECK (source_urn IS NULL OR source_urn != '')` constraint
2. Relations with `source_urn`: allow duplicates (same as PostgreSQL behaviour when `source_urn IS NOT NULL`)

Or simpler: handle dedup in Go before inserting.

### Driver choice

Use `modernc.org/sqlite` — a pure Go SQLite implementation (no CGo, no C compiler needed). This is critical for cross-platform desktop distribution. The alternative `github.com/mattn/go-sqlite3` requires CGo which complicates builds.

### SQLC configuration

```yaml
# sqlitestore/sqlc.yaml
version: "2"
sql:
  - engine: "sqlite"
    queries: "queries/"
    schema: "migrations/"
    gen:
      go:
        package: "dbgen"
        out: "internal/dbgen"
        emit_json_tags: true
```

### Migration embedding

Same pattern as PostgreSQL — embed migration SQL files at build time using `go:embed`. Use `github.com/laenen-partners/migrate` with SQLite support (needs to be added to the migrate library).

### Options pattern

```go
// New option alongside existing WithPgStore
func WithSQLiteStore(path string, opts ...sqlitestore.Option) entitystore.Option {
    return func(o *options) {
        o.store = sqlitestore.New(path, opts...)
    }
}

// SQLite-specific options
sqlitestore.WithAutoMigrate()     // apply migrations on open
sqlitestore.WithWAL()             // enable WAL mode (recommended for concurrent reads)
sqlitestore.WithBusyTimeout(5000) // milliseconds to wait for locks
```

## Implementation Plan

### Phase 1: Core CRUD (no embeddings)
1. Create `sqlitestore/` package with SQLite schema (junction tables for tags/tokens)
2. Write SQLC queries for all entity/relation/anchor/token/provenance operations
3. Implement `sqlitestore.Store` satisfying `matching.EntityStore`
4. Add `WithSQLiteStore` option to `entitystore.go`
5. Tests using file-based SQLite (no testcontainers needed)

### Phase 2: Embedding support
1. Add `entity_embeddings` table with BLOB storage
2. Implement brute-force `FindByEmbedding` in Go
3. Implement `EmbeddingStore` interface
4. Benchmark at 1K, 5K, 10K entities to validate performance

### Phase 3: Desktop integration
1. Add WAL mode, busy timeout, connection pooling configuration
2. Database file lifecycle (create, open, close, backup)
3. Schema versioning and migration on app startup

## Effort Estimate

| Phase | Work | Effort |
|---|---|---|
| SQLite migration schema | Junction tables, no arrays/vectors | 1 day |
| SQLC queries (rewrite all 25+ queries) | No array ops, JOIN-based tag/token queries | 2-3 days |
| Go store implementation | Satisfy same interfaces, handle BLOB embeddings | 1-2 days |
| Brute-force embedding search | In-memory cosine similarity | 0.5 day |
| Tests | Mirror PostgreSQL test suite for SQLite | 1 day |
| **Total** | | **5-7 days** |

## Consequences

### Positive
- **Zero infrastructure for desktop** — no Docker, no PostgreSQL server, just a file
- **Per-family isolation** — each family is a separate `.db` file, easy to backup/restore/migrate
- **Same API** — desktop and server apps use identical `EntityStore` interface
- **Pure Go** — `modernc.org/sqlite` has no CGo dependency, simplifies cross-compilation
- **Fast for small datasets** — SQLite is faster than PostgreSQL for small, single-user workloads

### Negative
- **Two SQL codebases** — PostgreSQL and SQLite queries diverge. Bug fixes must be applied to both.
- **No server-grade vector search** — brute-force works for desktop scale but won't scale to 100K+ entities
- **Tag queries are slower** — junction table JOINs vs. GIN-indexed array containment. Acceptable at desktop scale.
- **Maintenance surface area** — two store implementations to test and maintain

### Risks
- **SQLite concurrent writes** — SQLite uses file-level locking. WAL mode helps but heavy concurrent writes (unlikely in single-user desktop) could cause `SQLITE_BUSY`. Mitigated by `busy_timeout`.
- **SQLC SQLite support maturity** — SQLC's SQLite support is newer than PostgreSQL. Some features may behave differently.
- **Migration library** — `github.com/laenen-partners/migrate` may need SQLite support added.

## Alternatives Considered

### A. Single SQL codebase with compatibility layer
Write SQL that works on both PostgreSQL and SQLite using a shared subset. Rejected because:
- Array operations (`@>`, `&&`, `cardinality`) have no SQLite equivalent
- pgvector has no SQLite equivalent
- The SQL would be lowest-common-denominator, losing PostgreSQL performance advantages
- SQLC doesn't support multi-engine from a single query set

### B. DuckDB instead of SQLite
DuckDB supports arrays and has a vector similarity extension. However:
- DuckDB is optimized for analytics, not OLTP
- The Go driver is less mature
- Heavier binary size than SQLite
- Not the standard choice for desktop embedded databases

### C. Embedded PostgreSQL
Run PostgreSQL embedded in the desktop app (e.g., via embedded-postgres-go). Rejected because:
- Large binary size (~100MB for PostgreSQL)
- Complex lifecycle management (start/stop/crash recovery)
- Overkill for a single-user desktop app with <10K entities

### D. Abstract at the Go level, not SQL
Instead of two SQL codebases, implement the SQLite store entirely in Go using `database/sql` directly (no SQLC). Rejected because:
- Loses SQLC's type safety and compile-time query validation
- More error-prone manual SQL string construction
- SQLC SQLite support is mature enough to use
