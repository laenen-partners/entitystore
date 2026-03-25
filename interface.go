package entitystore

import (
	"context"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/laenen-partners/entitystore/matching"
	"github.com/laenen-partners/entitystore/store"
)

// EntityStorer is the interface satisfied by both EntityStore and ScopedStore.
// Use this type for dependency injection and testing.
type EntityStorer interface {
	// Entity reads
	GetEntity(ctx context.Context, id string) (matching.StoredEntity, error)
	GetByAnchor(ctx context.Context, entityType, field, value string, filter *matching.QueryFilter) (matching.StoredEntity, error)
	GetEntitiesByType(ctx context.Context, entityType string, pageSize int32, cursor *time.Time, filter *matching.QueryFilter) ([]matching.StoredEntity, error)
	FindByAnchors(ctx context.Context, entityType string, anchors []matching.AnchorQuery, filter *matching.QueryFilter) ([]matching.StoredEntity, error)
	FindByTokens(ctx context.Context, entityType string, tokens []string, limit int, filter *matching.QueryFilter) ([]matching.StoredEntity, error)
	FindByEmbedding(ctx context.Context, entityType string, vec []float32, topK int, filter *matching.QueryFilter) ([]matching.StoredEntity, error)
	FindConnectedByType(ctx context.Context, entityID string, opts *FindConnectedOpts) ([]matching.StoredEntity, error)
	FindEntitiesByRelation(ctx context.Context, entityType string, relationType string, filter *matching.QueryFilter) ([]matching.StoredEntity, error)
	ConnectedEntities(ctx context.Context, entityID string) ([]matching.StoredEntity, error)

	// Graph traversal
	Traverse(ctx context.Context, entityID string, opts *store.TraverseOpts) ([]store.TraverseResult, error)

	// Relation reads
	GetRelationsFromEntity(ctx context.Context, entityID string, pageSize int32, cursor *time.Time) ([]matching.StoredRelation, error)
	GetRelationsToEntity(ctx context.Context, entityID string, pageSize int32, cursor *time.Time) ([]matching.StoredRelation, error)

	// Events
	GetEventsForEntity(ctx context.Context, entityID string, opts *EventQueryOpts) ([]Event, error)

	// Writes
	BatchWrite(ctx context.Context, ops []store.BatchWriteOp) ([]store.BatchWriteResult, error)
	DeleteEntity(ctx context.Context, id string) error
	HardDeleteEntity(ctx context.Context, id string) error
	DeleteRelationByKey(ctx context.Context, sourceID, targetID, relationType string) error
	UpdateRelationData(ctx context.Context, sourceID, targetID, relationType string, data proto.Message) (matching.StoredRelation, error)

	// Tags
	SetTags(ctx context.Context, entityID string, tags []string) error
	AddTags(ctx context.Context, entityID string, tags []string) error
	RemoveTag(ctx context.Context, entityID string, tag string) error

	// Embedding
	UpdateEmbedding(ctx context.Context, entityID string, vec []float32) error

	// Stats
	Stats(ctx context.Context) (store.StoreStats, error)
	CountEntitiesByType(ctx context.Context, entityType string) (int64, error)
	CountRelationsForEntity(ctx context.Context, entityID string) (int64, error)
}

// Compile-time checks.
var (
	_ EntityStorer = (*EntityStore)(nil)
	_ EntityStorer = (*ScopedStore)(nil)
)
