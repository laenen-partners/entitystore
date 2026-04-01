# EntityStore

Pure Go library for entity storage, deduplication, and relationship management with PostgreSQL + pgvector backend. Includes `protoc-gen-entitystore`, a buf plugin that generates matching configs and LLM extraction schemas from proto annotations.

## Features

- **Entity CRUD** — create, update, merge, soft-delete entities with typed proto data
- **Anchor matching** — O(1) deduplication via normalized field values (email, tax ID, etc.)
- **Token matching** — fuzzy blocking via tokenized field values with overlap scoring
- **Embedding search** — vector similarity search via pgvector (cosine distance)
- **Matching engine** — field-level scoring with Jaro-Winkler, Levenshtein, Token Jaccard, threshold-based decisions, and merge plans
- **Graph traversal** — multi-hop exploration via recursive CTE with direction, type, confidence, and tag filtering
- **LLM extraction schemas** — generate structured extraction schemas from proto annotations for use with Genkit or any LLM framework
- **Relationships** — directed edges between entities with typed proto data, confidence, and evidence
- **Tag filtering** — filter queries by arbitrary string tags with AND/OR semantics
- **Event store** — proto-first audit trail with automatic lifecycle events and custom domain events
- **Soft deletes** — `DeleteEntity` preserves data for audit; `HardDeleteEntity` for permanent removal
- **Scoped stores** — tag-based multi-tenant filtering with auto-tagging on writes
- **Preconditions** — transactionally safe existence, uniqueness, and tag guards on `BatchWrite`
- **Transactions** — atomic read + write operations via `TxStore` or shared `WithTx(pgx.Tx)`
- **Stats** — aggregate counts by entity type, relation type, soft-deleted; `Stats()` for full overview
- **EntityStorer interface** — for dependency injection and testing; satisfied by both `EntityStore` and `ScopedStore`
- **Input validation** — tag length/count limits, relation type validation, batch size cap (1000)
- **Structured logging** — `WithLogger(*slog.Logger)` for debug-level operation tracing
- **Code generation** — `protoc-gen-entitystore` generates matching configs, extraction schemas, typed token/anchor extractors, and `WriteOp` builders from proto annotations

## Quick start

```go
import (
    "github.com/jackc/pgx/v5/pgxpool"
    "github.com/laenen-partners/entitystore"
    "github.com/laenen-partners/entitystore/matching"
)

// Setup
pool, _ := pgxpool.New(ctx, connString)
entitystore.Migrate(ctx, pool)
es, _ := entitystore.New(entitystore.WithPgStore(pool))
defer es.Close()

// Write — Data accepts proto.Message, EntityType derived automatically
results, _ := es.BatchWrite(ctx, []entitystore.BatchWriteOp{
    {WriteEntity: &entitystore.WriteEntityOp{
        Action:     entitystore.WriteActionCreate,
        Data:       &personv1.Person{Email: "alice@example.com", FullName: "Alice Johnson"},
        Confidence: 0.95,
        Anchors:    []entitystore.AnchorQuery{{Field: "email", Value: "alice@example.com"}},
    }},
})

// Read — unmarshal back into proto
entity, _ := es.GetEntity(ctx, results[0].Entity.ID)
var person personv1.Person
entity.GetData(&person)

// Find by anchor
matches, _ := es.FindByAnchors(ctx, "persons.v1.Person", anchors, nil)

// Match — full entity resolution pipeline
m := matching.NewMatcher(personMatchConfig, es)
decision, _ := m.Match(ctx, extractedData)
// decision.Action: "create", "update", "review", or "conflict"
```

## Defining entity types with proto annotations

Annotate proto messages to define matching behaviour and LLM extraction schemas:

```protobuf
syntax = "proto3";
package entities.v1;

import "entitystore/v1/options.proto";

message Person {
  option (entitystore.v1.message) = {
    match_thresholds: {auto_match: 0.85, review_zone: 0.60}
    extraction_prompt: "Extract person details from the provided text."
    extraction_display_name: "Person"
  };

  // Primary email address.
  string email = 1 [(entitystore.v1.field) = {
    anchor: true
    similarity: SIMILARITY_FUNCTION_EXACT
    weight: 0.30
    normalizer: NORMALIZER_LOWERCASE_TRIM
    extraction_hint: "Extract the primary email, not CC addresses"
    examples: ["john@example.com"]
  }];

  // Full legal name of the person.
  string full_name = 2 [(entitystore.v1.field) = {
    similarity: SIMILARITY_FUNCTION_JARO_WINKLER
    weight: 0.40
    embed: true
    token_field: true
  }];

  // Phone number.
  string phone = 3 [(entitystore.v1.field) = {
    similarity: SIMILARITY_FUNCTION_EXACT
    weight: 0.15
    normalizer: NORMALIZER_PHONE_NORMALIZE
  }];

  // Date of birth.
  string date_of_birth = 4 [(entitystore.v1.field) = {
    similarity: SIMILARITY_FUNCTION_EXACT
    weight: 0.15
  }];
}
```

### Setup in your project

**1. Add the BSR dependency** to `buf.yaml`:

```yaml
version: v2
modules:
  - path: proto
deps:
  - buf.build/laenen-partners/entitystore
```

**2. Configure `buf.gen.yaml`:**

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

**3. Generate and register:**

```sh
buf dep update && buf generate
```

This generates two functions per annotated message:

- `PersonMatchConfig()` → `matching.EntityMatchConfig` for dedup and matching
- `PersonExtractionSchema()` → `extraction.ExtractionSchema` for LLM extraction

```go
// Register matching configs
configs := matching.NewMatchConfigRegistry()
configs.Register(entitiesv1.PersonMatchConfig())

// Register extraction schemas (for LLM entity extraction)
schemas := extraction.NewExtractionSchemaRegistry()
schemas.Register(entitiesv1.PersonExtractionSchema())

// Create matcher for entity resolution
matcher := matching.NewMatcher(entitiesv1.PersonMatchConfig(), es,
    matching.WithEmbedder(embedFunc),
)
decision, _ := matcher.Match(ctx, extractedJSON)
```

### Annotation reference

**Field options** `(entitystore.v1.field)`:

| Option | Type | Description |
|---|---|---|
| `anchor` | bool | Identity field for O(1) dedup lookup |
| `similarity` | enum | `EXACT`, `JARO_WINKLER`, `LEVENSHTEIN`, `TOKEN_JACCARD` |
| `weight` | float | Contribution to composite score (0.0–1.0, sum ~1.0) |
| `conflict_strategy` | enum | `FLAG_FOR_REVIEW`, `LATEST_WINS`, `HIGHEST_CONFIDENCE` |
| `normalizer` | enum | `LOWERCASE_TRIM`, `PHONE_NORMALIZE` |
| `embed` | bool | Include in embedding vector input |
| `token_field` | bool | Tokenize for fuzzy blocking |
| `description` | string | Override field description for LLM extraction (default: proto comment) |
| `extraction_hint` | string | Directive instruction for LLM extraction |
| `extract` | bool | Include in extraction output (default: true) |
| `examples` | repeated string | Example values for few-shot grounding |

**Message options** `(entitystore.v1.message)`:

| Option | Type | Description |
|---|---|---|
| `match_thresholds` | message | `auto_match` and `review_zone` score boundaries |
| `composite_anchors` | repeated | Multi-field identity keys |
| `allowed_relations` | repeated string | Restrict relation types |
| `extraction_prompt` | string | System-level LLM extraction instruction |
| `extraction_instructions` | string | Additional extraction context |
| `extraction_display_name` | string | Human-friendly entity name for prompts |

## Matching engine

The `Matcher` orchestrates the full entity resolution pipeline:

```
Extracted Entity → Anchor Lookup → Fuzzy Candidates → Field Scoring → Decision
                      ↓                  ↓                  ↓            ↓
                  Short-circuit      Tokens +          Weighted      Create /
                  (confidence 1.0)   Embeddings        composite     Update /
                                                       score         Review /
                                                                     Conflict
```

- **Anchor short-circuit** — exact anchor match returns immediately (confidence 1.0)
- **Fuzzy retrieval** — token overlap + embedding similarity for candidate generation
- **Field-level scoring** — Jaro-Winkler, Levenshtein, Token Jaccard, Exact with configurable weights
- **Threshold decisions** — auto-match (≥ `auto_match`), review zone, or create new entity
- **Merge plans** — per-field conflict resolution using configured strategies

## Scoped stores

`ScopedStore` wraps an `EntityStore` with tag-based read/write filtering for multi-tenant scoping. All reads are filtered, all creates are auto-tagged.

```go
scoped := es.Scoped(entitystore.ScopeConfig{
    RequireTags: []string{"ws:acme"},          // reads: entity must have ALL these tags
    ExcludeTag:  "restricted",                 // reads: hide entities with this tag
    UnlessTags:  []string{"admin"},            // reads: exempt from ExcludeTag
    AutoTags:    []string{"ws:acme"},          // writes: auto-added on create
})

// All reads are now scoped — only entities with "ws:acme" tag are visible.
entities, _ := scoped.FindByAnchors(ctx, entityType, anchors, nil)

// Creates are auto-tagged — no need to manually add workspace tags.
scoped.BatchWrite(ctx, []entitystore.BatchWriteOp{
    {WriteEntity: &entitystore.WriteEntityOp{
        Action: entitystore.WriteActionCreate,
        Data:   &personv1.Person{Email: "alice@acme.com"},
        // Tags will include "ws:acme" automatically
    }},
})
```

### ScopeConfig options

| Field | Type | Description |
|---|---|---|
| `RequireTags` | `[]string` | Reads: entity must have ALL of these tags (AND) |
| `ExcludeTag` | `string` | Reads: hide entities with this tag |
| `UnlessTags` | `[]string` | Reads: exempt from ExcludeTag if entity has any of these |
| `AutoTags` | `[]string` | Writes: appended to Tags on `WriteActionCreate` only |

### Common patterns

**Workspace scoping** — isolate data per workspace:

```go
scoped := es.Scoped(entitystore.ScopeConfig{
    RequireTags: []string{tags.Workspace(workspaceID)},
    ExcludeTag:  tags.Restricted(),
    UnlessTags:  userAccessGroupTags,
    AutoTags:    []string{tags.Workspace(workspaceID)},
})
```

**Tenant scoping** — simple tenant isolation:

```go
scoped := es.Scoped(entitystore.ScopeConfig{
    RequireTags: []string{tags.Tenant(tenantID)},
    AutoTags:    []string{tags.Tenant(tenantID)},
})
```

**Read-only scope** — no auto-tagging, just filtering:

```go
scoped := es.Scoped(entitystore.ScopeConfig{
    RequireTags: []string{tags.Workspace(workspaceID)},
})
```

`GetEntity` returns `ErrAccessDenied` for entities outside the scope. Callers can choose to disguise this as "not found" or surface it directly.

Scoped stores support `WithTx` — the scope config is preserved across transactions.

## Preconditions

`BatchWrite` supports optional preconditions that are evaluated **inside** the transaction before each operation is applied. If any precondition fails, the entire batch is rolled back atomically.

This eliminates TOCTOU (time-of-check-time-of-use) gaps — no more "check entity exists, then write" with a race window in between.

```go
_, err := es.BatchWrite(ctx, []entitystore.BatchWriteOp{
    {
        WriteEntity: &entitystore.WriteEntityOp{
            Action:          entitystore.WriteActionUpdate,
            MatchedEntityID: productID,
            Data:            updatedProduct,
            Confidence:      0.95,
        },
        // Ensure the referenced ruleset exists and is not disabled.
        PreConditions: []entitystore.PreCondition{
            {
                EntityType:   "rulesets.v1.Ruleset",
                Anchors:      []entitystore.AnchorQuery{{Field: "ruleset_id", Value: rulesetID}},
                MustExist:    true,
                TagForbidden: "disabled:true",
            },
        },
    },
})
```

### Precondition options

| Field | Type | Description |
|---|---|---|
| `EntityType` | string | Entity type to look up |
| `Anchors` | `[]AnchorQuery` | Anchor values to search for |
| `MustExist` | bool | Fail if no entity matches |
| `MustNotExist` | bool | Fail if any entity matches (uniqueness guard) |
| `TagRequired` | string | Matched entity must carry this tag |
| `TagForbidden` | string | Matched entity must NOT carry this tag |

`MustExist` and `MustNotExist` are mutually exclusive.

### Error handling

Precondition failures return a `*PreConditionError` that can be inspected with `errors.As`:

```go
var pcErr *entitystore.PreConditionError
if errors.As(err, &pcErr) {
    fmt.Printf("op %d failed: %s for %s\n", pcErr.OpIndex, pcErr.Violation, pcErr.Condition.EntityType)
    // pcErr.Violation is one of: "not_found", "already_exists", "tag_required", "tag_forbidden"
}
```

### Common patterns

**Referential integrity** — ensure a related entity exists before writing:

```go
PreConditions: []entitystore.PreCondition{
    {EntityType: "companies.v1.Company", Anchors: companyAnchors, MustExist: true},
}
```

**Uniqueness guard** — prevent duplicate creation:

```go
PreConditions: []entitystore.PreCondition{
    {EntityType: "invoices.v1.Invoice", Anchors: invoiceAnchors, MustNotExist: true},
}
```

**Status guard** — check entity state via tags:

```go
PreConditions: []entitystore.PreCondition{
    {EntityType: "rulesets.v1.Ruleset", Anchors: rulesetAnchors, MustExist: true, TagForbidden: "disabled:true"},
}
```

## Shared transactions

When multiple stores share the same PostgreSQL database, use `WithTx` for atomic cross-store operations:

```go
tx, _ := pool.Begin(ctx)
defer tx.Rollback(ctx)

esTx := entityStore.WithTx(tx)
cbTx := cashbook.WithTx(tx)  // same tx, different schema

esTx.BatchWrite(ctx, entityOps...)     // schema: public
cbTx.RecordTransaction(ctx, txOp...)   // schema: cashbook

tx.Commit(ctx)  // atomic: both or neither
```

## Embedder interface

The `matching.Embedder` interface is compatible with [`laenen-partners/embedder`](https://github.com/laenen-partners/embedder):

```go
import "github.com/laenen-partners/embedder"

emb := embedder.New(ctx)  // satisfies matching.Embedder

// Extract text from fields marked embed: true
text := matching.TextToEmbed(entityData, config.EmbedFields)

// Compute embedding
vecs, _ := emb.Embed(ctx, []string{text})

// Pass to Matcher for candidate retrieval
matcher := matching.NewMatcher(config, store, matching.WithEmbedder(emb))

// Or store directly
op.Embedding = vecs[0]
```

## Relationships

Relations are directed edges between entities with optional typed proto data.

### Writing relations

```go
// Upsert — Data accepts proto.Message, DataType derived automatically
es.BatchWrite(ctx, []entitystore.BatchWriteOp{
    {UpsertRelation: &entitystore.UpsertRelationOp{
        SourceID:     personID,
        TargetID:     companyID,
        RelationType: "works_at",
        Confidence:   0.95,
        Evidence:     "Mentioned in invoice header",
        Data:         &employmentv1.Employment{Role: "VP of Product"},
    }},
})

// Update typed data on an existing relation
es.UpdateRelationData(ctx, personID, companyID, "works_at",
    &employmentv1.Employment{Role: "CTO"})

// Delete a specific relation
es.DeleteRelationByKey(ctx, personID, companyID, "works_at")
```

### Querying relations

```go
// Outbound relations from an entity (source → target), paginated
rels, _ := es.GetRelationsFromEntity(ctx, personID, 0, nil) // 0 = default page size, nil = first page
fmt.Println(rels[0].DataType) // "employment.v1.Employment"
var emp employmentv1.Employment
rels[0].GetData(&emp)

// Inbound relations to an entity (source → target)
rels, _ = es.GetRelationsToEntity(ctx, companyID, 100, nil)

// All connected entities (both directions, deduplicated)
connected, _ := es.ConnectedEntities(ctx, personID)

// Connected entities filtered by entity type and relation type
companies, _ := es.FindConnectedByType(ctx, personID, &entitystore.FindConnectedOpts{
    EntityType:    "companies.v1.Company",
    RelationTypes: []string{"works_at"},
})

// All entities participating in a relation type
employees, _ := es.FindEntitiesByRelation(ctx,
    "persons.v1.Person",           // entity type
    "works_at",                    // relation type
    nil,                           // optional QueryFilter
)
```

### Graph traversal

`Traverse` performs multi-hop exploration from a starting entity using a single PostgreSQL recursive CTE:

```go
results, _ := es.Traverse(ctx, personID, &entitystore.TraverseOpts{
    MaxDepth:      3,                              // hops (default 2, max 10)
    Direction:     entitystore.DirectionBoth,       // or DirectionOutbound / DirectionInbound
    RelationTypes: []string{"works_at", "knows"},  // filter edges (empty = all)
    EntityType:    "persons.v1.Person",            // filter discovered entities (empty = all)
    MinConfidence: 0.5,                            // skip low-confidence edges
    Filter:        &entitystore.QueryFilter{        // tag filtering on discovered entities
        Tags: []string{"ws:acme"},
    },
})

for _, r := range results {
    fmt.Printf("depth %d: %s (%s)\n", r.Depth, r.Entity.ID, r.Entity.EntityType)
    for _, edge := range r.Path {
        fmt.Printf("  via %s: %s → %s (%.2f)\n",
            edge.RelationType, edge.FromID, edge.ToID, edge.Confidence)
    }
}
```

Key properties:
- **Single round-trip** — recursive CTE handles all hops server-side
- **Cycle-safe** — visited array prevents infinite loops
- **Scope-aware** — `ScopedStore.Traverse` stops traversal at scope boundaries
- **Transitive filtering** — filtered-out entities block the path to entities beyond them

## Events

Every write operation automatically emits a standard lifecycle event. Callers can attach custom domain events on top.

### Automatic lifecycle events

| Operation | Event emitted |
|-----------|--------------|
| `WriteActionCreate` | `entitystore.events.EntityCreated` |
| `WriteActionUpdate` | `entitystore.events.EntityUpdated` |
| `WriteActionMerge` | `entitystore.events.EntityMerged` |
| `DeleteEntity` | `entitystore.events.EntityDeleted` |
| `HardDeleteEntity` | `entitystore.events.EntityHardDeleted` |
| `UpsertRelation` | `entitystore.events.RelationCreated` |
| `DeleteRelationByKey` | `entitystore.events.RelationDeleted` |

### Custom domain events

```go
results, _ := es.BatchWrite(ctx, []entitystore.BatchWriteOp{
    {WriteEntity: &entitystore.WriteEntityOp{
        Action: entitystore.WriteActionCreate,
        Data:   &personv1.Person{Email: "alice@example.com"},
        Events: []proto.Message{
            &hiringv1.CandidateSourced{Source: "linkedin", Recruiter: "bob@acme.com"},
        },
    }},
})
// entity_events now contains:
//   1. entitystore.events.EntityCreated  (automatic)
//   2. hiring.CandidateSourced           (custom)
```

Or via the option function with generated WriteOps:

```go
op := jobsv1.JobPostingWriteOp(posting, entitystore.WriteActionCreate,
    entitystore.WithTags("ws:acme"),
    entitystore.WithEvents(&pipelinev1.ExtractionCompleted{Source: "email:msg-42"}),
)
```

### Querying events

```go
// All events for an entity (newest first)
events, _ := es.GetEventsForEntity(ctx, entityID, nil)

// Filtered by event type and time
events, _ = es.GetEventsForEntity(ctx, entityID, &entitystore.EventQueryOpts{
    EventTypes: []string{"entitystore.events.EntityCreated", "entitystore.events.EntityUpdated"},
    Since:      time.Now().Add(-24 * time.Hour),
    Limit:      50,
})

for _, e := range events {
    fmt.Printf("%s at %s\n", e.EventType, e.OccurredAt)
    switch evt := e.Payload.(type) {
    case *eventsv1.EntityCreated:
        fmt.Printf("  Created: %s (confidence %.2f)\n", evt.EntityType, evt.Confidence)
    }
}
```

### Design

- Events are proto messages stored as JSONB — schema-checked, auto-marshaled
- `payload_type` is the full proto name (e.g. `entitystore.events.v1.EntityCreated`) for deserialization
- `event_type` strips the version segment (e.g. `entitystore.events.EntityCreated`) for routing stability
- Event IDs are UUIDv7 (time-sortable) — `ORDER BY id` = `ORDER BY occurred_at`
- Table is partitioned by `occurred_at` with an outbox-ready `published_at` column
- Events carry a tag snapshot from write time (enables scoped queries without joins)

## Event Consumers

Named consumers independently process the event stream with cursor-based progress tracking. Each consumer has its own position and lock — multiple consumers run simultaneously without conflict.

```go
// Realtime consumer — wakes instantly on new events via LISTEN/NOTIFY.
notifier := es.NewConsumer(func(ctx context.Context, events []entitystore.Event) error {
    for _, evt := range events {
        pubsub.Publish(evt.EventType, evt)
    }
    return nil
}, entitystore.ConsumerConfig{
    Name:         "notifier",
    PollInterval: 5 * time.Second, // fallback if NOTIFY missed
})
go notifier.RunRealtime(ctx)

// Polling consumer — for heavyweight processing (embeddings, projections).
projector := es.NewConsumer(projectFunc, entitystore.ConsumerConfig{
    Name:         "projector",
    BatchSize:    50,
    PollInterval: 5 * time.Second,
})
go projector.Run(ctx)
```

Key properties:
- **Named cursors** — each consumer tracks its own position via `entity_event_consumers` table
- **LISTEN/NOTIFY** — `RunRealtime` wakes instantly on new events, with polling fallback
- **Single leader per consumer** — TTL-based lock ensures only one instance runs per name
- **At-least-once delivery** — cursor advances only after `ConsumerFunc` returns nil
- **Idempotent** — consumers must handle redelivery on error/restart

## Health

```go
status, _ := es.Health(ctx)
fmt.Printf("DB OK: %v (latency %v)\n", status.DB.OK, status.DB.Latency)
fmt.Printf("Pool: %d/%d connections\n", status.DB.TotalConns, status.DB.MaxConns)
fmt.Printf("Unpublished events: %d\n", status.Events.UnpublishedCount)
for _, c := range status.Consumers {
    fmt.Printf("Consumer %s: lag %s, holder %s\n", c.Name, c.Lag, c.HolderID)
}

// Wire to HTTP for k8s probes:
http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
    s, _ := es.Health(r.Context())
    if !s.Healthy() { w.WriteHeader(503) }
    json.NewEncoder(w).Encode(s)
})
```

## Soft deletes

`DeleteEntity` sets `deleted_at` instead of removing the row — data is preserved for audit. All read queries automatically filter deleted entities. Use `HardDeleteEntity` for permanent cleanup.

```go
es.DeleteEntity(ctx, id)     // soft delete — data preserved
es.HardDeleteEntity(ctx, id) // permanent removal with CASCADE

// Soft-deleted entities are invisible to all reads, traversals, and scoped stores.
```

## Stats

Get a view of what's in the database:

```go
stats, _ := es.Stats(ctx)
fmt.Printf("Entities: %d, Relations: %d, Soft-deleted: %d\n",
    stats.TotalEntities, stats.TotalRelations, stats.SoftDeleted)

// Breakdown by type (GROUP BY with counts)
for _, tc := range stats.EntityTypes {
    fmt.Printf("  %s: %d\n", tc.Type, tc.Count)
}
for _, tc := range stats.RelationTypes {
    fmt.Printf("  %s: %d\n", tc.Type, tc.Count)
}

// Individual counts
count, _ := es.CountEntitiesByType(ctx, "persons.v1.Person")
relCount, _ := es.CountRelationsForEntity(ctx, entityID)
```

## Explorer UI

A visual debug tool for browsing entities, relations, and graph traversals. Embed it in any service:

```go
import "github.com/laenen-partners/entitystore/ui/explorer"

// Standalone server (blocks until interrupted):
explorer.Run(explorer.Config{
    Store: es,    // any EntityStorer (EntityStore or ScopedStore)
    Port:  3336,  // default 3336
})

// Or run in background:
explorer.RunInBackground(ctx, explorer.Config{Store: es})

// Or mount fragment handlers on an existing router:
explorer.Mount(r, es)
```

### Standalone binary

Point it at any database with entitystore tables:

```sh
# Install
go install github.com/laenen-partners/entitystore/cmd/entitystore-explorer@latest

# Run against your database
entitystore-explorer -dsn "postgres://user:pass@host:5432/mydb?sslmode=disable"
entitystore-explorer -port 3336  # uses DATABASE_URL env var
```

### Embed in your service

```go
import "github.com/laenen-partners/entitystore/ui/explorer"

explorer.Run(explorer.Config{Store: es, Port: 3336})           // standalone
explorer.RunInBackground(ctx, explorer.Config{Store: es})       // goroutine
explorer.Mount(r, es)                                           // existing router
```

### Features
- **Search** — fuzzy trigram search on display names with 300ms debounce, falls back to token search
- **Entity detail** — opens in a drawer with JSON viewer, anchors, tags, and clickable relations
- **Relations** — display names resolved via single Traverse call, click to navigate
- **Stats** — entity/relation type counts, soft-deleted count

The standalone showcase includes seed data for demo purposes:

```sh
docker compose up -d    # start pgvector
task showcase           # run explorer at http://localhost:3336
```

## Search

`Search()` performs fuzzy matching on entity display names using PostgreSQL trigram similarity (`pg_trgm`), falling back to token search:

```go
results, _ := es.Search(ctx, "ali", 20, nil)  // finds "Alice Dupont"
results, _ := es.Search(ctx, "tech", 20, nil) // finds "TechCorp"
```

Partial matches, case-insensitive, ranked by similarity. Requires the `pg_trgm` extension (auto-migrated).

## EntityStorer interface

Both `EntityStore` and `ScopedStore` satisfy `EntityStorer` — use it for dependency injection and testing:

```go
type MyService struct {
    es entitystore.EntityStorer  // accepts EntityStore or ScopedStore
}

// In tests, use a mock that satisfies EntityStorer.
```

## Project structure

```
entitystore.go               Library entry point: New(), EntityStore, re-exported types
interface.go                 EntityStorer interface (EntityStore + ScopedStore)
options.go                   Options: WithPgStore(), WithLogger()
ui/                          Fragment handlers for explorer UI
ui/explorer/                 Embeddable explorer server (Run, Mount, RunInBackground)
ui/cmd/showcase/             Standalone showcase with seed data
cmd/protoc-gen-entitystore/  Buf plugin: proto annotations → matching configs + extraction schemas
cmd/entitystore-explorer/    Standalone explorer binary (go install)
proto/entitystore/v1/        Proto annotation definitions (options.proto)
proto/entitystore/events/v1/ Standard lifecycle event protos
gen/                         Generated protobuf code (do not edit)
matching/                    Domain logic: matcher, similarity, anchors, tokens, embeddings, normalizers
extraction/                  LLM extraction schema types and registry
store/                       PostgreSQL persistence layer (SQLC)
store/db/migrations          SQL migrations (embedded at build time)
store/db/queries             SQLC query definitions
store/internal/dbgen         Generated SQLC code (do not edit)
examples/                    Comprehensive Go examples for all features
examples/proto/              Example proto definitions with full annotations
docs/                        Tutorials, ADRs, and migration guides
```

## Development

```sh
task generate         # buf generate + sqlc generate
task generate:plugin  # install protoc-gen-entitystore
task lint             # go vet ./...
task test             # go test -v -count=1 ./store/... (uses testcontainers)
task test:cover       # tests with coverage
task tidy             # go mod tidy
```

Tests use [testcontainers](https://testcontainers.com) with `pgvector/pgvector:pg17` — Docker is required.

## Documentation

- [Entity Extraction Tutorial](docs/tutorial-entity-extraction.md) — full pipeline with Genkit
- [Migration Guide v0.20→v1.0](docs/migration-v1.0.md) — soft deletes, pagination, EntityStorer interface, stats
- [Migration Guide v0.17→v0.18](docs/migration-v0.18.md) — generated Tokens, Anchors, WriteOp, option helpers
- [Migration Guide v0.7→v0.8](docs/migration-v0.8.md) — proto-native write API migration
- [ADR-001: LLM Extraction Schemas](docs/adr/001-llm-entity-extraction-schema-generation.md)
- [ADR-002: Matching Engine](docs/adr/002-matching-engine.md)
- [ADR-005: Preconditions](docs/adr/005_pre-conditions.md)
- [ADR-006: Scoped Store](docs/adr/006_scoped-store.md)
- [ADR-007: Graph Traversal](docs/adr/007_traverse.md)
- [Example protos](examples/proto/) — fully annotated Person, Invoice, JobPosting
- [Example Go code](examples/) — codegen, matching, and store usage patterns

## License

Proprietary. All rights reserved.
