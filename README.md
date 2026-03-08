# EntityStore

An entity storage and deduplication microservice for managing structured entities, their relationships, and provenance. Supports anchor-based exact matching, token-based fuzzy matching, and vector embedding similarity search.

Built with [Connect-RPC](https://connectrpc.com) and [PostgreSQL](https://www.postgresql.org) with [pgvector](https://github.com/pgvector/pgvector).

## Features

- **Entity CRUD** -- Insert, update, delete, and query entities with typed JSON data
- **Anchor matching** -- Exact deduplication via normalized field values (email, tax ID, etc.)
- **Token matching** -- Fuzzy blocking via tokenized field values with overlap scoring
- **Embedding search** -- Vector similarity search via pgvector (cosine distance)
- **Relationships** -- Directed edges between entities with confidence, evidence, and data
- **Tag filtering** -- Filter queries by arbitrary string tags
- **Provenance tracking** -- Full audit trail of entity extraction origin
- **`protoc-gen-entitystore`** -- Buf plugin to define entity types via proto annotations

## Prerequisites

- [mise](https://mise.jdx.dev) (tool manager -- installs Go, Task, Buf, sqlc)
- [Docker](https://docs.docker.com/get-docker/) and [Docker Compose](https://docs.docker.com/compose/)

## Quick start

**1. Clone and install tools**

```sh
git clone https://github.com/laenen-partners/entitystore.git
cd entitystore
mise install
cp .env.sample .env
```

**2. Start infrastructure**

```sh
task infra:up
```

This launches PostgreSQL 16 with pgvector on port 5433.

**3. Run the server**

```sh
task run
```

The server starts on `http://localhost:3002`. Migrations are applied automatically at startup.

**4. Make a request**

```sh
# Without auth (API_KEYS not set in .env)
buf curl --schema proto \
  --data '{
    "entity_type": "entities.v1.Person",
    "data": "{\"name\":\"Alice\",\"email\":\"alice@example.com\"}",
    "confidence": 0.95
  }' \
  http://localhost:3002/entitystore.v1.EntityStoreService/InsertEntity
```

With API keys enabled (`API_KEYS=my-secret-key` in `.env`):

```sh
buf curl --schema proto \
  -H "Authorization: Bearer my-secret-key" \
  --data '{"entity_type": "entities.v1.Person"}' \
  http://localhost:3002/entitystore.v1.EntityStoreService/GetEntitiesByType
```

**5. Health checks**

```sh
curl http://localhost:3002/healthz   # liveness
curl http://localhost:3002/readyz    # readiness
```

## API

All RPCs are defined in [`proto/entitystore/v1/entitystore.proto`](proto/entitystore/v1/entitystore.proto).

| RPC | Description |
|---|---|
| `GetEntity` | Get a single entity by ID |
| `GetEntitiesByType` | List all entities of a given type |
| `InsertEntity` | Create a new entity |
| `UpdateEntity` | Update an existing entity's data and confidence |
| `DeleteEntity` | Delete an entity by ID |
| `FindByAnchors` | Find entities by exact anchor field matches |
| `FindByTokens` | Find entities by token overlap (fuzzy) |
| `FindByEmbedding` | Find entities by vector similarity |
| `FindConnectedByType` | Find entities connected via relationships |
| `UpsertRelation` | Create or update a relationship between entities |
| `GetRelationsFromEntity` | Get all outgoing relationships |
| `GetRelationsToEntity` | Get all incoming relationships |

## Configuration

| Variable | Default | Description |
|---|---|---|
| `ADDR` | `:3002` | Server listen address |
| `DATABASE_URL` | `postgres://...localhost:5433/entitystore?sslmode=disable` | PostgreSQL connection string |
| `API_KEYS` | | Comma-separated API keys for RPC auth (empty = auth disabled) |
| `RATE_LIMIT` | `10` | Requests per second per IP (0 = disabled) |
| `RATE_BURST` | `20` | Burst allowance per IP |
| `CORS_ORIGINS` | | Comma-separated allowed CORS origins |

## Project structure

```
cmd/estore/                  Server binary
cmd/protoc-gen-entitystore/  Buf plugin: proto annotations → matching configs
proto/entitystore/v1/        Protobuf definitions (service + options)
gen/                         Generated code (do not edit)
matching/                    Domain logic (anchors, tokens, embeddings, normalizers)
store/                       PostgreSQL persistence layer
store/db/migrations/         SQL migrations (embedded at build time)
store/db/queries/            SQLC query definitions
store/internal/dbgen/        Generated SQLC code (do not edit)
auth.go                      API key auth interceptor
caller.go                    Caller identity context (X-User-ID, X-Service-ID)
middleware.go                Rate limiting, CORS, security headers, request logging
config.go                    Environment-based configuration
server.go                    Service wiring and initialization
handler.go                   Connect-RPC request handlers
e2e_test.go                  End-to-end integration tests
docker-compose.yml           PostgreSQL infrastructure
Taskfile.yml                 Task runner commands
```

## Defining entity types with proto annotations

Downstream projects can define their own entity types by annotating protobuf messages. The `protoc-gen-entitystore` plugin generates Go matching configs from these annotations.

**1. Add the BSR dependency** to your project's `buf.yaml`:

```yaml
version: v2
modules:
  - path: proto
deps:
  - buf.build/laenen-partners/entitystore
```

Then run `buf dep update`.

**2. Annotate your messages:**

```protobuf
syntax = "proto3";
package jobs.v1;

import "entitystore/v1/options.proto";

message JobPosting {
  option (entitystore.v1.message) = {
    match_thresholds: {auto_match: 0.85, review_zone: 0.60}
    composite_anchors: [{fields: ["company", "reference"]}]
    allowed_relations: ["posted_by", "located_at"]
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

  string company = 3 [(entitystore.v1.field) = {
    similarity: SIMILARITY_FUNCTION_JARO_WINKLER
    weight: 0.20
    normalizer: NORMALIZER_LOWERCASE_TRIM
    embed: true
  }];

  string location = 4 [(entitystore.v1.field) = {
    weight: 0.10
    embed: true
  }];
}
```

**3. Add the plugin** to your `buf.gen.yaml`:

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

Using `local: ["go", "run", ...]` means no separate install step -- buf invokes `go run` which caches the binary automatically.

**4. Generate and register:**

```sh
buf generate
```

This produces `job_posting_entitystore.go` with a `JobPostingMatchConfig()` function. Register it in your Go code:

```go
import (
    "github.com/laenen-partners/entitystore/matching"
    jobsv1 "example.com/jobs/gen/jobs/v1"
)

mcr := matching.NewMatchConfigRegistry()
mcr.Register(jobsv1.JobPostingMatchConfig())
```

### Annotation reference

**Field options** `(entitystore.v1.field)`:

| Option | Type | Description |
|---|---|---|
| `anchor` | bool | Identity field for O(1) dedup lookup |
| `similarity` | enum | `SIMILARITY_FUNCTION_EXACT`, `_JARO_WINKLER`, `_LEVENSHTEIN`, `_TOKEN_JACCARD` |
| `weight` | float | Contribution to composite score (0.0-1.0, sum ~1.0) |
| `conflict_strategy` | enum | `CONFLICT_STRATEGY_FLAG_FOR_REVIEW`, `_LATEST_WINS`, `_HIGHEST_CONFIDENCE` |
| `normalizer` | enum | `NORMALIZER_LOWERCASE_TRIM`, `NORMALIZER_PHONE_NORMALIZE` |
| `embed` | bool | Include in embedding vector input |
| `token_field` | bool | Tokenize for fuzzy blocking |

**Message options** `(entitystore.v1.message)`:

| Option | Type | Description |
|---|---|---|
| `match_thresholds` | message | `auto_match` (auto-merge) and `review_zone` (human review) scores |
| `composite_anchors` | repeated | Multi-field identity keys (all must match) |
| `allowed_relations` | repeated string | Restrict which relation types this entity can have |

## Development

### Available tasks

```sh
task                    # List all tasks
task build              # Build binary to bin/estore
task run                # Run the server
task test               # Run integration tests
task test:e2e           # Run e2e tests (requires infra)
task test:cover         # Run tests with coverage
task generate           # Regenerate protobuf and SQLC code
task generate:plugin    # Install protoc-gen-entitystore
task lint               # Run go vet
task tidy               # Tidy go modules
task infra:up           # Start PostgreSQL via Docker Compose
task infra:down         # Stop PostgreSQL and remove volumes
task migrate:up         # Apply migrations via dbmate
task migrate:down       # Roll back the latest migration
task proto:login        # Log in to BSR (one-time)
task proto:push         # Lint and push proto module to BSR
```

### Running tests

Unit tests run without infrastructure:

```sh
task test
```

End-to-end tests require PostgreSQL:

```sh
task infra:up
task test:e2e
```

The e2e tests boot a real HTTP server backed by PostgreSQL, exercise every RPC, and clean up after themselves.

### Regenerating code

```sh
task generate
```

This runs `buf generate` (proto) and `sqlc generate` (SQL queries). Never edit files in `gen/` or `store/internal/dbgen/` manually.

## Partial updates

Entities have two layers of structure, each with its own update strategy:

1. **Envelope fields** (proto-defined): `confidence`, `tags`, etc.
2. **Data fields** (freeform JSON): the `bytes data` field holding JSONB

### Envelope fields: FieldMask (Google AIP-134)

Envelope fields use `google.protobuf.FieldMask` following the [Google AIP-134](https://google.aip.dev/134) standard. The client sends a mask listing which fields to apply. Fields not in the mask are left untouched.

```protobuf
import "google/protobuf/field_mask.proto";

message UpdateEntityRequest {
  string id = 1;
  bytes data = 2;
  double confidence = 3;
  google.protobuf.FieldMask update_mask = 4;
}
```

**Rules:**

| Scenario | `update_mask` | Field value | Result |
|---|---|---|---|
| Update a field | `["confidence"]` | `0.99` | Set confidence to 0.99 |
| Clear a field | `["confidence"]` | `0.0` (zero value) | Reset confidence to default |
| Skip a field | field not in mask | any | No change |
| Full replace | `update_mask` omitted/empty | all fields | Replace everything (backwards compatible) |

When `update_mask` is omitted or empty, the request behaves as a full replace -- identical to the current `UpdateEntity` behaviour. This keeps the API backwards compatible.

### Data fields: JSON merge patch (RFC 7396)

The `data` field contains freeform JSON. Since FieldMask cannot reach inside opaque bytes, data updates use [JSON Merge Patch](https://datatracker.ietf.org/doc/html/rfc7396) semantics. The client sends only the keys to change:

```
Existing data:   {"name": "Alice", "email": "old@example.com", "phone": "555-0100"}
Patch:           {"email": "new@example.com", "phone": null}
Result:          {"name": "Alice", "email": "new@example.com"}
```

**Rules:**

| Patch value | Result |
|---|---|
| `"email": "new@example.com"` | Set or overwrite the `email` key |
| `"phone": null` | Delete the `phone` key |
| key not in patch | Keep existing value unchanged |

This is implemented in PostgreSQL using JSONB operators, making it a single atomic operation.

### Tags: dedicated RPCs

Tags have their own RPCs for fine-grained control:

| RPC | Description |
|---|---|
| `SetTags` | Replace all tags |
| `AddTags` | Append tags (deduplicated) |
| `RemoveTag` | Remove a single tag |

### Combining both in a single request

A single `UpdateEntity` call can update envelope fields and merge data in one operation:

```json
{
  "id": "abc-123",
  "data": "{\"email\": \"new@example.com\", \"phone\": null}",
  "confidence": 0.99,
  "update_mask": {"paths": ["data", "confidence"]}
}
```

This sets confidence to 0.99, updates the email, and removes the phone field -- all atomically.

## Architecture

```
                    +-----------+
  Client ----RPC--->| Handler   |----> Connect-RPC endpoints
                    +-----------+
                         |
                         v
                    +-----------+
                    |   Store   |----> PostgreSQL + pgvector
                    +-----------+
                    /    |    |   \
                   v     v    v    v
              Anchors  Tokens  Embeddings  Relations
              (exact)  (fuzzy) (vector)    (graph)
```

Entities are stored with typed JSON data. Multiple search strategies (anchors, tokens, embeddings) enable flexible deduplication. Relationships form a directed graph between entities.

## License

Proprietary. All rights reserved.
