# EntityStore

Go library and server for entity storage, deduplication, and relationship management with Connect-RPC API, PostgreSQL + pgvector backend. Includes `protoc-gen-entitystore`, a buf plugin that generates entity matching configs from proto annotations.

## Quick reference

- **Language:** Go 1.25+
- **RPC framework:** Connect-RPC (protobuf)
- **Database:** PostgreSQL 16 with pgvector
- **ORM/queries:** SQLC (type-safe generated code)
- **Task runner:** [Task](https://taskfile.dev) (`Taskfile.yml`)
- **Proto tool:** [Buf](https://buf.build) (`buf.yaml`, `buf.gen.yaml`)

## Project structure

```
cmd/estore/                  Server binary
cmd/protoc-gen-entitystore/  Buf plugin: proto annotations → matching configs
proto/entitystore/v1/        Protobuf definitions (service + options)
gen/                         Generated protobuf/connect code (do not edit)
matching/                    Domain logic: anchors, tokens, embeddings, normalizers
store/                       PostgreSQL persistence layer (SQLC)
store/db/migrations          SQL migrations (embedded at build time)
store/db/queries             SQLC query definitions
store/internal/dbgen         Generated SQLC code (do not edit)
auth.go                      API key auth interceptor
caller.go                    Caller identity context (X-User-ID, X-Service-ID)
middleware.go                Rate limiting, CORS, security headers, request logging
config.go                    Config + ConfigFromEnv()
server.go                    New() wires store + handler + middleware + health
handler.go                   Connect-RPC handler (all RPCs)
docker-compose.yml           PostgreSQL with pgvector
docs/ROADMAP.md              Project roadmap
```

## Common commands

```sh
task generate         # buf generate + sqlc generate
task generate:plugin  # install protoc-gen-entitystore
task lint             # go vet ./...
task build            # go build ./cmd/estore
task run              # run the server
task test             # go test -v -count=1 ./store/...
task test:e2e         # e2e tests (requires infra)
task test:cover       # tests with coverage
task tidy             # go mod tidy
task infra:up         # start Postgres via docker compose
task infra:down       # stop Postgres and remove volumes
task migrate:up       # apply migrations via dbmate
```

## Configuration

| Variable | Default | Description |
|---|---|---|
| `ADDR` | `:3002` | Server listen address |
| `DATABASE_URL` | `postgres://postgres:postgres@localhost:5433/entitystore?sslmode=disable` | PostgreSQL connection string |
| `API_KEYS` | | Comma-separated API keys for RPC auth |
| `RATE_LIMIT` | `10` | Requests per second per IP (0 = disabled) |
| `RATE_BURST` | `20` | Burst allowance per IP |
| `CORS_ORIGINS` | | Comma-separated allowed CORS origins |

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
  - remote: buf.build/connectrpc/go
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

- No `init()` functions; wire dependencies explicitly in `main.go` or `New()`.
- Generated code lives in `gen/` and `store/internal/dbgen/` — never edit manually, regenerate with `task generate`.
- Errors are wrapped with `fmt.Errorf("context: %w", err)`.
- Use `slog` for structured logging.
- SQL queries are defined in `store/db/queries/*.sql` and generated with SQLC.
- Migrations are embedded and applied at startup via dbmate (tracked in `entitystore_migrations` table). Use `store.WithAutoMigrate()` option or call `store.Migrate(connString)` directly.
- The `matching` package contains pure domain logic with no database dependency.
- Security: API key auth (constant-time compare), rate limiting, CORS allowlist, security headers.
- Graceful shutdown with 30s drain timeout.
