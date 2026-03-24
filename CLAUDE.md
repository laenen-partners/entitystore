# EntityStore

Pure Go library for entity storage, deduplication, and relationship management with PostgreSQL + pgvector backend. Includes `protoc-gen-entitystore`, a buf plugin that generates matching configs, extraction schemas, and typed write helpers from proto annotations.

## Quick reference

- **Language:** Go 1.25+
- **Database:** PostgreSQL 16 with pgvector
- **ORM/queries:** SQLC (type-safe generated code)
- **Task runner:** [Task](https://taskfile.dev) (`Taskfile.yml`)
- **Proto tool:** [Buf](https://buf.build) (`buf.yaml`, `buf.gen.yaml`)

## Project structure

```
entitystore.go               Library entry point: New(), EntityStore, re-exported types
interface.go                 EntityStorer interface (satisfied by EntityStore + ScopedStore)
scoped.go                    ScopedStore: tag-based multi-tenant filtering wrapper
options.go                   Options: WithPgStore(), WithLogger()
cmd/protoc-gen-entitystore/  Buf plugin: proto annotations → matching configs + write helpers
proto/entitystore/v1/        Proto annotation definitions (options.proto)
gen/                         Generated protobuf code (do not edit)
matching/                    Domain logic: matcher, similarity, anchors, tokens, embeddings, normalizers
extraction/                  LLM extraction schema types and registry
store/                       PostgreSQL persistence layer (SQLC)
store/db/migrations          SQL migrations (embedded at build time)
store/db/queries             SQLC query definitions
store/internal/dbgen         Generated SQLC code (do not edit)
examples/                    Comprehensive Go examples for all features
examples/proto/              Example proto definitions with full annotations
docs/adr/                    Architecture Decision Records
```

## Usage

```go
import (
    "github.com/jackc/pgx/v5/pgxpool"
    "github.com/laenen-partners/entitystore"
)

pool, _ := pgxpool.New(ctx, connString)
es, err := entitystore.New(entitystore.WithPgStore(pool))
defer es.Close()

// Read entities
entity, _ := es.GetEntity(ctx, "entity-id")
matches, _ := es.FindByAnchors(ctx, "entities.v1.Person", anchors, nil)

// Unmarshal entity data into proto message
var person entitiesv1.Person
entity.GetData(&person)

// Write entities — Data accepts proto.Message, EntityType is derived automatically
results, _ := es.BatchWrite(ctx, []entitystore.BatchWriteOp{
    {WriteEntity: &entitystore.WriteEntityOp{
        Action: entitystore.WriteActionCreate,
        Data:   &entitiesv1.Person{Email: "alice@example.com"},
        // EntityType derived as "entities.v1.Person"
    }},
})

// Write relations — Data accepts proto.Message, DataType derived automatically
es.BatchWrite(ctx, []entitystore.BatchWriteOp{
    {UpsertRelation: &entitystore.UpsertRelationOp{
        SourceID: personID, TargetID: companyID,
        RelationType: "works_at", Confidence: 0.95,
        Data: &employmentv1.Employment{Role: "CTO"},
    }},
})

// Query relations (paginated)
outbound, _ := es.GetRelationsFromEntity(ctx, personID, 0, nil)  // source → target
inbound, _ := es.GetRelationsToEntity(ctx, companyID, 0, nil)    // inbound to companyID
connected, _ := es.ConnectedEntities(ctx, personID)              // all connected (both directions)
companies, _ := es.FindConnectedByType(ctx, personID,         // filtered by entity type + relation type
    &entitystore.FindConnectedOpts{EntityType: "companies.v1.Company", RelationTypes: []string{"works_at"}})
employees, _ := es.FindEntitiesByRelation(ctx,                // all entities in a relation type
    "persons.v1.Person", "works_at", nil)

// Update / delete relations
es.UpdateRelationData(ctx, personID, companyID, "works_at", updatedData)
es.DeleteRelationByKey(ctx, personID, companyID, "works_at")

// Graph traversal — multi-hop exploration from an entity
results, _ := es.Traverse(ctx, "entity-id", &entitystore.TraverseOpts{
    MaxDepth:      3,
    Direction:     entitystore.DirectionBoth,
    RelationTypes: []string{"employed_by", "knows"},
    MinConfidence: 0.5,
})
for _, r := range results {
    fmt.Printf("depth %d: %s (%s)\n", r.Depth, r.Entity.ID, r.Entity.EntityType)
}

// Soft deletes — data preserved for audit
es.DeleteEntity(ctx, id)     // sets deleted_at, filtered from all reads
es.HardDeleteEntity(ctx, id) // permanent removal with CASCADE

// Stats — understand what's in the database
stats, _ := es.Stats(ctx)
// stats.TotalEntities, stats.TotalRelations, stats.SoftDeleted
// stats.EntityTypes  → [{Type: "persons.v1.Person", Count: 1234}, ...]
// stats.RelationTypes → [{Type: "works_at", Count: 567}, ...]

// Single anchor lookup
entity, _ := es.GetByAnchor(ctx, "persons.v1.Person", "email", "alice@example.com", nil)

// EntityStorer interface — use for dependency injection and testing
var store entitystore.EntityStorer = es  // or es.Scoped(cfg)

// Migrations
entitystore.Migrate(ctx, pool)

// Shared transactions with reads + writes
tx, _ := es.Tx(ctx)
defer tx.Rollback(ctx)
existing, _ := tx.GetEntity(ctx, id)        // read within tx
matches, _ := tx.FindByAnchors(ctx, ...)    // anchor lookup within tx
tx.WriteEntity(ctx, &op)
tx.Commit(ctx)
```

## Common commands

```sh
task generate         # buf generate + sqlc generate
task generate:plugin  # install protoc-gen-entitystore
task lint             # go vet ./...
task test             # go test -v -count=1 ./store/... (uses testcontainers)
task test:cover       # tests with coverage
task tidy             # go mod tidy
```

## protoc-gen-entitystore

Buf plugin that reads `(entitystore.v1.field)` and `(entitystore.v1.message)` proto annotations and generates up to six functions per annotated message:
- `{Message}MatchConfig()` → `matching.EntityMatchConfig` for deduplication and matching
- `{Message}ExtractionSchema()` → `extraction.ExtractionSchema` for LLM-based entity extraction
- `{Message}Tokens(msg)` → `map[string][]string` — statically-typed token extraction (no reflection)
- `{Message}EmbedText(msg)` → `string` — concatenated embed fields for embedding input
- `{message}Anchors(msg)` → `[]matching.AnchorQuery` — anchor extraction with normalizers applied (unexported)
- `{Message}WriteOp(msg, action, opts...)` → `*store.WriteEntityOp` — complete write op with anchors, tokens, and data wired automatically

### Using in a downstream project

**1. Add the BSR dependency** to your `buf.yaml`:

```yaml
version: v2
modules:
  - path: proto
deps:
  - buf.build/laenen-partners/entitystore
```

Then run `buf dep update`.

**2. Annotate your proto messages:**

```protobuf
syntax = "proto3";
package jobs.v1;

import "entitystore/v1/options.proto";

message JobPosting {
  option (entitystore.v1.message) = {
    match_thresholds: {auto_match: 0.85, review_zone: 0.60}
  };

  string reference = 1 [(entitystore.v1.field) = {
    anchor: true
    similarity: SIMILARITY_FUNCTION_EXACT
    weight: 0.40
    normalizer: NORMALIZER_LOWERCASE_TRIM
  }];

  // Job title or position name.
  string title = 2 [(entitystore.v1.field) = {
    similarity: SIMILARITY_FUNCTION_TOKEN_JACCARD
    weight: 0.30
    embed: true
    token_field: true
    extraction_hint: "Extract the exact title as stated"
    examples: ["Senior Software Engineer", "Head of Product"]
  }];
}
```

**3. Configure `buf.gen.yaml`** to run the plugin via `go run`:

```yaml
version: v2
plugins:
  - remote: buf.build/protocolbuffers/go
    out: gen
    opt: paths=source_relative
  - local: ["go", "run", "github.com/laenen-partners/entitystore/cmd/protoc-gen-entitystore@latest"]
    out: gen
    opt: paths=source_relative
```

Using `local: ["go", "run", ...]` means no separate install step — buf invokes `go run` directly, which downloads and caches the plugin binary automatically via the Go module cache.

Alternatively, if you prefer a pre-installed binary:

```yaml
  - local: protoc-gen-entitystore
    out: gen
    opt: paths=source_relative
```

With `go install github.com/laenen-partners/entitystore/cmd/protoc-gen-entitystore@latest` run beforehand.

**4. Generate:**

```sh
buf generate
```

This produces `job_posting_entitystore.go` with all generated functions.

**5. Use in Go code:**

```go
import (
    "github.com/laenen-partners/entitystore/store"
    jobsv1 "example.com/jobs/gen/jobs/v1"
)

// Create entity — anchors, tokens wired automatically from proto annotations.
posting := &jobsv1.JobPosting{Reference: "JOB-001", Title: "Senior Engineer"}
op := jobsv1.JobPostingWriteOp(posting, store.WriteActionCreate,
    store.WithTags("ws:acme", "status:active"),
    store.WithProvenance(matching.ProvenanceEntry{SourceURN: "crm:job/1", ModelID: "gpt-4o"}),
)
results, _ := es.BatchWrite(ctx, []store.BatchWriteOp{{WriteEntity: op}})

// Token extraction (no reflection).
tokens := jobsv1.JobPostingTokens(posting)  // map[string][]string

// Embed text for embedding models.
text := jobsv1.JobPostingEmbedText(posting)  // "Senior Engineer"

// Match configs and extraction schemas for registries.
mcr := matching.NewMatchConfigRegistry()
mcr.Register(jobsv1.JobPostingMatchConfig())
esr := extraction.NewExtractionSchemaRegistry()
esr.Register(jobsv1.JobPostingExtractionSchema())
```

### Extraction schema annotations

Field descriptions are resolved in this order:
1. Explicit `description` annotation (when the LLM needs different wording than developer docs)
2. Proto leading comment on the field (default for most fields)
3. Humanized field name (e.g., `full_name` → `"full name"`)

Available extraction annotations on `(entitystore.v1.field)`:
- `description` — explicit description override
- `extraction_hint` — directive instruction for the LLM
- `extract` — set to `false` to exclude from extraction output
- `examples` — sample values for few-shot grounding

Available extraction annotations on `(entitystore.v1.message)`:
- `extraction_prompt` — system-level extraction instruction
- `extraction_instructions` — additional context/edge-case handling
- `extraction_display_name` — human-friendly entity name

See `examples/proto/` for fully annotated proto definitions and `examples/` for Go usage patterns.

### Publishing proto changes

```sh
task proto:login  # one-time BSR login (opens browser)
task proto:push   # lint + push to buf.build/laenen-partners/entitystore
```

## Release process

- After creating a git tag (`git tag vX.Y.Z`), always create GitHub release notes with `gh release create`.
- Release notes should summarize key changes, breaking changes, new types, and usage examples.
- Push the updated proto to BSR with `task proto:push` if `options.proto` changed.

## Code conventions

- No `init()` functions; wire dependencies explicitly.
- Generated code lives in `gen/` and `store/internal/dbgen/` — never edit manually, regenerate with `task generate`.
- Errors are wrapped with `fmt.Errorf("context: %w", err)`.
- Use `slog` for structured logging.
- SQL queries are defined in `store/db/queries/*.sql` and generated with SQLC.
- Migrations are embedded and applied via `github.com/laenen-partners/migrate` (scoped to `entitystore` in `scoped_schema_migrations` table). Use `store.WithAutoMigrate()` option or call `entitystore.Migrate(ctx, pool)` directly.
- The `matching` package contains pure domain logic. It depends on `google.golang.org/protobuf` for `GetData()` convenience methods.
- The `extraction` package contains LLM extraction schema types — independent of matching.
- `Embedder` interface in `matching` is compatible with `github.com/laenen-partners/embedder` — no adapter needed.
- `WithTx(pgx.Tx)` on `EntityStore` enables shared transactions across stores sharing the same PostgreSQL pool.
- `ScopedStore` wraps `EntityStore` with tag-based multi-tenant filtering. Created via `es.Scoped(ScopeConfig{...})`. Reads are filtered by `RequireTags`/`ExcludeTag`/`UnlessTags`; creates are auto-tagged with `AutoTags`. Scope config is preserved across `WithTx`.
- `EntityStorer` interface in `interface.go` is satisfied by both `EntityStore` and `ScopedStore`. Use for dependency injection and mocking in tests.
- `DeleteEntity` performs a soft delete (sets `deleted_at`). All reads filter `deleted_at IS NULL`. Use `HardDeleteEntity` for permanent removal.
- `TxStore` is defined in the root package (not `store`). Consumers never need to import `store` for transactions. Supports reads (`GetEntity`, `FindByAnchors`, `GetRelationsFromEntity`, `GetRelationsToEntity`) alongside writes.
- `GetEntitiesByType` accepts an optional `*QueryFilter` as the last parameter (nil = no filter). The old `GetEntitiesByTypeFiltered` method is removed.
- `FindConnectedByType` takes a `*FindConnectedOpts` struct instead of 7 positional parameters. Consistent with `TraverseOpts` pattern.
- `Stats(ctx)` returns aggregate counts (entities, relations, soft-deleted, per-type breakdowns). Individual count methods also available.
- `WithLogger(*slog.Logger)` enables structured debug logging for operations.
- `MaxBatchSize = 1000` caps BatchWrite operations. Input validation enforces tag limits (255 chars, 100 max) and relation type limits (255 chars).
- `Traverse(ctx, entityID, opts)` performs multi-hop graph traversal using a recursive CTE. Supports direction control, relation/entity type filtering, confidence thresholds, tag filtering, and depth/result caps. Raw SQL (not SQLC) due to recursive CTE complexity. See ADR-007.
- `WriteOpOption` type and option functions (`WithTags`, `WithConfidence`, `WithMatchedEntityID`, `WithEmbedding`, `WithID`, `WithProvenance`) live in `store/write_options.go` and are re-exported from the root package. Used by generated `WriteOp` functions.
- Generated `{Entity}WriteOp` returns `*store.WriteEntityOp` so callers can write `{WriteEntity: jobsv1.JobPostingWriteOp(msg, action, opts...)}` directly.
