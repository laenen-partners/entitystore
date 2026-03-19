# EntityStore

Pure Go library for entity storage, deduplication, and relationship management with PostgreSQL + pgvector backend. Includes `protoc-gen-entitystore`, a buf plugin that generates entity matching configs and LLM extraction schemas from proto annotations.

## Quick reference

- **Language:** Go 1.25+
- **Database:** PostgreSQL 16 with pgvector
- **ORM/queries:** SQLC (type-safe generated code)
- **Task runner:** [Task](https://taskfile.dev) (`Taskfile.yml`)
- **Proto tool:** [Buf](https://buf.build) (`buf.yaml`, `buf.gen.yaml`)

## Project structure

```
entitystore.go               Library entry point: New(), EntityStore, re-exported types
options.go                   Options: WithPgStore()
cmd/protoc-gen-entitystore/  Buf plugin: proto annotations → matching configs
proto/entitystore/v1/        Proto annotation definitions (options.proto)
gen/                         Generated protobuf code (do not edit)
matching/                    Domain logic: anchors, tokens, embeddings, normalizers, extraction schemas
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

// Migrations
entitystore.Migrate(ctx, pool)
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

Buf plugin that reads `(entitystore.v1.field)` and `(entitystore.v1.message)` proto annotations and generates two Go functions per annotated message:
- `{Message}MatchConfig()` → `matching.EntityMatchConfig` for deduplication and matching
- `{Message}ExtractionSchema()` → `matching.ExtractionSchema` for LLM-based entity extraction

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

This produces `job_posting_entitystore.go` with `JobPostingMatchConfig()` and `JobPostingExtractionSchema()` functions.

**5. Register in Go code:**

```go
import (
    "github.com/laenen-partners/entitystore/matching"
    jobsv1 "example.com/jobs/gen/jobs/v1"
)

// Match configs for deduplication.
mcr := matching.NewMatchConfigRegistry()
mcr.Register(jobsv1.JobPostingMatchConfig())

// Extraction schemas for LLM entity extraction.
esr := matching.NewExtractionSchemaRegistry()
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
- The `matching` package contains pure domain logic with no database dependency.
