# Changelog

All notable changes to EntityStore are documented in this file.

Format follows [Keep a Changelog](https://keepachangelog.com/). Entries are written for consumers — what you need to know when upgrading.

## [Unreleased]

### Breaking
- **Publisher removed** — `NewPublisher`, `PublishFunc`, `PublisherConfig`, `Publisher`, `PublisherHealth` all removed. Replaced by named consumers (see below).
- **Optimistic locking on updates/merges** — `WriteActionUpdate` and `WriteActionMerge` now require `Version` field on `WriteEntityOp`. Pass the version from the entity you read. Returns `ErrConflict` if the entity was modified by another writer since you read it. See "Optimistic Locking" section in README.

### Added
- **Optimistic locking** — `version` column on entities (starts at 0, increments on every update/merge). Prevents lost updates when multiple writers modify the same entity concurrently. New `ErrConflict` sentinel error and `WithVersion(v)` write option.
- **Named event consumers** — `NewConsumer(fn, cfg)` with cursor-based progress tracking per consumer name. `Run(ctx)` for polling, `RunRealtime(ctx)` for LISTEN/NOTIFY + polling fallback. Each consumer has its own lock and cursor in `entity_event_consumers` table.
- **LISTEN/NOTIFY trigger** — `entity_events` table fires a Postgres notification on each insert, enabling sub-second latency for realtime consumers.
- **Consumer health** — `Health()` now reports per-consumer status (name, lag, lock holder) instead of publisher status.
- **Soft delete guard** — UPDATE and MERGE queries now include `AND deleted_at IS NULL`. Soft-deleted entities cannot be mutated.
- **Consumer resilience** — exponential backoff with jitter (`InitialBackoff`, `MaxBackoff`), dead letter table (`MaxRetries`, `OnDeadLetter` callback), replay (`ReplayDeadLetters`), purge (`PurgeDeadLetters`), health state (`healthy`/`degraded`/`failing`). All zero-value defaults preserve previous behaviour. Failure state persisted to DB across restarts.

## [v0.25.0] - 2026-03-26

### Breaking
- **`GetEntitiesByTypeFiltered` removed** — merged into `GetEntitiesByType` which now accepts an optional `*QueryFilter` as the last parameter. Pass `nil` for no filtering.
- **`FindConnectedByType`** takes `*FindConnectedOpts` struct instead of 7 positional parameters.
- **`Tx()`** returns `*entitystore.TxStore` (root package) instead of `*store.TxStore`. Consumers no longer need to import the `store` package for transactions.
- **Provenance removed** — `ProvenanceEntry`, `WithProvenance`, `Provenance()`, `GetProvenanceForEntity` all removed. Replaced by the event store (see below).

### Added
- **Event store** — proto-first audit trail replaces provenance. Every write operation automatically emits a standard lifecycle event (`EntityCreated`, `EntityUpdated`, `EntityMerged`, `EntityDeleted`, `EntityHardDeleted`, `RelationCreated`, `RelationDeleted`). Callers attach custom domain events via `WithEvents(...proto.Message)` or the `Events` field on write ops. Events are proto messages stored as JSONB with UUIDv7 IDs (time-sortable). Table is partitioned by `occurred_at` with outbox-ready `published_at` column.
- **`GetEventsForEntity(ctx, entityID, opts)`** — query events with optional filtering by event type, time range, and limit. Added to `EntityStore`, `ScopedStore`, `TxStore`, and `EntityStorer` interface.
- **`WithEvents(...proto.Message)`** — write option to attach custom domain events to entity/relation writes.
- **Event type derivation** — `payload_type` keeps the full proto name (e.g. `entitystore.events.v1.EntityCreated`); `event_type` strips the version segment for routing stability (e.g. `entitystore.events.EntityCreated`).
- **Standard event protos** — `proto/entitystore/events/v1/events.proto` with 8 lifecycle event messages.
- **Health API** — `es.Health(ctx)` returns `HealthStatus` with DB health (ping latency, pool stats), event activity (last event, unpublished count), and consumer status. `HealthError(ctx)` for liveness probes.
- `FindConnectedOpts` struct for cleaner `FindConnectedByType` calls.
- `TxStore` wrapper in root package with full read + write surface (GetEntity, FindByAnchors, GetRelationsFrom/To, WriteEntity, UpsertRelation, DeleteRelationByKey, UpdateRelationData, GetEventsForEntity).
- Expanded godoc package comment with quick-start example and pointers to key types.
- Migration: `entity_events` (partitioned, replaces `entity_provenance`).

- **Explorer UI** — embeddable visual debug tool for browsing entities, relations, and graph traversals. `explorer.Run(Config{Store: es})` for standalone, `explorer.Mount(r, es)` for existing routers, `explorer.RunInBackground(ctx, cfg)` for goroutine. Pages: search (debounced trigram), stats (server-rendered), entities, entity detail drawer with JSON viewer, anchors, and clickable relations.
- **`Search(ctx, query, maxResults, filter)`** — fuzzy trigram search on `display_name` via `pg_trgm` extension, ranked by similarity, falls back to token search. Partial matches ("ali" → "Alice Dupont").
- **`display_name` column** on entities — set via `WriteEntityOp.DisplayName` or `WithDisplayName()` option. Auto-set by generated `WriteOp` when `display_fields` proto annotation is present.
- **`display_fields` proto annotation** on `MessageOptions` — lists proto field names to derive display name from. Generates `{Entity}DisplayName(msg)` function.
- **`GetAnchorsForEntity(ctx, entityID)`** — returns stored anchor field+value pairs. Added to Store, EntityStore, ScopedStore, EntityStorer.
- **`pg_trgm` migration** — adds extension and GIN trigram index on `display_name`.
- **`StoredAnchor`** type re-exported from root package.
- Entity detail drawer shows relations with resolved display names (via single Traverse call).

### Changed
- ADR-001, ADR-002 status updated to "Implemented". ADR-003 updated to "Partially implemented".
- `matching.BuildAnchors`, `matching.BuildTokens`, `matching.TextToEmbed` unexported (use generated functions).
- `FindEntitiesByRelation` now has internal LIMIT 1000.
- Explorer search handler uses `Search()` instead of manual anchor+token lookup.

See [Migration Guide v0.20→v1.0](docs/migration-v1.0.md).

## [v0.20.0] - 2026-03-24

### Added
- **Soft deletes** — `DeleteEntity` now sets `deleted_at` instead of removing the row. All read queries, traversals, and scoped stores automatically filter soft-deleted entities.
- `HardDeleteEntity` for permanent removal with CASCADE.
- **TxStore read methods** — `GetEntity`, `FindByAnchors`, `GetRelationsFromEntity`, `GetRelationsToEntity` now available within transactions for read-modify-write patterns.
- **Stats queries** — `Stats()` returns aggregate `StoreStats` with `TotalEntities`, `TotalRelations`, `SoftDeleted`, `EntityTypes` (GROUP BY), `RelationTypes` (GROUP BY).
- Individual count methods: `CountEntitiesByType`, `CountAllEntities`, `CountAllRelations`, `CountRelationsForEntity`, `CountSoftDeleted`, `CountEntityTypes`, `CountRelationTypes`.
- **Benchmark tests** — 9 benchmarks covering BatchWrite, FindByAnchors, GetEntity, Traverse, ConnectedEntities, GetRelations, FindByTokens, Stats.
- Migration 4: `deleted_at` column on entities with partial index.

## [v0.19.0] - 2026-03-24

### Added
- **`EntityStorer` interface** — satisfied by both `EntityStore` and `ScopedStore`. Use for dependency injection and testing.
- **`GetByAnchor`** convenience method — single-anchor lookup returning `StoredEntity` or `ErrNotFound`.
- **`ErrNotFound`** sentinel error for `GetByAnchor` misses.
- **`WithLogger(*slog.Logger)`** option — structured debug logging for `FindByAnchors`, `BatchWrite`, `Traverse`.
- **`MaxBatchSize = 1000`** — `BatchWrite` rejects batches exceeding this limit.
- **Input validation** — tags (max 255 chars, max 100 per entity, non-empty), relation types (max 255 chars, non-empty). Applied in `BatchWrite`, `SetTags`, `AddTags`, `DeleteRelationByKey`, `UpdateRelationData`.
- Migration 3: composite index on `(target_id, relation_type)` and partial index on `source_urn` for entity_relations.

### Breaking
- **`GetRelationsFromEntity`/`GetRelationsToEntity`** now require `pageSize int32` and `cursor *time.Time` parameters. Pass `0, nil` for previous behavior (default 1000 limit).
- **`ConnectedEntities`** now uses a single UNION query with LIMIT 1000 instead of two unbounded queries.

See [Migration Guide v0.20→v1.0](docs/migration-v1.0.md).

## [v0.18.0] - 2026-03-24

### Added
- **`protoc-gen-entitystore` codegen enhancements** — generates four additional functions per annotated message:
  - `{Entity}Tokens(msg)` — typed token extraction using `matching.Tokenize` (no reflection).
  - `{Entity}EmbedText(msg)` — concatenated embed fields for embedding input.
  - `{entity}Anchors(msg)` — anchor extraction with normalizers and composite anchors (unexported, used by WriteOp).
  - `{Entity}WriteOp(msg, action, opts...)` — complete `*WriteEntityOp` builder with anchors, tokens, and data wired automatically.
- **`WriteOpOption`** type and option functions: `WithTags`, `WithConfidence`, `WithMatchedEntityID`, `WithEmbedding`, `WithID`, `WithProvenance`.
- **`Provenance(sourceURN, modelID)`** convenience builder.

See [Migration Guide v0.17→v0.18](docs/migration-v0.18.md).

## [v0.17.0] - 2026-03-24

### Added
- **Graph traversal** — `Traverse(ctx, entityID, opts)` performs multi-hop exploration using a PostgreSQL recursive CTE. Supports direction control (`DirectionBoth`, `DirectionOutbound`, `DirectionInbound`), relation/entity type filtering, confidence thresholds, tag filtering, and depth/result caps.
- `ScopedStore.Traverse` merges scope filters into the CTE — traversal stops at scope boundaries.
- New types: `Direction`, `TraverseOpts`, `TraverseResult`, `TraverseEdge`.

See [ADR-007](docs/adr/007_traverse.md).

## [v0.16.0] - 2026-03-23

### Added
- **`ScopedStore`** — tag-based multi-tenant filtering wrapper. Created via `es.Scoped(ScopeConfig{...})`. Reads filtered by `RequireTags`/`ExcludeTag`/`UnlessTags`; creates auto-tagged with `AutoTags`.
- `ErrAccessDenied` for entities outside scope.
- Scope config preserved across `WithTx`.

See [ADR-006](docs/adr/006_scoped-store.md).

## [v0.15.0] - 2026-03-23

### Added
- **Preconditions** on `BatchWrite` — `MustExist`, `MustNotExist`, `TagRequired`, `TagForbidden` guards evaluated inside the transaction. `PreConditionError` with `OpIndex`, `Condition`, `Violation` for caller inspection.

See [ADR-005](docs/adr/005_pre-conditions.md).

## [v0.14.0] - 2026-03-19

### Fixed
- Empty entity type filter on `FindByEmbedding` now correctly returns results across all types.
- Relation operations (`UpdateRelationData`, `DeleteRelationByKey`) added to `EntityStore` and `ScopedStore`.
- `FindConnectedByType` pagination cursor handling fixed.

## [v0.13.0] - 2026-03-19

### Added
- **`WithTx(pgx.Tx)`** on `EntityStore` — shared transactions across stores sharing the same PostgreSQL pool.

## [v0.12.0] - 2026-03-19

### Changed
- **`Embedder` interface** — replaced `EmbedderFunc` with batch-native `Embedder` interface compatible with `laenen-partners/embedder`.
- Renamed `TextToEmbed` parameters for clarity.

## [v0.11.0] - 2026-03-19

### Added
- **`ExcludeTag`/`UnlessTags`** conditional visibility filter on `QueryFilter`.
- **`AnyTags`** OR-based tag filtering (entity must have at least one of the listed tags).

## [v0.10.0] - 2026-03-19

### Changed
- **Extraction package split** — `extraction` types moved from `matching` to top-level `extraction` package. Import path: `github.com/laenen-partners/entitystore/extraction`.

## [v0.9.0] - 2026-03-19

### Added
- **Matching engine** — `Matcher` orchestrates anchor lookup, fuzzy candidate retrieval (tokens + embeddings), field-level scoring (Jaro-Winkler, Levenshtein, Token Jaccard, Exact), threshold-based decisions, and merge plans.
- `MatchConfigRegistry` and `MatcherRegistry` for multi-entity-type matching.

See [ADR-002](docs/adr/002-matching-engine.md).

## [v0.8.0] - 2026-03-19

### Breaking
- **`WriteEntityOp.Data`** changed from `json.RawMessage` to `proto.Message`. `EntityType` removed — derived automatically from `proto.MessageName(Data)`.
- **`UpsertRelationOp.Data`** changed from `map[string]any` to `proto.Message`. `DataType` auto-derived.
- `StoredEntity.GetData(msg)` and `StoredRelation.GetData(msg)` added for proto unmarshalling.
- Migration adds `data_type` column to `entity_relations`.

See [Migration Guide v0.7→v0.8](docs/migration-v0.8.md).

## [v0.7.0] - 2026-03-19

### Added
- **LLM extraction schema generation** — `protoc-gen-entitystore` generates `{Entity}ExtractionSchema()` alongside `{Entity}MatchConfig()`. Field descriptions derived from proto comments with annotation overrides.

See [ADR-001](docs/adr/001-llm-entity-extraction-schema-generation.md).

[Unreleased]: https://github.com/laenen-partners/entitystore/compare/v0.25.0...HEAD
[v0.25.0]: https://github.com/laenen-partners/entitystore/compare/v0.24.0...v0.25.0
[v0.24.0]: https://github.com/laenen-partners/entitystore/compare/v0.23.0...v0.24.0
[v0.23.0]: https://github.com/laenen-partners/entitystore/compare/v0.22.0...v0.23.0
[v0.22.0]: https://github.com/laenen-partners/entitystore/compare/v0.21.0...v0.22.0
[v0.21.0]: https://github.com/laenen-partners/entitystore/compare/v0.20.0...v0.21.0
[v0.20.0]: https://github.com/laenen-partners/entitystore/compare/v0.19.0...v0.20.0
[v0.19.0]: https://github.com/laenen-partners/entitystore/compare/v0.18.0...v0.19.0
[v0.18.0]: https://github.com/laenen-partners/entitystore/compare/v0.17.0...v0.18.0
[v0.17.0]: https://github.com/laenen-partners/entitystore/compare/v0.16.0...v0.17.0
[v0.16.0]: https://github.com/laenen-partners/entitystore/compare/v0.15.0...v0.16.0
[v0.15.0]: https://github.com/laenen-partners/entitystore/compare/v0.14.0...v0.15.0
[v0.14.0]: https://github.com/laenen-partners/entitystore/compare/v0.13.0...v0.14.0
[v0.13.0]: https://github.com/laenen-partners/entitystore/compare/v0.12.0...v0.13.0
[v0.12.0]: https://github.com/laenen-partners/entitystore/compare/v0.11.0...v0.12.0
[v0.11.0]: https://github.com/laenen-partners/entitystore/compare/v0.10.0...v0.11.0
[v0.10.0]: https://github.com/laenen-partners/entitystore/compare/v0.9.0...v0.10.0
[v0.9.0]: https://github.com/laenen-partners/entitystore/compare/v0.8.0...v0.9.0
[v0.8.0]: https://github.com/laenen-partners/entitystore/compare/v0.7.0...v0.8.0
[v0.7.0]: https://github.com/laenen-partners/entitystore/releases/tag/v0.7.0
