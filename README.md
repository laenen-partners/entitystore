# EntityStore

Pure Go library for entity storage, deduplication, and relationship management with PostgreSQL + pgvector backend. Includes `protoc-gen-entitystore`, a buf plugin that generates matching configs and LLM extraction schemas from proto annotations.

## Features

- **Entity CRUD** — create, update, merge, delete entities with typed proto data
- **Anchor matching** — O(1) deduplication via normalized field values (email, tax ID, etc.)
- **Token matching** — fuzzy blocking via tokenized field values with overlap scoring
- **Embedding search** — vector similarity search via pgvector (cosine distance)
- **Matching engine** — field-level scoring with Jaro-Winkler, Levenshtein, Token Jaccard, threshold-based decisions, and merge plans
- **LLM extraction schemas** — generate structured extraction schemas from proto annotations for use with Genkit or any LLM framework
- **Relationships** — directed edges between entities with typed proto data, confidence, and evidence
- **Tag filtering** — filter queries by arbitrary string tags
- **Provenance tracking** — full audit trail of entity extraction origin
- **Scoped stores** — tag-based multi-tenant filtering with auto-tagging on writes
- **Preconditions** — transactionally safe existence, uniqueness, and tag guards on `BatchWrite`
- **Transactions** — atomic multi-step operations via `TxStore` or shared `WithTx(pgx.Tx)`
- **Embedder interface** — batch-native `Embedder` interface compatible with `laenen-partners/embedder`
- **Code generation** — `protoc-gen-entitystore` buf plugin generates matching configs and extraction schemas from proto annotations

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
// Outbound relations from an entity (source → target)
rels, _ := es.GetRelationsFromEntity(ctx, personID)
fmt.Println(rels[0].DataType) // "employment.v1.Employment"
var emp employmentv1.Employment
rels[0].GetData(&emp)

// Inbound relations to an entity (source → target)
rels, _ = es.GetRelationsToEntity(ctx, companyID)

// All connected entities (both directions, deduplicated)
connected, _ := es.ConnectedEntities(ctx, personID)

// Connected entities filtered by entity type and relation type
companies, _ := es.FindConnectedByType(ctx, personID,
    "companies.v1.Company",        // target entity type
    []string{"works_at"},          // relation types (empty = all)
    nil,                           // optional QueryFilter for tag filtering
    100,                           // page size
    nil,                           // cursor (nil = first page)
)

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

## Project structure

```
entitystore.go               Library entry point: New(), EntityStore, re-exported types
options.go                   Options: WithPgStore()
cmd/protoc-gen-entitystore/  Buf plugin: proto annotations → matching configs + extraction schemas
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
