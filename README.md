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

Relations are directed edges between entities with optional typed proto data:

```go
// Write — Data accepts proto.Message, DataType derived automatically
es.BatchWrite(ctx, []entitystore.BatchWriteOp{
    {UpsertRelation: &entitystore.UpsertRelationOp{
        SourceID:     personID,
        TargetID:     companyID,
        RelationType: "works_at",
        Confidence:   0.95,
        Data:         &employmentv1.Employment{Role: "VP of Product"},
    }},
})

// Read — unmarshal typed relation data
rels, _ := es.GetRelationsFromEntity(ctx, personID)
fmt.Println(rels[0].DataType) // "employment.v1.Employment"
var emp employmentv1.Employment
rels[0].GetData(&emp)
```

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
- [Example protos](examples/proto/) — fully annotated Person, Invoice, JobPosting
- [Example Go code](examples/) — codegen, matching, and store usage patterns

## License

Proprietary. All rights reserved.
