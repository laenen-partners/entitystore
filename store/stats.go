package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"

	eventsv1 "github.com/laenen-partners/entitystore/gen/entitystore/events/v1"
	"github.com/laenen-partners/entitystore/store/internal/dbgen"
)

// TypeCount represents the count of entities or relations for a given type.
type TypeCount struct {
	Type  string
	Count int64
}

// CountEntitiesByType returns the number of non-deleted entities of the given type.
func (s *Store) CountEntitiesByType(ctx context.Context, entityType string) (int64, error) {
	count, err := s.queries.CountEntitiesByType(ctx, entityType)
	if err != nil {
		return 0, fmt.Errorf("count entities by type: %w", err)
	}
	return count, nil
}

// CountAllEntities returns the total number of non-deleted entities.
func (s *Store) CountAllEntities(ctx context.Context) (int64, error) {
	count, err := s.queries.CountAllEntities(ctx)
	if err != nil {
		return 0, fmt.Errorf("count all entities: %w", err)
	}
	return count, nil
}

// CountRelationsForEntity returns the number of relations (inbound + outbound) for an entity.
func (s *Store) CountRelationsForEntity(ctx context.Context, entityID string) (int64, error) {
	uid, err := uuid.Parse(entityID)
	if err != nil {
		return 0, fmt.Errorf("parse entity id: %w", err)
	}
	count, err := s.queries.CountRelationsForEntity(ctx, uid)
	if err != nil {
		return 0, fmt.Errorf("count relations for entity: %w", err)
	}
	return count, nil
}

// CountAllRelations returns the total number of relations.
func (s *Store) CountAllRelations(ctx context.Context) (int64, error) {
	count, err := s.queries.CountAllRelations(ctx)
	if err != nil {
		return 0, fmt.Errorf("count all relations: %w", err)
	}
	return count, nil
}

// CountEntityTypes returns the count of entities per entity type.
func (s *Store) CountEntityTypes(ctx context.Context) ([]TypeCount, error) {
	rows, err := s.queries.CountEntityTypes(ctx)
	if err != nil {
		return nil, fmt.Errorf("count entity types: %w", err)
	}
	result := make([]TypeCount, len(rows))
	for i, row := range rows {
		result[i] = TypeCount{Type: row.EntityType, Count: row.Count}
	}
	return result, nil
}

// CountRelationTypes returns the count of relations per relation type.
func (s *Store) CountRelationTypes(ctx context.Context) ([]TypeCount, error) {
	rows, err := s.queries.CountRelationTypes(ctx)
	if err != nil {
		return nil, fmt.Errorf("count relation types: %w", err)
	}
	result := make([]TypeCount, len(rows))
	for i, row := range rows {
		result[i] = TypeCount{Type: row.RelationType, Count: row.Count}
	}
	return result, nil
}

// CountSoftDeleted returns the number of soft-deleted entities.
func (s *Store) CountSoftDeleted(ctx context.Context) (int64, error) {
	count, err := s.queries.CountSoftDeleted(ctx)
	if err != nil {
		return 0, fmt.Errorf("count soft deleted: %w", err)
	}
	return count, nil
}

// StoreStats contains aggregate statistics about the store contents.
type StoreStats struct {
	TotalEntities  int64
	TotalRelations int64
	SoftDeleted    int64
	EntityTypes    []TypeCount
	RelationTypes  []TypeCount
}

// Stats returns aggregate statistics about the store contents.
func (s *Store) Stats(ctx context.Context) (StoreStats, error) {
	var stats StoreStats
	var err error

	stats.TotalEntities, err = s.CountAllEntities(ctx)
	if err != nil {
		return stats, err
	}
	stats.TotalRelations, err = s.CountAllRelations(ctx)
	if err != nil {
		return stats, err
	}
	stats.SoftDeleted, err = s.CountSoftDeleted(ctx)
	if err != nil {
		return stats, err
	}
	stats.EntityTypes, err = s.CountEntityTypes(ctx)
	if err != nil {
		return stats, err
	}
	stats.RelationTypes, err = s.CountRelationTypes(ctx)
	if err != nil {
		return stats, err
	}
	return stats, nil
}

// HardDeleteEntity permanently removes an entity and all its associated data.
// Use this for cleanup of soft-deleted entities. For normal deletion, use DeleteEntity.
func (s *Store) HardDeleteEntity(ctx context.Context, id string) error {
	uid, err := uuid.Parse(id)
	if err != nil {
		return fmt.Errorf("parse entity id: %w", err)
	}

	doHardDelete := func(q *dbgen.Queries) error {
		// Emit event BEFORE the hard delete (CASCADE would remove related data).
		// Use GetEntityIncludingDeleted to capture soft-deleted entities too.
		row, getErr := q.GetEntity(ctx, uid)
		var entityType string
		var tags []string
		if getErr == nil {
			entityType = row.EntityType
			tags = row.Tags
		}
		evt := &eventsv1.EntityHardDeleted{
			EntityId:   id,
			EntityType: entityType,
		}
		if err := insertEvents(ctx, q, uid, "", tags, entityType, []proto.Message{evt}); err != nil {
			return fmt.Errorf("insert hard delete event: %w", err)
		}
		return q.HardDeleteEntity(ctx, uid)
	}

	if s.tx != nil {
		return doHardDelete(s.queries)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := doHardDelete(s.queries.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// unexported helper to check generated types match
var _ = dbgen.CountEntityTypesRow{}
var _ = dbgen.CountRelationTypesRow{}
