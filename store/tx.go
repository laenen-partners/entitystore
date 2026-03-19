package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

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
