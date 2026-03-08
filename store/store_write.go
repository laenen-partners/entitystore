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

func (s *Store) InsertEntity(ctx context.Context, entityType string, data json.RawMessage, confidence float64) (matching.StoredEntity, error) {
	row, err := s.queries.InsertEntity(ctx, dbgen.InsertEntityParams{
		EntityType: entityType,
		Data:       data,
		Confidence: confidence,
		Tags:       []string{},
	})
	if err != nil {
		return matching.StoredEntity{}, fmt.Errorf("insert entity: %w", err)
	}
	return entityFromRow(row), nil
}

func (s *Store) UpdateEntity(ctx context.Context, id string, data json.RawMessage, confidence float64) error {
	uid, err := uuid.Parse(id)
	if err != nil {
		return fmt.Errorf("parse entity id: %w", err)
	}
	return s.queries.UpdateEntityData(ctx, dbgen.UpdateEntityDataParams{
		ID:         uid,
		Data:       data,
		Confidence: confidence,
	})
}

func (s *Store) MergeEntity(ctx context.Context, id string, data json.RawMessage, confidence float64) error {
	uid, err := uuid.Parse(id)
	if err != nil {
		return fmt.Errorf("parse entity id: %w", err)
	}
	return s.queries.MergeEntityData(ctx, dbgen.MergeEntityDataParams{
		ID:         uid,
		Data:       data,
		Confidence: confidence,
	})
}

func (s *Store) DeleteEntity(ctx context.Context, id string) error {
	uid, err := uuid.Parse(id)
	if err != nil {
		return fmt.Errorf("parse entity id: %w", err)
	}
	return s.queries.DeleteEntity(ctx, uid)
}

func (s *Store) UpsertAnchors(ctx context.Context, entityID string, entityType string, anchors []matching.AnchorQuery) error {
	uid, err := uuid.Parse(entityID)
	if err != nil {
		return fmt.Errorf("parse entity id: %w", err)
	}
	for _, aq := range anchors {
		if err := s.queries.UpsertAnchor(ctx, dbgen.UpsertAnchorParams{
			EntityID:        uid,
			EntityType:      entityType,
			AnchorField:     aq.Field,
			NormalizedValue: aq.Value,
		}); err != nil {
			return fmt.Errorf("upsert anchor %s=%s: %w", aq.Field, aq.Value, err)
		}
	}
	return nil
}

func (s *Store) UpsertTokens(ctx context.Context, entityID string, entityType string, tokenField string, tokens []string) error {
	uid, err := uuid.Parse(entityID)
	if err != nil {
		return fmt.Errorf("parse entity id: %w", err)
	}
	return s.queries.UpsertTokens(ctx, dbgen.UpsertTokensParams{
		EntityID:   uid,
		EntityType: entityType,
		TokenField: tokenField,
		Tokens:     tokens,
	})
}

func (s *Store) InsertProvenance(ctx context.Context, p matching.ProvenanceEntry) (matching.ProvenanceEntry, error) {
	uid, err := uuid.Parse(p.EntityID)
	if err != nil {
		return matching.ProvenanceEntry{}, fmt.Errorf("parse entity id: %w", err)
	}
	row, err := s.queries.InsertProvenance(ctx, dbgen.InsertProvenanceParams{
		EntityID:        uid,
		DocumentID:      p.DocumentID,
		ExtractedAt:     p.ExtractedAt,
		ModelID:         p.ModelID,
		Confidence:      p.Confidence,
		Fields:          p.Fields,
		MatchMethod:     p.MatchMethod,
		MatchConfidence: p.MatchConfidence,
	})
	if err != nil {
		return matching.ProvenanceEntry{}, fmt.Errorf("insert provenance: %w", err)
	}
	return provenanceFromRow(row), nil
}

func (s *Store) UpsertRelation(ctx context.Context, rel matching.StoredRelation) (matching.StoredRelation, error) {
	return upsertRelation(ctx, s.queries, rel)
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
	docID := pgtype.Text{}
	if rel.DocumentID != "" {
		docID = pgtype.Text{String: rel.DocumentID, Valid: true}
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
		DocumentID:   docID,
		Data:         dataJSON,
	})
	if err != nil {
		return matching.StoredRelation{}, fmt.Errorf("upsert relation: %w", err)
	}
	return relationFromRow(row), nil
}

// ---------------------------------------------------------------------------
// Transactional operations
// ---------------------------------------------------------------------------

// MatchDecisionInput bundles the data needed to apply a match decision atomically.
type MatchDecisionInput struct {
	EntityType string
	Data       json.RawMessage
	Confidence float64
	Tags       []string
	Anchors    []matching.AnchorQuery
	Tokens     map[string][]string
	Provenance matching.ProvenanceEntry
	Embedding  []float32
}

func (s *Store) ApplyMatchDecision(ctx context.Context, input MatchDecisionInput) (matching.StoredEntity, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return matching.StoredEntity{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	stored, err := applyMatchDecision(ctx, s.queries.WithTx(tx), input)
	if err != nil {
		return matching.StoredEntity{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return matching.StoredEntity{}, fmt.Errorf("tx commit: %w", err)
	}

	return stored, nil
}

func applyMatchDecision(ctx context.Context, q *dbgen.Queries, input MatchDecisionInput) (matching.StoredEntity, error) {
	tags := input.Tags
	if tags == nil {
		tags = []string{}
	}

	row, err := q.InsertEntity(ctx, dbgen.InsertEntityParams{
		EntityType: input.EntityType,
		Data:       input.Data,
		Confidence: input.Confidence,
		Tags:       tags,
	})
	if err != nil {
		return matching.StoredEntity{}, fmt.Errorf("tx insert entity: %w", err)
	}

	for _, aq := range input.Anchors {
		if err := q.UpsertAnchor(ctx, dbgen.UpsertAnchorParams{
			EntityID:        row.ID,
			EntityType:      input.EntityType,
			AnchorField:     aq.Field,
			NormalizedValue: aq.Value,
		}); err != nil {
			return matching.StoredEntity{}, fmt.Errorf("tx upsert anchor: %w", err)
		}
	}

	for field, toks := range input.Tokens {
		if err := q.UpsertTokens(ctx, dbgen.UpsertTokensParams{
			EntityID:   row.ID,
			EntityType: input.EntityType,
			TokenField: field,
			Tokens:     toks,
		}); err != nil {
			return matching.StoredEntity{}, fmt.Errorf("tx upsert tokens: %w", err)
		}
	}

	prov := input.Provenance
	if prov.ExtractedAt.IsZero() {
		prov.ExtractedAt = time.Now()
	}
	if _, err := q.InsertProvenance(ctx, dbgen.InsertProvenanceParams{
		EntityID:        row.ID,
		DocumentID:      prov.DocumentID,
		ExtractedAt:     prov.ExtractedAt,
		ModelID:         prov.ModelID,
		Confidence:      prov.Confidence,
		Fields:          prov.Fields,
		MatchMethod:     prov.MatchMethod,
		MatchConfidence: prov.MatchConfidence,
	}); err != nil {
		return matching.StoredEntity{}, fmt.Errorf("tx insert provenance: %w", err)
	}

	if input.Embedding != nil {
		if err := q.UpdateEntityEmbedding(ctx, dbgen.UpdateEntityEmbeddingParams{
			EntityID:  row.ID,
			Embedding: pgVec(input.Embedding),
		}); err != nil {
			return matching.StoredEntity{}, fmt.Errorf("tx update embedding: %w", err)
		}
	}

	return entityFromRow(row), nil
}
