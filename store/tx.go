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

// ApplyMatchDecision creates an entity with anchors, tokens, provenance, and
// embedding in the current transaction.
func (ts *TxStore) ApplyMatchDecision(ctx context.Context, input MatchDecisionInput) (matching.StoredEntity, error) {
	return applyMatchDecision(ctx, ts.queries, input)
}

// ResolveEntity applies a match decision (create, update, or merge) in the
// current transaction.
func (ts *TxStore) ResolveEntity(ctx context.Context, action string, entityID string, input MatchDecisionInput) (matching.StoredEntity, error) {
	switch action {
	case "create":
		return applyMatchDecision(ctx, ts.queries, input)
	case "update", "merge":
		return applyUpdateOrMerge(ctx, ts.queries, action, entityID, input)
	default:
		return matching.StoredEntity{}, fmt.Errorf("resolve entity: unknown action %q", action)
	}
}

// UpsertRelation creates or updates a relation in the current transaction.
func (ts *TxStore) UpsertRelation(ctx context.Context, rel matching.StoredRelation) (matching.StoredRelation, error) {
	return upsertRelation(ctx, ts.queries, rel)
}

