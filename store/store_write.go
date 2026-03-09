package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/laenen-partners/entitystore/matching"
	"github.com/laenen-partners/entitystore/store/internal/dbgen"
)

// WriteAction specifies the type of entity write operation.
type WriteAction int

const (
	WriteActionCreate WriteAction = 1
	WriteActionUpdate WriteAction = 2
	WriteActionMerge  WriteAction = 3
)

// WriteEntityOp describes a single entity write within a batch.
type WriteEntityOp struct {
	Action          WriteAction
	ID              string // Optional: client-generated UUID for create.
	EntityType      string
	Data            json.RawMessage
	Confidence      float64
	Tags            []string
	MatchedEntityID string // Required for update and merge.
	Anchors         []matching.AnchorQuery
	Tokens          map[string][]string
	Embedding       []float32
	Provenance      matching.ProvenanceEntry
}

// UpsertRelationOp describes a single relation upsert within a batch.
type UpsertRelationOp struct {
	SourceID     string
	TargetID     string
	RelationType string
	Confidence   float64
	Evidence     string
	Implied      bool
	SourceURN    string
	Data         map[string]any
}

// BatchWriteOp is a single operation in a batch — either an entity write or a relation upsert.
type BatchWriteOp struct {
	WriteEntity    *WriteEntityOp
	UpsertRelation *UpsertRelationOp
}

// BatchWriteResult is the result of a single operation in a batch.
type BatchWriteResult struct {
	Entity   *matching.StoredEntity
	Relation *matching.StoredRelation
}

// BatchWrite executes mixed entity writes and relation upserts in a single transaction.
func (s *Store) BatchWrite(ctx context.Context, ops []BatchWriteOp) ([]BatchWriteResult, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	q := s.queries.WithTx(tx)
	results := make([]BatchWriteResult, 0, len(ops))

	for i, op := range ops {
		switch {
		case op.WriteEntity != nil:
			ent, err := applyEntityWrite(ctx, q, op.WriteEntity)
			if err != nil {
				return nil, fmt.Errorf("op %d (write_entity): %w", i, err)
			}
			results = append(results, BatchWriteResult{Entity: &ent})

		case op.UpsertRelation != nil:
			rel, err := upsertRelation(ctx, q, toStoredRelation(op.UpsertRelation))
			if err != nil {
				return nil, fmt.Errorf("op %d (upsert_relation): %w", i, err)
			}
			results = append(results, BatchWriteResult{Relation: &rel})

		default:
			return nil, fmt.Errorf("op %d: empty operation", i)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("tx commit: %w", err)
	}
	return results, nil
}

// DeleteEntity removes an entity and its associated data.
func (s *Store) DeleteEntity(ctx context.Context, id string) error {
	uid, err := uuid.Parse(id)
	if err != nil {
		return fmt.Errorf("parse entity id: %w", err)
	}
	return s.queries.DeleteEntity(ctx, uid)
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

func toStoredRelation(op *UpsertRelationOp) matching.StoredRelation {
	return matching.StoredRelation{
		SourceID:     op.SourceID,
		TargetID:     op.TargetID,
		RelationType: op.RelationType,
		Confidence:   op.Confidence,
		Evidence:     op.Evidence,
		Implied:      op.Implied,
		SourceURN:    op.SourceURN,
		Data:         op.Data,
	}
}

// applyEntityWrite dispatches to create, update, or merge based on the action.
func applyEntityWrite(ctx context.Context, q *dbgen.Queries, op *WriteEntityOp) (matching.StoredEntity, error) {
	switch op.Action {
	case WriteActionCreate:
		return applyCreate(ctx, q, op)
	case WriteActionUpdate, WriteActionMerge:
		return applyUpdateOrMerge(ctx, q, op)
	default:
		return matching.StoredEntity{}, fmt.Errorf("unknown write action %d", op.Action)
	}
}

func applyCreate(ctx context.Context, q *dbgen.Queries, op *WriteEntityOp) (matching.StoredEntity, error) {
	tags := op.Tags
	if tags == nil {
		tags = []string{}
	}

	var entityID uuid.UUID
	var ent matching.StoredEntity

	if op.ID != "" {
		// Client-generated ID.
		uid, err := uuid.Parse(op.ID)
		if err != nil {
			return matching.StoredEntity{}, fmt.Errorf("parse client id: %w", err)
		}
		row, err := q.InsertEntityWithID(ctx, dbgen.InsertEntityWithIDParams{
			ID: uid, EntityType: op.EntityType,
			Data: op.Data, Confidence: op.Confidence, Tags: tags,
		})
		if err != nil {
			return matching.StoredEntity{}, fmt.Errorf("insert entity with id: %w", err)
		}
		entityID = row.ID
		ent = entityFromRow(row)
	} else {
		row, err := q.InsertEntity(ctx, dbgen.InsertEntityParams{
			EntityType: op.EntityType,
			Data:       op.Data,
			Confidence: op.Confidence,
			Tags:       tags,
		})
		if err != nil {
			return matching.StoredEntity{}, fmt.Errorf("insert entity: %w", err)
		}
		entityID = row.ID
		ent = entityFromRow(row)
	}

	if err := upsertAnchors(ctx, q, entityID, op.EntityType, op.Anchors); err != nil {
		return matching.StoredEntity{}, err
	}
	if err := upsertTokens(ctx, q, entityID, op.EntityType, op.Tokens); err != nil {
		return matching.StoredEntity{}, err
	}
	if err := insertProvenance(ctx, q, entityID, op.Provenance); err != nil {
		return matching.StoredEntity{}, err
	}
	if err := updateEmbedding(ctx, q, entityID, op.Embedding); err != nil {
		return matching.StoredEntity{}, err
	}

	return ent, nil
}

func applyUpdateOrMerge(ctx context.Context, q *dbgen.Queries, op *WriteEntityOp) (matching.StoredEntity, error) {
	uid, err := uuid.Parse(op.MatchedEntityID)
	if err != nil {
		return matching.StoredEntity{}, fmt.Errorf("parse entity id: %w", err)
	}

	switch op.Action {
	case WriteActionUpdate:
		if err := q.UpdateEntityData(ctx, dbgen.UpdateEntityDataParams{
			ID: uid, Data: op.Data, Confidence: op.Confidence,
		}); err != nil {
			return matching.StoredEntity{}, fmt.Errorf("update entity: %w", err)
		}
	case WriteActionMerge:
		if err := q.MergeEntityData(ctx, dbgen.MergeEntityDataParams{
			ID: uid, Data: op.Data, Confidence: op.Confidence,
		}); err != nil {
			return matching.StoredEntity{}, fmt.Errorf("merge entity: %w", err)
		}
	}

	if len(op.Tags) > 0 {
		if err := q.AddEntityTags(ctx, dbgen.AddEntityTagsParams{
			EntityID: uid, Tags: op.Tags,
		}); err != nil {
			return matching.StoredEntity{}, fmt.Errorf("add tags: %w", err)
		}
	}

	if err := upsertAnchors(ctx, q, uid, op.EntityType, op.Anchors); err != nil {
		return matching.StoredEntity{}, err
	}
	if err := upsertTokens(ctx, q, uid, op.EntityType, op.Tokens); err != nil {
		return matching.StoredEntity{}, err
	}
	if err := insertProvenance(ctx, q, uid, op.Provenance); err != nil {
		return matching.StoredEntity{}, err
	}
	if err := updateEmbedding(ctx, q, uid, op.Embedding); err != nil {
		return matching.StoredEntity{}, err
	}

	row, err := q.GetEntity(ctx, uid)
	if err != nil {
		return matching.StoredEntity{}, fmt.Errorf("get entity: %w", err)
	}
	return entityFromRow(row), nil
}

func upsertAnchors(ctx context.Context, q *dbgen.Queries, entityID uuid.UUID, entityType string, anchors []matching.AnchorQuery) error {
	for _, aq := range anchors {
		if err := q.UpsertAnchor(ctx, dbgen.UpsertAnchorParams{
			EntityID: entityID, EntityType: entityType,
			AnchorField: aq.Field, NormalizedValue: aq.Value,
		}); err != nil {
			return fmt.Errorf("upsert anchor %s=%s: %w", aq.Field, aq.Value, err)
		}
	}
	return nil
}

func upsertTokens(ctx context.Context, q *dbgen.Queries, entityID uuid.UUID, entityType string, tokens map[string][]string) error {
	for field, toks := range tokens {
		if err := q.UpsertTokens(ctx, dbgen.UpsertTokensParams{
			EntityID: entityID, EntityType: entityType,
			TokenField: field, Tokens: toks,
		}); err != nil {
			return fmt.Errorf("upsert tokens %s: %w", field, err)
		}
	}
	return nil
}

func insertProvenance(ctx context.Context, q *dbgen.Queries, entityID uuid.UUID, prov matching.ProvenanceEntry) error {
	if prov.SourceURN == "" {
		return nil
	}
	if prov.ExtractedAt.IsZero() {
		prov.ExtractedAt = time.Now()
	}
	if prov.Fields == nil {
		prov.Fields = []string{}
	}
	if _, err := q.InsertProvenance(ctx, dbgen.InsertProvenanceParams{
		EntityID: entityID, SourceUrn: prov.SourceURN,
		ExtractedAt: prov.ExtractedAt, ModelID: prov.ModelID,
		Confidence: prov.Confidence, Fields: prov.Fields,
		MatchMethod: prov.MatchMethod, MatchConfidence: prov.MatchConfidence,
	}); err != nil {
		return fmt.Errorf("insert provenance: %w", err)
	}
	return nil
}

func updateEmbedding(ctx context.Context, q *dbgen.Queries, entityID uuid.UUID, embedding []float32) error {
	if embedding == nil {
		return nil
	}
	if err := q.UpdateEntityEmbedding(ctx, dbgen.UpdateEntityEmbeddingParams{
		EntityID: entityID, Embedding: pgVec(embedding),
	}); err != nil {
		return fmt.Errorf("update embedding: %w", err)
	}
	return nil
}

func upsertRelation(ctx context.Context, q *dbgen.Queries, rel matching.StoredRelation) (matching.StoredRelation, error) {
	sourceUID, err := uuid.Parse(rel.SourceID)
	if err != nil {
		return matching.StoredRelation{}, fmt.Errorf("parse source id: %w", err)
	}
	targetUID, err := uuid.Parse(rel.TargetID)
	if err != nil {
		return matching.StoredRelation{}, fmt.Errorf("parse target id: %w", err)
	}

	evidence := pgtype.Text{}
	if rel.Evidence != "" {
		evidence = pgtype.Text{String: rel.Evidence, Valid: true}
	}
	srcURN := pgtype.Text{}
	if rel.SourceURN != "" {
		srcURN = pgtype.Text{String: rel.SourceURN, Valid: true}
	}

	dataJSON := json.RawMessage("{}")
	if len(rel.Data) > 0 {
		b, err := json.Marshal(rel.Data)
		if err != nil {
			return matching.StoredRelation{}, fmt.Errorf("marshal relation data: %w", err)
		}
		dataJSON = b
	}

	row, err := q.UpsertRelation(ctx, dbgen.UpsertRelationParams{
		SourceID:     sourceUID,
		TargetID:     targetUID,
		RelationType: rel.RelationType,
		Confidence:   rel.Confidence,
		Evidence:     evidence,
		Implied:      rel.Implied,
		SourceUrn:    srcURN,
		Data:         dataJSON,
	})
	if err != nil {
		return matching.StoredRelation{}, fmt.Errorf("upsert relation: %w", err)
	}
	return relationFromRow(row), nil
}
