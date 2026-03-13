# EntityStore

Pure Go library for entity storage, deduplication, and relationship management with PostgreSQL + pgvector backend. Includes `protoc-gen-entitystore`, a buf plugin that generates entity matching configs from proto annotations.

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
matching/                    Domain logic: anchors, tokens, embeddings, normalizers
store/                       PostgreSQL persistence layer (SQLC)
store/db/migrations          SQL migrations (embedded at build time)
store/db/queries             SQLC query definitions
store/internal/dbgen         Generated SQLC code (do not edit)
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

// Write entities
results, _ := es.BatchWrite(ctx, []entitystore.BatchWriteOp{...})

// Migrations
entitystore.Migrate(connString)
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

Buf plugin that reads `(entitystore.v1.field)` and `(entitystore.v1.message)` proto annotations and generates Go functions returning `matching.EntityMatchConfig` for each annotated message.

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

  string title = 2 [(entitystore.v1.field) = {
    similarity: SIMILARITY_FUNCTION_TOKEN_JACCARD
    weight: 0.30
    embed: true
    token_field: true
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

This produces `job_posting_entitystore.go` with a `JobPostingMatchConfig()` function.

**5. Register in Go code:**

```go
import (
    "github.com/laenen-partners/entitystore/matching"
    jobsv1 "example.com/jobs/gen/jobs/v1"
)

mcr := matching.NewMatchConfigRegistry()
mcr.Register(jobsv1.JobPostingMatchConfig())
```

### Publishing proto changes

```sh
task proto:login  # one-time BSR login (opens browser)
task proto:push   # lint + push to buf.build/laenen-partners/entitystore
```

## Code conventions

- No `init()` functions; wire dependencies explicitly.
- Generated code lives in `gen/` and `store/internal/dbgen/` — never edit manually, regenerate with `task generate`.
- Errors are wrapped with `fmt.Errorf("context: %w", err)`.
- Use `slog` for structured logging.
- SQL queries are defined in `store/db/queries/*.sql` and generated with SQLC.
- Migrations are embedded and applied via `github.com/laenen-partners/migrate` (scoped to `entitystore` in `scoped_schema_migrations` table). Use `store.WithAutoMigrate()` option or call `entitystore.Migrate(ctx, pool)` directly.
- The `matching` package contains pure domain logic with no database dependency.
