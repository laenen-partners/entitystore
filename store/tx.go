package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

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
	return ts.queries.DeleteRelationByKey(ctx, dbgen.DeleteRelationByKeyParams{
		SourceID:     sourceUID,
		TargetID:     targetUID,
		RelationType: relationType,
	})
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
