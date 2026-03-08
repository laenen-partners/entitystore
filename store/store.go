// Package store implements matching.EntityStore backed by PostgreSQL.
//
// It uses pgx/v5 as the driver and SQLC-generated code for type-safe queries.
package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"

	"github.com/laenen-partners/entitystore/matching"
	"github.com/laenen-partners/entitystore/store/internal/dbgen"
)

// Store implements matching.EntityStore and matching.EmbeddingStore with a
// PostgreSQL backend.
type Store struct {
	pool    *pgxpool.Pool
	queries *dbgen.Queries
	ownPool bool
}

// New creates a Store connected to the given PostgreSQL connection string.
func New(ctx context.Context, connString string) (*Store, error) {
	pool, err := pgxpool.New(ctx, connString)
	if err != nil {
		return nil, fmt.Errorf("postgres store: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres store: ping: %w", err)
	}
	return &Store{
		pool:    pool,
		queries: dbgen.New(pool),
		ownPool: true,
	}, nil
}

// NewFromPool creates a Store from an existing pgxpool.Pool.
func NewFromPool(pool *pgxpool.Pool) *Store {
	return &Store{
		pool:    pool,
		queries: dbgen.New(pool),
		ownPool: false,
	}
}

// Close releases the connection pool if it was created by New.
func (s *Store) Close() {
	if s.ownPool {
		s.pool.Close()
	}
}

// Pool returns the underlying connection pool.
func (s *Store) Pool() *pgxpool.Pool {
	return s.pool
}

func tagsParam(filter *matching.QueryFilter) []string {
	if filter == nil || len(filter.Tags) == 0 {
		return []string{}
	}
	return filter.Tags
}

// ---------------------------------------------------------------------------
// matching.EntityStore implementation
// ---------------------------------------------------------------------------

func (s *Store) FindByAnchors(ctx context.Context, entityType string, anchors []matching.AnchorQuery, filter *matching.QueryFilter) ([]matching.StoredEntity, error) {
	seen := make(map[uuid.UUID]struct{})
	var result []matching.StoredEntity
	tags := tagsParam(filter)

	for _, aq := range anchors {
		rows, err := s.queries.FindByAnchors(ctx, dbgen.FindByAnchorsParams{
			EntityType:      entityType,
			AnchorField:     aq.Field,
			NormalizedValue: aq.Value,
			Tags:            tags,
		})
		if err != nil {
			return nil, fmt.Errorf("find by anchor %s=%s: %w", aq.Field, aq.Value, err)
		}
		for _, row := range rows {
			if _, ok := seen[row.ID]; ok {
				continue
			}
			seen[row.ID] = struct{}{}
			result = append(result, entityFromRow(row))
		}
	}
	return result, nil
}

func (s *Store) FindByTokens(ctx context.Context, entityType string, tokens []string, limit int, filter *matching.QueryFilter) ([]matching.StoredEntity, error) {
	rows, err := s.queries.FindByTokenOverlap(ctx, dbgen.FindByTokenOverlapParams{
		EntityType: entityType,
		Column2:    tokens,
		Tags:       tagsParam(filter),
		Limit:      int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("find by tokens: %w", err)
	}
	result := make([]matching.StoredEntity, len(rows))
	for i, row := range rows {
		result[i] = entityFromRow(row)
	}
	return result, nil
}

// ---------------------------------------------------------------------------
// matching.EmbeddingStore implementation
// ---------------------------------------------------------------------------

func (s *Store) FindByEmbedding(ctx context.Context, entityType string, vec []float32, topK int, filter *matching.QueryFilter) ([]matching.StoredEntity, error) {
	rows, err := s.queries.FindByEmbedding(ctx, dbgen.FindByEmbeddingParams{
		EntityType: entityType,
		Embedding:  pgVec(vec),
		Tags:       tagsParam(filter),
		TopK:       int32(topK),
	})
	if err != nil {
		return nil, fmt.Errorf("find by embedding: %w", err)
	}
	result := make([]matching.StoredEntity, len(rows))
	for i, row := range rows {
		result[i] = entityFromRow(row)
	}
	return result, nil
}

func (s *Store) UpdateEmbedding(ctx context.Context, entityID string, vec []float32) error {
	uid, err := uuid.Parse(entityID)
	if err != nil {
		return fmt.Errorf("parse entity id: %w", err)
	}
	return s.queries.UpdateEntityEmbedding(ctx, dbgen.UpdateEntityEmbeddingParams{
		EntityID:  uid,
		Embedding: pgVec(vec),
	})
}

// ---------------------------------------------------------------------------
// Tag operations
// ---------------------------------------------------------------------------

func (s *Store) SetTags(ctx context.Context, entityID string, tags []string) error {
	uid, err := uuid.Parse(entityID)
	if err != nil {
		return fmt.Errorf("parse entity id: %w", err)
	}
	return s.queries.SetEntityTags(ctx, dbgen.SetEntityTagsParams{
		EntityID: uid,
		Tags:     tags,
	})
}

func (s *Store) AddTags(ctx context.Context, entityID string, tags []string) error {
	uid, err := uuid.Parse(entityID)
	if err != nil {
		return fmt.Errorf("parse entity id: %w", err)
	}
	return s.queries.AddEntityTags(ctx, dbgen.AddEntityTagsParams{
		EntityID: uid,
		Tags:     tags,
	})
}

func (s *Store) RemoveTag(ctx context.Context, entityID string, tag string) error {
	uid, err := uuid.Parse(entityID)
	if err != nil {
		return fmt.Errorf("parse entity id: %w", err)
	}
	return s.queries.RemoveEntityTag(ctx, dbgen.RemoveEntityTagParams{
		EntityID: uid,
		Tag:      tag,
	})
}

// ---------------------------------------------------------------------------
// Read helpers
// ---------------------------------------------------------------------------

func (s *Store) GetEntity(ctx context.Context, id string) (matching.StoredEntity, error) {
	uid, err := uuid.Parse(id)
	if err != nil {
		return matching.StoredEntity{}, fmt.Errorf("parse entity id: %w", err)
	}
	row, err := s.queries.GetEntity(ctx, uid)
	if err != nil {
		return matching.StoredEntity{}, fmt.Errorf("get entity: %w", err)
	}
	return entityFromRow(row), nil
}

func (s *Store) GetEntitiesByType(ctx context.Context, entityType string) ([]matching.StoredEntity, error) {
	rows, err := s.queries.GetEntitiesByType(ctx, entityType)
	if err != nil {
		return nil, fmt.Errorf("get entities by type: %w", err)
	}
	result := make([]matching.StoredEntity, len(rows))
	for i, row := range rows {
		result[i] = entityFromRow(row)
	}
	return result, nil
}

func (s *Store) GetProvenanceForEntity(ctx context.Context, entityID string) ([]matching.ProvenanceEntry, error) {
	uid, err := uuid.Parse(entityID)
	if err != nil {
		return nil, fmt.Errorf("parse entity id: %w", err)
	}
	rows, err := s.queries.GetProvenanceForEntity(ctx, uid)
	if err != nil {
		return nil, fmt.Errorf("get provenance: %w", err)
	}
	result := make([]matching.ProvenanceEntry, len(rows))
	for i, row := range rows {
		result[i] = provenanceFromRow(row)
	}
	return result, nil
}

func (s *Store) GetRelationsFromEntity(ctx context.Context, entityID string) ([]matching.StoredRelation, error) {
	uid, err := uuid.Parse(entityID)
	if err != nil {
		return nil, fmt.Errorf("parse entity id: %w", err)
	}
	rows, err := s.queries.GetRelationsFromEntity(ctx, uid)
	if err != nil {
		return nil, fmt.Errorf("get relations from: %w", err)
	}
	result := make([]matching.StoredRelation, len(rows))
	for i, row := range rows {
		result[i] = relationFromRow(row)
	}
	return result, nil
}

func (s *Store) GetRelationsToEntity(ctx context.Context, entityID string) ([]matching.StoredRelation, error) {
	uid, err := uuid.Parse(entityID)
	if err != nil {
		return nil, fmt.Errorf("parse entity id: %w", err)
	}
	rows, err := s.queries.GetRelationsToEntity(ctx, uid)
	if err != nil {
		return nil, fmt.Errorf("get relations to: %w", err)
	}
	result := make([]matching.StoredRelation, len(rows))
	for i, row := range rows {
		result[i] = relationFromRow(row)
	}
	return result, nil
}

func (s *Store) ConnectedEntities(ctx context.Context, entityID string) ([]matching.StoredEntity, error) {
	uid, err := uuid.Parse(entityID)
	if err != nil {
		return nil, fmt.Errorf("parse entity id: %w", err)
	}
	rows, err := s.queries.ConnectedEntities(ctx, uid)
	if err != nil {
		return nil, fmt.Errorf("connected entities: %w", err)
	}
	result := make([]matching.StoredEntity, len(rows))
	for i, row := range rows {
		result[i] = entityFromRow(row)
	}
	return result, nil
}

func (s *Store) FindConnectedByType(ctx context.Context, entityID string, entityType string, relationTypes []string, filter *matching.QueryFilter) ([]matching.StoredEntity, error) {
	uid, err := uuid.Parse(entityID)
	if err != nil {
		return nil, fmt.Errorf("parse entity id: %w", err)
	}
	if relationTypes == nil {
		relationTypes = []string{}
	}
	rows, err := s.queries.FindConnectedByType(ctx, dbgen.FindConnectedByTypeParams{
		EntityID:      uid,
		EntityType:    entityType,
		RelationTypes: relationTypes,
		Tags:          tagsParam(filter),
	})
	if err != nil {
		return nil, fmt.Errorf("find connected by type: %w", err)
	}
	result := make([]matching.StoredEntity, len(rows))
	for i, row := range rows {
		result[i] = entityFromRow(row)
	}
	return result, nil
}

func (s *Store) FindEntitiesByRelation(ctx context.Context, entityType string, relationType string, filter *matching.QueryFilter) ([]matching.StoredEntity, error) {
	rows, err := s.queries.FindEntitiesByRelation(ctx, dbgen.FindEntitiesByRelationParams{
		EntityType:   entityType,
		RelationType: relationType,
		Tags:         tagsParam(filter),
	})
	if err != nil {
		return nil, fmt.Errorf("find entities by relation: %w", err)
	}
	result := make([]matching.StoredEntity, len(rows))
	for i, row := range rows {
		result[i] = entityFromRow(row)
	}
	return result, nil
}

// ---------------------------------------------------------------------------
// Conversion helpers
// ---------------------------------------------------------------------------

type entityRow interface {
	dbgen.FindByAnchorsRow | dbgen.FindByTokenOverlapRow | dbgen.FindByEmbeddingRow |
		dbgen.GetEntityRow | dbgen.GetEntitiesByTypeRow | dbgen.InsertEntityRow |
		dbgen.ConnectedEntitiesRow | dbgen.FindConnectedByTypeRow | dbgen.FindEntitiesByRelationRow
}

func entityFromRow[R entityRow](row R) matching.StoredEntity {
	switch r := any(row).(type) {
	case dbgen.FindByAnchorsRow:
		return toStoredEntity(r.ID, r.EntityType, r.Data, r.Confidence, r.Tags, r.CreatedAt, r.UpdatedAt)
	case dbgen.FindByTokenOverlapRow:
		return toStoredEntity(r.ID, r.EntityType, r.Data, r.Confidence, r.Tags, r.CreatedAt, r.UpdatedAt)
	case dbgen.FindByEmbeddingRow:
		return toStoredEntity(r.ID, r.EntityType, r.Data, r.Confidence, r.Tags, r.CreatedAt, r.UpdatedAt)
	case dbgen.GetEntityRow:
		return toStoredEntity(r.ID, r.EntityType, r.Data, r.Confidence, r.Tags, r.CreatedAt, r.UpdatedAt)
	case dbgen.GetEntitiesByTypeRow:
		return toStoredEntity(r.ID, r.EntityType, r.Data, r.Confidence, r.Tags, r.CreatedAt, r.UpdatedAt)
	case dbgen.InsertEntityRow:
		return toStoredEntity(r.ID, r.EntityType, r.Data, r.Confidence, r.Tags, r.CreatedAt, r.UpdatedAt)
	case dbgen.ConnectedEntitiesRow:
		return toStoredEntity(r.ID, r.EntityType, r.Data, r.Confidence, r.Tags, r.CreatedAt, r.UpdatedAt)
	case dbgen.FindConnectedByTypeRow:
		return toStoredEntity(r.ID, r.EntityType, r.Data, r.Confidence, r.Tags, r.CreatedAt, r.UpdatedAt)
	case dbgen.FindEntitiesByRelationRow:
		return toStoredEntity(r.ID, r.EntityType, r.Data, r.Confidence, r.Tags, r.CreatedAt, r.UpdatedAt)
	default:
		panic("unreachable")
	}
}

func toStoredEntity(id uuid.UUID, entityType string, data json.RawMessage, confidence float64, tags []string, createdAt, updatedAt time.Time) matching.StoredEntity {
	if tags == nil {
		tags = []string{}
	}
	return matching.StoredEntity{
		ID:         id.String(),
		EntityType: entityType,
		Data:       json.RawMessage(data),
		Confidence: confidence,
		Tags:       tags,
		CreatedAt:  createdAt,
		UpdatedAt:  updatedAt,
	}
}

func provenanceFromRow(row dbgen.EntityProvenance) matching.ProvenanceEntry {
	return matching.ProvenanceEntry{
		ID:              row.ID.String(),
		EntityID:        row.EntityID.String(),
		DocumentID:      row.DocumentID,
		ExtractedAt:     row.ExtractedAt,
		ModelID:         row.ModelID,
		Confidence:      row.Confidence,
		Fields:          row.Fields,
		MatchMethod:     row.MatchMethod,
		MatchConfidence: row.MatchConfidence,
	}
}

func relationFromRow(row dbgen.EntityRelation) matching.StoredRelation {
	rel := matching.StoredRelation{
		ID:           row.ID.String(),
		SourceID:     row.SourceID.String(),
		TargetID:     row.TargetID.String(),
		RelationType: row.RelationType,
		Confidence:   row.Confidence,
		Implied:      row.Implied,
		CreatedAt:    row.CreatedAt,
	}
	if row.Evidence.Valid {
		rel.Evidence = row.Evidence.String
	}
	if row.DocumentID.Valid {
		rel.DocumentID = row.DocumentID.String
	}
	if len(row.Data) > 0 && string(row.Data) != "{}" {
		_ = json.Unmarshal(row.Data, &rel.Data)
	}
	return rel
}

func pgVec(v []float32) pgvector.Vector {
	return pgvector.NewVector(v)
}
