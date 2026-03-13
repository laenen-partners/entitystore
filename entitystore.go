// Package entitystore provides entity storage, deduplication, and relationship
// management backed by pluggable stores.
//
// Create an EntityStore with a PostgreSQL backend:
//
//	pool, _ := pgxpool.New(ctx, connString)
//	es, err := entitystore.New(
//	    entitystore.WithPgStore(pool),
//	)
package entitystore

import (
	"context"
	"encoding/json"
	"errors"
	"time"

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

	if o.store == nil {
		return nil, errors.New("entitystore: no store backend configured (use WithPgStore)")
	}

	return &EntityStore{store: o.store}, nil
}

// Close releases resources held by the underlying store.
func (es *EntityStore) Close() {
	es.store.Close()
}

// ---------------------------------------------------------------------------
// Entity reads
// ---------------------------------------------------------------------------

// GetEntity returns a single entity by ID.
func (es *EntityStore) GetEntity(ctx context.Context, id string) (matching.StoredEntity, error) {
	return es.store.GetEntity(ctx, id)
}

// GetEntitiesByType returns entities of the given type with cursor-based pagination.
func (es *EntityStore) GetEntitiesByType(ctx context.Context, entityType string, pageSize int32, cursor *time.Time) ([]matching.StoredEntity, error) {
	return es.store.GetEntitiesByType(ctx, entityType, pageSize, cursor)
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

// FindConnectedByType finds entities connected to the given entity by relation type.
func (es *EntityStore) FindConnectedByType(ctx context.Context, entityID string, entityType string, relationTypes []string, filter *matching.QueryFilter) ([]matching.StoredEntity, error) {
	return es.store.FindConnectedByType(ctx, entityID, entityType, relationTypes, filter)
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
// Relation reads
// ---------------------------------------------------------------------------

// GetRelationsFromEntity returns all outbound relations from the given entity.
func (es *EntityStore) GetRelationsFromEntity(ctx context.Context, entityID string) ([]matching.StoredRelation, error) {
	return es.store.GetRelationsFromEntity(ctx, entityID)
}

// GetRelationsToEntity returns all inbound relations to the given entity.
func (es *EntityStore) GetRelationsToEntity(ctx context.Context, entityID string) ([]matching.StoredRelation, error) {
	return es.store.GetRelationsToEntity(ctx, entityID)
}

// ---------------------------------------------------------------------------
// Provenance
// ---------------------------------------------------------------------------

// GetProvenanceForEntity returns provenance entries for the given entity.
func (es *EntityStore) GetProvenanceForEntity(ctx context.Context, entityID string) ([]matching.ProvenanceEntry, error) {
	return es.store.GetProvenanceForEntity(ctx, entityID)
}

// ---------------------------------------------------------------------------
// Writes
// ---------------------------------------------------------------------------

// BatchWrite executes mixed entity writes and relation upserts in a single transaction.
func (es *EntityStore) BatchWrite(ctx context.Context, ops []store.BatchWriteOp) ([]store.BatchWriteResult, error) {
	return es.store.BatchWrite(ctx, ops)
}

// DeleteEntity removes an entity and its associated data.
func (es *EntityStore) DeleteEntity(ctx context.Context, id string) error {
	return es.store.DeleteEntity(ctx, id)
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

// Tx begins a new transaction and returns a TxStore for atomic multi-step operations.
func (es *EntityStore) Tx(ctx context.Context) (*store.TxStore, error) {
	return es.store.Tx(ctx)
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

// StoredEntity is a persisted entity record.
type StoredEntity = matching.StoredEntity

// StoredRelation is a directed edge between two entities.
type StoredRelation = matching.StoredRelation

// ProvenanceEntry records the origin of an entity.
type ProvenanceEntry = matching.ProvenanceEntry

// AnchorQuery is a single anchor lookup.
type AnchorQuery = matching.AnchorQuery

// QueryFilter narrows entity searches by tags.
type QueryFilter = matching.QueryFilter

// Migrate applies all pending database migrations.
func Migrate(connString string) error {
	return store.Migrate(connString)
}

// MarshalEntityData is a convenience for marshaling entity data to JSON.
func MarshalEntityData(v any) (json.RawMessage, error) {
	return json.Marshal(v)
}
