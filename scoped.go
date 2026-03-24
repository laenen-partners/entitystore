package entitystore

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"

	"github.com/laenen-partners/entitystore/matching"
	"github.com/laenen-partners/entitystore/store"
)

// ErrAccessDenied is returned when an entity exists but is not visible
// in the current scope. Callers can choose to disguise this as "not found"
// or surface it directly depending on their security requirements.
var ErrAccessDenied = fmt.Errorf("entitystore: access denied")

// ScopeConfig defines tag-based read/write filtering for a ScopedStore.
type ScopeConfig struct {
	// RequireTags filters reads — entities must carry ALL of these tags (AND semantics).
	RequireTags []string

	// ExcludeTag hides entities carrying this tag from read results,
	// unless the entity also carries one of the UnlessTags.
	ExcludeTag string

	// UnlessTags exempts entities from ExcludeTag filtering.
	UnlessTags []string

	// AutoTags are automatically added to every entity created
	// through this scoped store (WriteActionCreate only).
	AutoTags []string
}

// ScopedStore wraps an EntityStore with tag-based read/write filtering.
// All read queries are filtered by RequireTags and ExcludeTag/UnlessTags.
// All creates are auto-tagged with AutoTags.
type ScopedStore struct {
	inner *EntityStore
	cfg   ScopeConfig
}

// Scoped returns a new ScopedStore that applies tag-based filtering
// to all read and write operations.
func (es *EntityStore) Scoped(cfg ScopeConfig) *ScopedStore {
	return &ScopedStore{inner: es, cfg: cfg}
}

// WithTx returns a ScopedStore that uses the given transaction.
// The scope configuration is preserved.
func (s *ScopedStore) WithTx(tx pgx.Tx) *ScopedStore {
	return &ScopedStore{
		inner: s.inner.WithTx(tx),
		cfg:   s.cfg,
	}
}

// ---------------------------------------------------------------------------
// Filter merging
// ---------------------------------------------------------------------------

func (s *ScopedStore) mergeFilter(filter *matching.QueryFilter) *matching.QueryFilter {
	merged := &matching.QueryFilter{}
	if filter != nil {
		*merged = *filter
	}
	merged.Tags = append(merged.Tags, s.cfg.RequireTags...)
	if s.cfg.ExcludeTag != "" {
		merged.ExcludeTag = s.cfg.ExcludeTag
		merged.UnlessTags = append(merged.UnlessTags, s.cfg.UnlessTags...)
	}
	return merged
}

// entityVisible checks whether an entity passes the scope filter.
func (s *ScopedStore) entityVisible(e matching.StoredEntity) bool {
	for _, req := range s.cfg.RequireTags {
		if !slices.Contains(e.Tags, req) {
			return false
		}
	}
	if s.cfg.ExcludeTag != "" && slices.Contains(e.Tags, s.cfg.ExcludeTag) {
		for _, u := range s.cfg.UnlessTags {
			if slices.Contains(e.Tags, u) {
				return true
			}
		}
		return false
	}
	return true
}

// ---------------------------------------------------------------------------
// Entity reads
// ---------------------------------------------------------------------------

// GetEntity returns a single entity by ID. Returns ErrAccessDenied if the
// entity does not exist or is not visible in the current scope.
func (s *ScopedStore) GetEntity(ctx context.Context, id string) (matching.StoredEntity, error) {
	ent, err := s.inner.GetEntity(ctx, id)
	if err != nil {
		return matching.StoredEntity{}, err
	}
	if !s.entityVisible(ent) {
		return matching.StoredEntity{}, ErrAccessDenied
	}
	return ent, nil
}

// GetEntitiesByType returns entities of the given type with cursor-based
// pagination, filtered by the scope.
func (s *ScopedStore) GetEntitiesByType(ctx context.Context, entityType string, pageSize int32, cursor *time.Time) ([]matching.StoredEntity, error) {
	return s.inner.GetEntitiesByTypeFiltered(ctx, entityType, pageSize, cursor, s.mergeFilter(nil))
}

// GetEntitiesByTypeFiltered returns entities with the caller's filter merged
// with the scope filter.
func (s *ScopedStore) GetEntitiesByTypeFiltered(ctx context.Context, entityType string, pageSize int32, cursor *time.Time, filter *matching.QueryFilter) ([]matching.StoredEntity, error) {
	return s.inner.GetEntitiesByTypeFiltered(ctx, entityType, pageSize, cursor, s.mergeFilter(filter))
}

// GetByAnchor returns a single entity matching the given anchor, or ErrNotFound.
func (s *ScopedStore) GetByAnchor(ctx context.Context, entityType, field, value string, filter *matching.QueryFilter) (matching.StoredEntity, error) {
	ent, err := s.inner.GetByAnchor(ctx, entityType, field, value, s.mergeFilter(filter))
	if err != nil {
		return matching.StoredEntity{}, err
	}
	if !s.entityVisible(ent) {
		return matching.StoredEntity{}, ErrAccessDenied
	}
	return ent, nil
}

// FindByAnchors searches for entities matching the given anchor values.
func (s *ScopedStore) FindByAnchors(ctx context.Context, entityType string, anchors []matching.AnchorQuery, filter *matching.QueryFilter) ([]matching.StoredEntity, error) {
	return s.inner.FindByAnchors(ctx, entityType, anchors, s.mergeFilter(filter))
}

// FindByTokens searches for entities with overlapping tokens.
func (s *ScopedStore) FindByTokens(ctx context.Context, entityType string, tokens []string, limit int, filter *matching.QueryFilter) ([]matching.StoredEntity, error) {
	return s.inner.FindByTokens(ctx, entityType, tokens, limit, s.mergeFilter(filter))
}

// FindByEmbedding searches for entities by vector similarity.
func (s *ScopedStore) FindByEmbedding(ctx context.Context, entityType string, vec []float32, topK int, filter *matching.QueryFilter) ([]matching.StoredEntity, error) {
	return s.inner.FindByEmbedding(ctx, entityType, vec, topK, s.mergeFilter(filter))
}

// FindConnectedByType finds entities connected to the given entity by relation type.
func (s *ScopedStore) FindConnectedByType(ctx context.Context, entityID string, entityType string, relationTypes []string, filter *matching.QueryFilter, pageSize int32, cursor *time.Time) ([]matching.StoredEntity, error) {
	return s.inner.FindConnectedByType(ctx, entityID, entityType, relationTypes, s.mergeFilter(filter), pageSize, cursor)
}

// FindEntitiesByRelation finds entities that participate in a given relation type.
func (s *ScopedStore) FindEntitiesByRelation(ctx context.Context, entityType string, relationType string, filter *matching.QueryFilter) ([]matching.StoredEntity, error) {
	return s.inner.FindEntitiesByRelation(ctx, entityType, relationType, s.mergeFilter(filter))
}

// Traverse performs a multi-hop graph traversal with scope filters applied.
func (s *ScopedStore) Traverse(ctx context.Context, entityID string, opts *store.TraverseOpts) ([]store.TraverseResult, error) {
	o := store.TraverseOpts{}
	if opts != nil {
		o = *opts
	}
	o.Filter = s.mergeFilter(o.Filter)
	return s.inner.Traverse(ctx, entityID, &o)
}

// ConnectedEntities returns all entities connected to the given entity,
// filtered by the scope (post-fetch).
func (s *ScopedStore) ConnectedEntities(ctx context.Context, entityID string) ([]matching.StoredEntity, error) {
	entities, err := s.inner.ConnectedEntities(ctx, entityID)
	if err != nil {
		return nil, err
	}
	filtered := make([]matching.StoredEntity, 0, len(entities))
	for _, e := range entities {
		if s.entityVisible(e) {
			filtered = append(filtered, e)
		}
	}
	return filtered, nil
}

// ---------------------------------------------------------------------------
// Relation reads
// ---------------------------------------------------------------------------

// GetRelationsFromEntity returns outbound relations from the given entity with pagination.
func (s *ScopedStore) GetRelationsFromEntity(ctx context.Context, entityID string, pageSize int32, cursor *time.Time) ([]matching.StoredRelation, error) {
	return s.inner.GetRelationsFromEntity(ctx, entityID, pageSize, cursor)
}

// GetRelationsToEntity returns inbound relations to the given entity with pagination.
func (s *ScopedStore) GetRelationsToEntity(ctx context.Context, entityID string, pageSize int32, cursor *time.Time) ([]matching.StoredRelation, error) {
	return s.inner.GetRelationsToEntity(ctx, entityID, pageSize, cursor)
}

// ---------------------------------------------------------------------------
// Provenance
// ---------------------------------------------------------------------------

// GetProvenanceForEntity returns provenance entries for the given entity.
func (s *ScopedStore) GetProvenanceForEntity(ctx context.Context, entityID string) ([]matching.ProvenanceEntry, error) {
	return s.inner.GetProvenanceForEntity(ctx, entityID)
}

// ---------------------------------------------------------------------------
// Writes
// ---------------------------------------------------------------------------

// BatchWrite executes operations with scope-aware auto-tagging.
// WriteActionCreate operations have AutoTags appended to their Tags.
func (s *ScopedStore) BatchWrite(ctx context.Context, ops []store.BatchWriteOp) ([]store.BatchWriteResult, error) {
	if len(s.cfg.AutoTags) == 0 {
		return s.inner.BatchWrite(ctx, ops)
	}

	scoped := make([]store.BatchWriteOp, len(ops))
	copy(scoped, ops)

	for i := range scoped {
		if scoped[i].WriteEntity != nil && scoped[i].WriteEntity.Action == store.WriteActionCreate {
			// Copy the WriteEntityOp to avoid mutating the caller's slice.
			op := *scoped[i].WriteEntity
			op.Tags = append(append([]string{}, op.Tags...), s.cfg.AutoTags...)
			scoped[i].WriteEntity = &op
		}
	}

	return s.inner.BatchWrite(ctx, scoped)
}

// DeleteEntity removes an entity and its associated data.
func (s *ScopedStore) DeleteEntity(ctx context.Context, id string) error {
	return s.inner.DeleteEntity(ctx, id)
}

// DeleteRelationByKey removes a specific relation by source, target, and type.
func (s *ScopedStore) DeleteRelationByKey(ctx context.Context, sourceID, targetID, relationType string) error {
	return s.inner.DeleteRelationByKey(ctx, sourceID, targetID, relationType)
}

// UpdateRelationData updates the typed data on an existing relation.
func (s *ScopedStore) UpdateRelationData(ctx context.Context, sourceID, targetID, relationType string, data proto.Message) (matching.StoredRelation, error) {
	return s.inner.UpdateRelationData(ctx, sourceID, targetID, relationType, data)
}

// ---------------------------------------------------------------------------
// Tags
// ---------------------------------------------------------------------------

// SetTags replaces all tags on an entity.
func (s *ScopedStore) SetTags(ctx context.Context, entityID string, tags []string) error {
	return s.inner.SetTags(ctx, entityID, tags)
}

// AddTags appends tags to an entity.
func (s *ScopedStore) AddTags(ctx context.Context, entityID string, tags []string) error {
	return s.inner.AddTags(ctx, entityID, tags)
}

// RemoveTag removes a single tag from an entity.
func (s *ScopedStore) RemoveTag(ctx context.Context, entityID string, tag string) error {
	return s.inner.RemoveTag(ctx, entityID, tag)
}

// ---------------------------------------------------------------------------
// Embedding
// ---------------------------------------------------------------------------

// UpdateEmbedding sets the embedding vector for an entity.
func (s *ScopedStore) UpdateEmbedding(ctx context.Context, entityID string, vec []float32) error {
	return s.inner.UpdateEmbedding(ctx, entityID, vec)
}

// ---------------------------------------------------------------------------
// Transactions
// ---------------------------------------------------------------------------

// Tx begins a new transaction and returns a TxStore for atomic operations.
func (s *ScopedStore) Tx(ctx context.Context) (*store.TxStore, error) {
	return s.inner.Tx(ctx)
}
