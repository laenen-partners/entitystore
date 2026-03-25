// Package entitystore provides entity storage, deduplication, and relationship
// management with PostgreSQL + pgvector backend.
//
// It is designed for entity resolution pipelines: extract entities from
// unstructured sources, match against existing records via anchors, tokens,
// and embeddings, then store with full provenance tracking.
//
// # Quick start
//
//	pool, _ := pgxpool.New(ctx, connString)
//	entitystore.Migrate(ctx, pool)
//	es, _ := entitystore.New(entitystore.WithPgStore(pool))
//	defer es.Close()
//
//	// Write — anchors, tokens wired by generated code
//	op := personv1.PersonWriteOp(person, entitystore.WriteActionCreate,
//	    entitystore.WithTags("ws:acme"),
//	)
//	results, _ := es.BatchWrite(ctx, []entitystore.BatchWriteOp{{WriteEntity: op}})
//
//	// Read
//	entity, _ := es.GetEntity(ctx, results[0].Entity.ID)
//	found, _ := es.GetByAnchor(ctx, "persons.v1.Person", "email", "alice@example.com", nil)
//
//	// Traverse
//	neighbors, _ := es.Traverse(ctx, entity.ID, &entitystore.TraverseOpts{MaxDepth: 2})
//
// Use [EntityStorer] as the interface for dependency injection and testing.
// Both [EntityStore] and [ScopedStore] satisfy it.
//
// Use protoc-gen-entitystore to generate typed token extractors, anchor
// builders, and WriteOp helpers from proto annotations. See the cmd/
// directory and examples/ for usage patterns.
package entitystore

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"

	"github.com/laenen-partners/entitystore/matching"
	"github.com/laenen-partners/entitystore/store"
)

// EntityStore is the main entry point for entity storage operations.
type EntityStore struct {
	store *store.Store
}

// New creates an EntityStore with the given options. At least one store
// backend must be provided (e.g. WithPgStore).
func New(opts ...Option) (*EntityStore, error) {
	var o options
	for _, opt := range opts {
		opt(&o)
	}

	if o.pool == nil {
		return nil, errors.New("entitystore: no store backend configured (use WithPgStore)")
	}

	return &EntityStore{store: store.NewFromPool(o.pool, o.storeOps...)}, nil
}

// Close releases resources held by the underlying store.
func (es *EntityStore) Close() {
	es.store.Close()
}

// WithTx returns an EntityStore that executes all operations within the
// given transaction. The caller is responsible for committing or rolling
// back the transaction. The returned EntityStore must not be used after
// the transaction ends.
//
// This enables atomic operations spanning multiple stores that share
// the same PostgreSQL database and connection pool.
func (es *EntityStore) WithTx(tx pgx.Tx) *EntityStore {
	return &EntityStore{
		store: es.store.WithTx(tx),
	}
}

// ---------------------------------------------------------------------------
// Entity reads
// ---------------------------------------------------------------------------

// GetEntity returns a single entity by ID.
func (es *EntityStore) GetEntity(ctx context.Context, id string) (matching.StoredEntity, error) {
	return es.store.GetEntity(ctx, id)
}

// GetEntitiesByType returns entities of the given type with cursor-based
// pagination and optional tag/visibility filtering.
// Pass filter=nil for no filtering. Pass pageSize=0 for default (100).
func (es *EntityStore) GetEntitiesByType(ctx context.Context, entityType string, pageSize int32, cursor *time.Time, filter *QueryFilter) ([]matching.StoredEntity, error) {
	return es.store.GetEntitiesByTypeFiltered(ctx, entityType, pageSize, cursor, filter)
}

// GetByAnchor returns a single entity matching the given anchor, or ErrNotFound.
func (es *EntityStore) GetByAnchor(ctx context.Context, entityType, field, value string, filter *matching.QueryFilter) (matching.StoredEntity, error) {
	return es.store.GetByAnchor(ctx, entityType, field, value, filter)
}

// FindByAnchors searches for entities matching the given anchor values.
func (es *EntityStore) FindByAnchors(ctx context.Context, entityType string, anchors []matching.AnchorQuery, filter *matching.QueryFilter) ([]matching.StoredEntity, error) {
	return es.store.FindByAnchors(ctx, entityType, anchors, filter)
}

// FindByTokens searches for entities with overlapping tokens.
func (es *EntityStore) FindByTokens(ctx context.Context, entityType string, tokens []string, limit int, filter *matching.QueryFilter) ([]matching.StoredEntity, error) {
	return es.store.FindByTokens(ctx, entityType, tokens, limit, filter)
}

// FindByEmbedding searches for entities by vector similarity.
func (es *EntityStore) FindByEmbedding(ctx context.Context, entityType string, vec []float32, topK int, filter *matching.QueryFilter) ([]matching.StoredEntity, error) {
	return es.store.FindByEmbedding(ctx, entityType, vec, topK, filter)
}

// FindConnectedOpts configures a FindConnectedByType query.
type FindConnectedOpts struct {
	EntityType    string          // filter connected entities by type (empty = all)
	RelationTypes []string        // filter edges by type (empty = all)
	Filter        *QueryFilter    // tag filtering on connected entities
	PageSize      int32           // default 1000
	Cursor        *time.Time      // cursor for pagination (nil = first page)
}

// FindConnectedByType finds entities connected to the given entity.
func (es *EntityStore) FindConnectedByType(ctx context.Context, entityID string, opts *FindConnectedOpts) ([]matching.StoredEntity, error) {
	if opts == nil {
		opts = &FindConnectedOpts{}
	}
	return es.store.FindConnectedByType(ctx, entityID, opts.EntityType, opts.RelationTypes, opts.Filter, opts.PageSize, opts.Cursor)
}

// FindEntitiesByRelation finds entities that participate in a given relation type.
func (es *EntityStore) FindEntitiesByRelation(ctx context.Context, entityType string, relationType string, filter *matching.QueryFilter) ([]matching.StoredEntity, error) {
	return es.store.FindEntitiesByRelation(ctx, entityType, relationType, filter)
}

// ConnectedEntities returns all entities connected to the given entity.
func (es *EntityStore) ConnectedEntities(ctx context.Context, entityID string) ([]matching.StoredEntity, error) {
	return es.store.ConnectedEntities(ctx, entityID)
}

// ---------------------------------------------------------------------------
// Graph traversal
// ---------------------------------------------------------------------------

// Traverse performs a multi-hop graph traversal starting from the given entity.
func (es *EntityStore) Traverse(ctx context.Context, entityID string, opts *store.TraverseOpts) ([]store.TraverseResult, error) {
	return es.store.Traverse(ctx, entityID, opts)
}

// ---------------------------------------------------------------------------
// Relation reads
// ---------------------------------------------------------------------------

// GetRelationsFromEntity returns outbound relations from the given entity with pagination.
// Pass pageSize=0 for default (1000). Pass cursor=nil for first page.
func (es *EntityStore) GetRelationsFromEntity(ctx context.Context, entityID string, pageSize int32, cursor *time.Time) ([]matching.StoredRelation, error) {
	return es.store.GetRelationsFromEntity(ctx, entityID, pageSize, cursor)
}

// GetRelationsToEntity returns inbound relations to the given entity with pagination.
// Pass pageSize=0 for default (1000). Pass cursor=nil for first page.
func (es *EntityStore) GetRelationsToEntity(ctx context.Context, entityID string, pageSize int32, cursor *time.Time) ([]matching.StoredRelation, error) {
	return es.store.GetRelationsToEntity(ctx, entityID, pageSize, cursor)
}

// ---------------------------------------------------------------------------
// Events
// ---------------------------------------------------------------------------

// GetEventsForEntity returns events for the given entity, newest first.
func (es *EntityStore) GetEventsForEntity(ctx context.Context, entityID string, opts *EventQueryOpts) ([]Event, error) {
	return es.store.GetEventsForEntity(ctx, entityID, opts)
}

// ---------------------------------------------------------------------------
// Writes
// ---------------------------------------------------------------------------

// BatchWrite executes mixed entity writes and relation upserts in a single transaction.
func (es *EntityStore) BatchWrite(ctx context.Context, ops []store.BatchWriteOp) ([]store.BatchWriteResult, error) {
	return es.store.BatchWrite(ctx, ops)
}

// DeleteEntity soft-deletes an entity (sets deleted_at, preserves data for audit).
func (es *EntityStore) DeleteEntity(ctx context.Context, id string) error {
	return es.store.DeleteEntity(ctx, id)
}

// HardDeleteEntity permanently removes an entity and all its associated data.
func (es *EntityStore) HardDeleteEntity(ctx context.Context, id string) error {
	return es.store.HardDeleteEntity(ctx, id)
}

// DeleteRelationByKey removes a specific relation by source, target, and type.
func (es *EntityStore) DeleteRelationByKey(ctx context.Context, sourceID, targetID, relationType string) error {
	return es.store.DeleteRelationByKey(ctx, sourceID, targetID, relationType)
}

// UpdateRelationData updates the typed data on an existing relation.
func (es *EntityStore) UpdateRelationData(ctx context.Context, sourceID, targetID, relationType string, data proto.Message) (matching.StoredRelation, error) {
	return es.store.UpdateRelationData(ctx, sourceID, targetID, relationType, data)
}

// ---------------------------------------------------------------------------
// Tags
// ---------------------------------------------------------------------------

// SetTags replaces all tags on an entity.
func (es *EntityStore) SetTags(ctx context.Context, entityID string, tags []string) error {
	return es.store.SetTags(ctx, entityID, tags)
}

// AddTags appends tags to an entity.
func (es *EntityStore) AddTags(ctx context.Context, entityID string, tags []string) error {
	return es.store.AddTags(ctx, entityID, tags)
}

// RemoveTag removes a single tag from an entity.
func (es *EntityStore) RemoveTag(ctx context.Context, entityID string, tag string) error {
	return es.store.RemoveTag(ctx, entityID, tag)
}

// ---------------------------------------------------------------------------
// Embedding
// ---------------------------------------------------------------------------

// UpdateEmbedding sets the embedding vector for an entity.
func (es *EntityStore) UpdateEmbedding(ctx context.Context, entityID string, vec []float32) error {
	return es.store.UpdateEmbedding(ctx, entityID, vec)
}

// ---------------------------------------------------------------------------
// Transactions
// ---------------------------------------------------------------------------

// TxStore wraps a database transaction for atomic multi-step operations.
// Use Commit or Rollback to end the transaction.
type TxStore struct {
	inner *store.TxStore
}

// Tx begins a new transaction and returns a TxStore.
func (es *EntityStore) Tx(ctx context.Context) (*TxStore, error) {
	tx, err := es.store.Tx(ctx)
	if err != nil {
		return nil, err
	}
	return &TxStore{inner: tx}, nil
}

// Commit commits the transaction.
func (tx *TxStore) Commit(ctx context.Context) error { return tx.inner.Commit(ctx) }

// Rollback aborts the transaction.
func (tx *TxStore) Rollback(ctx context.Context) error { return tx.inner.Rollback(ctx) }

// GetEntity returns a single entity by ID within the transaction.
func (tx *TxStore) GetEntity(ctx context.Context, id string) (matching.StoredEntity, error) {
	return tx.inner.GetEntity(ctx, id)
}

// FindByAnchors searches for entities within the transaction.
func (tx *TxStore) FindByAnchors(ctx context.Context, entityType string, anchors []matching.AnchorQuery, filter *matching.QueryFilter) ([]matching.StoredEntity, error) {
	return tx.inner.FindByAnchors(ctx, entityType, anchors, filter)
}

// GetRelationsFromEntity returns outbound relations within the transaction.
func (tx *TxStore) GetRelationsFromEntity(ctx context.Context, entityID string, pageSize int32, cursor *time.Time) ([]matching.StoredRelation, error) {
	return tx.inner.GetRelationsFromEntity(ctx, entityID, pageSize, cursor)
}

// GetRelationsToEntity returns inbound relations within the transaction.
func (tx *TxStore) GetRelationsToEntity(ctx context.Context, entityID string, pageSize int32, cursor *time.Time) ([]matching.StoredRelation, error) {
	return tx.inner.GetRelationsToEntity(ctx, entityID, pageSize, cursor)
}

// WriteEntity applies a single entity write within the transaction.
func (tx *TxStore) WriteEntity(ctx context.Context, op *store.WriteEntityOp) (matching.StoredEntity, error) {
	return tx.inner.WriteEntity(ctx, op)
}

// UpsertRelation creates or updates a relation within the transaction.
func (tx *TxStore) UpsertRelation(ctx context.Context, op *store.UpsertRelationOp) (matching.StoredRelation, error) {
	return tx.inner.UpsertRelation(ctx, op)
}

// DeleteRelationByKey removes a relation within the transaction.
func (tx *TxStore) DeleteRelationByKey(ctx context.Context, sourceID, targetID, relationType string) error {
	return tx.inner.DeleteRelationByKey(ctx, sourceID, targetID, relationType)
}

// GetEventsForEntity returns events for the given entity within the transaction.
func (tx *TxStore) GetEventsForEntity(ctx context.Context, entityID string, opts *EventQueryOpts) ([]Event, error) {
	return tx.inner.GetEventsForEntity(ctx, entityID, opts)
}

// UpdateRelationData updates typed data on an existing relation within the transaction.
func (tx *TxStore) UpdateRelationData(ctx context.Context, sourceID, targetID, relationType string, data proto.Message) (matching.StoredRelation, error) {
	return tx.inner.UpdateRelationData(ctx, sourceID, targetID, relationType, data)
}

// ---------------------------------------------------------------------------
// Stats
// ---------------------------------------------------------------------------

// Stats returns aggregate statistics about the store contents.
func (es *EntityStore) Stats(ctx context.Context) (store.StoreStats, error) {
	return es.store.Stats(ctx)
}

// CountEntitiesByType returns the number of non-deleted entities of the given type.
func (es *EntityStore) CountEntitiesByType(ctx context.Context, entityType string) (int64, error) {
	return es.store.CountEntitiesByType(ctx, entityType)
}

// CountRelationsForEntity returns the number of relations for an entity.
func (es *EntityStore) CountRelationsForEntity(ctx context.Context, entityID string) (int64, error) {
	return es.store.CountRelationsForEntity(ctx, entityID)
}

// ---------------------------------------------------------------------------
// Re-exported types for convenience
// ---------------------------------------------------------------------------

// WriteAction specifies the type of entity write operation.
type WriteAction = store.WriteAction

// Write action constants.
const (
	WriteActionCreate = store.WriteActionCreate
	WriteActionUpdate = store.WriteActionUpdate
	WriteActionMerge  = store.WriteActionMerge
)

// WriteEntityOp describes a single entity write within a batch.
type WriteEntityOp = store.WriteEntityOp

// UpsertRelationOp describes a single relation upsert within a batch.
type UpsertRelationOp = store.UpsertRelationOp

// BatchWriteOp is a single operation in a batch.
type BatchWriteOp = store.BatchWriteOp

// BatchWriteResult is the result of a single operation in a batch.
type BatchWriteResult = store.BatchWriteResult

// PreCondition is a check evaluated inside the BatchWrite transaction.
type PreCondition = store.PreCondition

// PreConditionError is returned when a precondition check fails.
type PreConditionError = store.PreConditionError

// StoredEntity is a persisted entity record.
type StoredEntity = matching.StoredEntity

// StoredRelation is a directed edge between two entities.
type StoredRelation = matching.StoredRelation

// Event is a stored event with its metadata.
type Event = store.Event

// EventQueryOpts filters event queries.
type EventQueryOpts = store.EventQueryOpts

// AnchorQuery is a single anchor lookup.
type AnchorQuery = matching.AnchorQuery

// QueryFilter narrows entity searches by tags.
type QueryFilter = matching.QueryFilter

// Direction controls which edge directions the traversal follows.
type Direction = store.Direction

// Direction constants.
const (
	DirectionBoth     = store.DirectionBoth
	DirectionOutbound = store.DirectionOutbound
	DirectionInbound  = store.DirectionInbound
)

// TraverseOpts configures a graph traversal.
type TraverseOpts = store.TraverseOpts

// TraverseResult represents a single entity discovered during traversal.
type TraverseResult = store.TraverseResult

// TraverseEdge represents a single edge in a traversal path.
type TraverseEdge = store.TraverseEdge

// MaxBatchSize is the maximum number of operations in a single BatchWrite call.
const MaxBatchSize = store.MaxBatchSize

// ErrNotFound is returned when a single-entity lookup finds no match.
var ErrNotFound = store.ErrNotFound

// WriteOpOption configures a WriteEntityOp built by generated code.
type WriteOpOption = store.WriteOpOption

// Write option functions for use with generated WriteOp functions.
var (
	WithMatchedEntityID = store.WithMatchedEntityID
	WithConfidence      = store.WithConfidence
	WithTags            = store.WithTags
	WithEmbedding       = store.WithEmbedding
	WithID              = store.WithID
	WithEvents          = store.WithEvents
)

// StoreStats contains aggregate statistics about the store contents.
type StoreStats = store.StoreStats

// TypeCount represents the count of entities or relations for a given type.
type TypeCount = store.TypeCount

// Migrate applies all pending database migrations using the given pool.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	return store.Migrate(ctx, pool)
}

// ---------------------------------------------------------------------------
// Health
// ---------------------------------------------------------------------------

// HealthStatus is the overall health of the entity store.
type HealthStatus = store.HealthStatus

// DBHealth reports database connection pool status.
type DBHealth = store.DBHealth

// EventHealth reports event store activity.
type EventHealth = store.EventHealth

// PublisherHealth reports outbox publisher status.
type PublisherHealth = store.PublisherHealth

// Health returns the current health status of the store.
func (es *EntityStore) Health(ctx context.Context) (HealthStatus, error) {
	return es.store.Health(ctx)
}

// ---------------------------------------------------------------------------
// Publisher
// ---------------------------------------------------------------------------

// PublishFunc is called by the publisher to deliver a batch of events.
type PublishFunc = store.PublishFunc

// PublisherConfig configures the outbox publisher.
type PublisherConfig = store.PublisherConfig

// Publisher polls entity_events for unpublished rows and delivers them
// via a caller-provided PublishFunc. Only one publisher runs at a time.
type Publisher = store.Publisher

// NewPublisher creates an outbox publisher using the EntityStore's pool.
func (es *EntityStore) NewPublisher(fn PublishFunc, cfg PublisherConfig) *Publisher {
	return store.NewPublisher(es.store.Pool(), fn, cfg)
}

