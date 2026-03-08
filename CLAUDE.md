# EntityStore

Go library and server for entity storage, deduplication, and relationship management with Connect-RPC API, PostgreSQL + pgvector backend.

## Quick reference

- **Language:** Go 1.25+
- **RPC framework:** Connect-RPC (protobuf)
- **Database:** PostgreSQL 16 with pgvector
- **ORM/queries:** SQLC (type-safe generated code)
- **Task runner:** [Task](https://taskfile.dev) (`Taskfile.yml`)
- **Proto tool:** [Buf](https://buf.build) (`buf.yaml`, `buf.gen.yaml`)

## Project structure

```
cmd/estore/         Server binary
proto/              Protobuf definitions
gen/                Generated protobuf/connect code (do not edit)
matching/           Domain logic: anchors, tokens, embeddings, normalizers
store/              PostgreSQL persistence layer (SQLC)
store/db/migrations SQL migrations (embedded at build time)
store/db/queries    SQLC query definitions
store/internal/dbgen Generated SQLC code (do not edit)
config.go           Config + ConfigFromEnv()
server.go           New() wires store + handler
handler.go          Connect-RPC handler (all RPCs)
docker-compose.yml  PostgreSQL with pgvector
Tiltfile            Tilt dev environment
```

## Common commands

```sh
task generate   # buf generate + sqlc generate
task lint       # go vet ./...
task build      # go build ./cmd/estore
task run        # run the server
task test       # go test -v -count=1 ./...
task test:e2e   # e2e tests (requires infra)
task tidy       # go mod tidy
task infra:up   # start Postgres via docker compose
task infra:down # stop Postgres and remove volumes
task migrate:up # apply migrations via dbmate
```

## Configuration

| Variable | Default | Description |
|---|---|---|
| `ADDR` | `:3002` | Server listen address |
| `DATABASE_URL` | `postgres://postgres:postgres@localhost:5433/entitystore?sslmode=disable` | PostgreSQL connection string |

## Code conventions

- No `init()` functions; wire dependencies explicitly in `main.go` or `New()`.
- Generated code lives in `gen/` and `store/internal/dbgen/` -- never edit manually, regenerate with `task generate`.
- Errors are wrapped with `fmt.Errorf("context: %w", err)`.
- Use `slog` for structured logging.
- SQL queries are defined in `store/db/queries/*.sql` and generated with SQLC.
- Migrations are embedded and applied at startup via `store.Migrate()`.
- The `matching` package contains pure domain logic with no database dependency.
