package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	eventsv1 "github.com/laenen-partners/entitystore/gen/entitystore/events/v1"
	"github.com/laenen-partners/entitystore/matching"
	"github.com/laenen-partners/entitystore/store/internal/dbgen"
)

// TxStore wraps a database transaction for atomic multi-step operations.
type TxStore struct {
	tx      pgx.Tx
	queries *dbgen.Queries
}

// Tx begins a new transaction and returns a TxStore.
func (s *Store) Tx(ctx context.Context) (*TxStore, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	return &TxStore{
		tx:      tx,
		queries: s.queries.WithTx(tx),
	}, nil
}

// Commit commits the transaction.
func (ts *TxStore) Commit(ctx context.Context) error {
	return ts.tx.Commit(ctx)
}

// Rollback aborts the transaction.
func (ts *TxStore) Rollback(ctx context.Context) error {
	return ts.tx.Rollback(ctx)
}

// ---------------------------------------------------------------------------
// Reads within transaction
// ---------------------------------------------------------------------------

// GetEntity returns a single entity by ID within the transaction.
func (ts *TxStore) GetEntity(ctx context.Context, id string) (matching.StoredEntity, error) {
	uid, err := uuid.Parse(id)
	if err != nil {
		return matching.StoredEntity{}, fmt.Errorf("parse entity id: %w", err)
	}
	row, err := ts.queries.GetEntity(ctx, uid)
	if err != nil {
		return matching.StoredEntity{}, fmt.Errorf("get entity: %w", err)
	}
	return entityFromRow(row), nil
}

// FindByAnchors searches for entities matching the given anchor values within the transaction.
func (ts *TxStore) FindByAnchors(ctx context.Context, entityType string, anchors []matching.AnchorQuery, filter *matching.QueryFilter) ([]matching.StoredEntity, error) {
	seen := make(map[uuid.UUID]struct{})
	var result []matching.StoredEntity
	tags := tagsParam(filter)
	anyTags := anyTagsParam(filter)

	for _, aq := range anchors {
		rows, err := ts.queries.FindByAnchors(ctx, dbgen.FindByAnchorsParams{
			EntityType:      entityType,
			AnchorField:     aq.Field,
			NormalizedValue: aq.Value,
			Tags:            tags,
			AnyTags:         anyTags,
			ExcludeTag:      excludeTagParam(filter),
			UnlessTags:      unlessTagsParam(filter),
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

// GetRelationsFromEntity returns outbound relations within the transaction.
func (ts *TxStore) GetRelationsFromEntity(ctx context.Context, entityID string, pageSize int32, cursor *time.Time) ([]matching.StoredRelation, error) {
	uid, err := uuid.Parse(entityID)
	if err != nil {
		return nil, fmt.Errorf("parse entity id: %w", err)
	}
	if pageSize <= 0 {
		pageSize = 1000
	}
	var pgCursor pgtype.Timestamptz
	if cursor != nil {
		pgCursor = pgtype.Timestamptz{Time: *cursor, Valid: true}
	}
	rows, err := ts.queries.GetRelationsFromEntity(ctx, dbgen.GetRelationsFromEntityParams{
		SourceID: uid,
		Cursor:   pgCursor,
		PageSize: pageSize,
	})
	if err != nil {
		return nil, fmt.Errorf("get relations from: %w", err)
	}
	result := make([]matching.StoredRelation, len(rows))
	for i, row := range rows {
		result[i] = relationFromRow(row)
	}
	return result, nil
}

// GetRelationsToEntity returns inbound relations within the transaction.
func (ts *TxStore) GetRelationsToEntity(ctx context.Context, entityID string, pageSize int32, cursor *time.Time) ([]matching.StoredRelation, error) {
	uid, err := uuid.Parse(entityID)
	if err != nil {
		return nil, fmt.Errorf("parse entity id: %w", err)
	}
	if pageSize <= 0 {
		pageSize = 1000
	}
	var pgCursor pgtype.Timestamptz
	if cursor != nil {
		pgCursor = pgtype.Timestamptz{Time: *cursor, Valid: true}
	}
	rows, err := ts.queries.GetRelationsToEntity(ctx, dbgen.GetRelationsToEntityParams{
		TargetID: uid,
		Cursor:   pgCursor,
		PageSize: pageSize,
	})
	if err != nil {
		return nil, fmt.Errorf("get relations to: %w", err)
	}
	result := make([]matching.StoredRelation, len(rows))
	for i, row := range rows {
		result[i] = relationFromRow(row)
	}
	return result, nil
}

// ---------------------------------------------------------------------------
// Writes within transaction
// ---------------------------------------------------------------------------

// WriteEntity applies a single entity write (create, update, or merge) in the
// current transaction.
func (ts *TxStore) WriteEntity(ctx context.Context, op *WriteEntityOp) (matching.StoredEntity, error) {
	return applyEntityWrite(ctx, ts.queries, op)
}

// UpsertRelation creates or updates a relation in the current transaction.
func (ts *TxStore) UpsertRelation(ctx context.Context, op *UpsertRelationOp) (matching.StoredRelation, error) {
	sr, err := toStoredRelation(op)
	if err != nil {
		return matching.StoredRelation{}, err
	}
	return upsertRelation(ctx, ts.queries, sr)
}

// DeleteRelationByKey removes a specific relation in the current transaction.
func (ts *TxStore) DeleteRelationByKey(ctx context.Context, sourceID, targetID, relationType string) error {
	sourceUID, err := uuid.Parse(sourceID)
	if err != nil {
		return fmt.Errorf("parse source id: %w", err)
	}
	targetUID, err := uuid.Parse(targetID)
	if err != nil {
		return fmt.Errorf("parse target id: %w", err)
	}
	if err := ts.queries.DeleteRelationByKey(ctx, dbgen.DeleteRelationByKeyParams{
		SourceID:     sourceUID,
		TargetID:     targetUID,
		RelationType: relationType,
	}); err != nil {
		return fmt.Errorf("delete relation: %w", err)
	}
	evt := &eventsv1.RelationDeleted{
		SourceId:     sourceID,
		TargetId:     targetID,
		RelationType: relationType,
	}
	rk := relationKeyStr(sourceUID, targetUID, relationType)
	return insertEvents(ctx, ts.queries, uuid.Nil, rk, nil, "", []proto.Message{evt})
}

// GetEventsForEntity returns events for the given entity within the transaction.
func (ts *TxStore) GetEventsForEntity(ctx context.Context, entityID string, opts *EventQueryOpts) ([]Event, error) {
	uid, err := uuid.Parse(entityID)
	if err != nil {
		return nil, fmt.Errorf("parse entity id: %w", err)
	}
	limit := int32(100)
	var eventTypes []string
	var since time.Time
	if opts != nil {
		if opts.Limit > 0 {
			limit = int32(opts.Limit)
		}
		if len(opts.EventTypes) > 0 {
			eventTypes = opts.EventTypes
		}
		if !opts.Since.IsZero() {
			since = opts.Since
		}
	}
	if eventTypes == nil {
		eventTypes = []string{}
	}
	rows, err := ts.queries.GetEventsForEntity(ctx, dbgen.GetEventsForEntityParams{
		EntityID:   pgtype.UUID{Bytes: uid, Valid: true},
		EventTypes: eventTypes,
		Since:      since,
		MaxResults: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("get events: %w", err)
	}
	result := make([]Event, len(rows))
	for i, row := range rows {
		result[i] = eventFromRow(row)
	}
	return result, nil
}

// UpdateRelationData updates the typed data on an existing relation in the current transaction.
func (ts *TxStore) UpdateRelationData(ctx context.Context, sourceID, targetID, relationType string, data proto.Message) (matching.StoredRelation, error) {
	sourceUID, err := uuid.Parse(sourceID)
	if err != nil {
		return matching.StoredRelation{}, fmt.Errorf("parse source id: %w", err)
	}
	targetUID, err := uuid.Parse(targetID)
	if err != nil {
		return matching.StoredRelation{}, fmt.Errorf("parse target id: %w", err)
	}

	var dataType string
	dataJSON := json.RawMessage("{}")
	if data != nil {
		dataType = string(proto.MessageName(data))
		b, err := protojson.Marshal(data)
		if err != nil {
			return matching.StoredRelation{}, fmt.Errorf("marshal relation data: %w", err)
		}
		dataJSON = b
	}

	row, err := ts.queries.UpdateRelationData(ctx, dbgen.UpdateRelationDataParams{
		SourceID:     sourceUID,
		TargetID:     targetUID,
		RelationType: relationType,
		DataType:     dataType,
		Data:         dataJSON,
	})
	if err != nil {
		return matching.StoredRelation{}, fmt.Errorf("update relation data: %w", err)
	}
	return relationFromRow(row), nil
}
