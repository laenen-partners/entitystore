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
		SourceUrn:      p.SourceURN,
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
		SourceUrn:   srcURN,
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

// ResolveEntity applies a match decision atomically. It supports three actions:
//   - "create": inserts a new entity with anchors, tokens, provenance, and embedding.
//   - "update": replaces the matched entity's data, then updates indexes and provenance.
//   - "merge":  merges new fields into the matched entity's data (JSON ||), then updates indexes and provenance.
func (s *Store) ResolveEntity(ctx context.Context, action string, entityID string, input MatchDecisionInput) (matching.StoredEntity, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return matching.StoredEntity{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	q := s.queries.WithTx(tx)

	var stored matching.StoredEntity
	switch action {
	case "create":
		stored, err = applyMatchDecision(ctx, q, input)
	case "update", "merge":
		stored, err = applyUpdateOrMerge(ctx, q, action, entityID, input)
	default:
		return matching.StoredEntity{}, fmt.Errorf("resolve entity: unknown action %q", action)
	}
	if err != nil {
		return matching.StoredEntity{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return matching.StoredEntity{}, fmt.Errorf("tx commit: %w", err)
	}
	return stored, nil
}

func applyUpdateOrMerge(ctx context.Context, q *dbgen.Queries, action, entityID string, input MatchDecisionInput) (matching.StoredEntity, error) {
	uid, err := uuid.Parse(entityID)
	if err != nil {
		return matching.StoredEntity{}, fmt.Errorf("parse entity id: %w", err)
	}

	switch action {
	case "update":
		if err := q.UpdateEntityData(ctx, dbgen.UpdateEntityDataParams{
			ID: uid, Data: input.Data, Confidence: input.Confidence,
		}); err != nil {
			return matching.StoredEntity{}, fmt.Errorf("tx update entity: %w", err)
		}
	case "merge":
		if err := q.MergeEntityData(ctx, dbgen.MergeEntityDataParams{
			ID: uid, Data: input.Data, Confidence: input.Confidence,
		}); err != nil {
			return matching.StoredEntity{}, fmt.Errorf("tx merge entity: %w", err)
		}
	}

	// Update tags if provided.
	if len(input.Tags) > 0 {
		if err := q.AddEntityTags(ctx, dbgen.AddEntityTagsParams{
			EntityID: uid, Tags: input.Tags,
		}); err != nil {
			return matching.StoredEntity{}, fmt.Errorf("tx add tags: %w", err)
		}
	}

	// Update anchors.
	for _, aq := range input.Anchors {
		if err := q.UpsertAnchor(ctx, dbgen.UpsertAnchorParams{
			EntityID: uid, EntityType: input.EntityType,
			AnchorField: aq.Field, NormalizedValue: aq.Value,
		}); err != nil {
			return matching.StoredEntity{}, fmt.Errorf("tx upsert anchor: %w", err)
		}
	}

	// Update tokens.
	for field, toks := range input.Tokens {
		if err := q.UpsertTokens(ctx, dbgen.UpsertTokensParams{
			EntityID: uid, EntityType: input.EntityType,
			TokenField: field, Tokens: toks,
		}); err != nil {
			return matching.StoredEntity{}, fmt.Errorf("tx upsert tokens: %w", err)
		}
	}

	// Insert provenance.
	prov := input.Provenance
	if prov.SourceURN != "" {
		if prov.ExtractedAt.IsZero() {
			prov.ExtractedAt = time.Now()
		}
		if _, err := q.InsertProvenance(ctx, dbgen.InsertProvenanceParams{
			EntityID: uid, SourceUrn: prov.SourceURN,
			ExtractedAt: prov.ExtractedAt, ModelID: prov.ModelID,
			Confidence: prov.Confidence, Fields: prov.Fields,
			MatchMethod: prov.MatchMethod, MatchConfidence: prov.MatchConfidence,
		}); err != nil {
			return matching.StoredEntity{}, fmt.Errorf("tx insert provenance: %w", err)
		}
	}

	// Update embedding.
	if input.Embedding != nil {
		if err := q.UpdateEntityEmbedding(ctx, dbgen.UpdateEntityEmbeddingParams{
			EntityID: uid, Embedding: pgVec(input.Embedding),
		}); err != nil {
			return matching.StoredEntity{}, fmt.Errorf("tx update embedding: %w", err)
		}
	}

	// Fetch and return the updated entity.
	row, err := q.GetEntity(ctx, uid)
	if err != nil {
		return matching.StoredEntity{}, fmt.Errorf("tx get entity: %w", err)
	}
	return entityFromRow(row), nil
}

// BatchInsertEntities inserts multiple entities in a single transaction.
func (s *Store) BatchInsertEntities(ctx context.Context, items []BatchInsertItem) ([]matching.StoredEntity, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	q := s.queries.WithTx(tx)
	result := make([]matching.StoredEntity, 0, len(items))
	for _, item := range items {
		tags := item.Tags
		if tags == nil {
			tags = []string{}
		}
		row, err := q.InsertEntity(ctx, dbgen.InsertEntityParams{
			EntityType: item.EntityType,
			Data:       item.Data,
			Confidence: item.Confidence,
			Tags:       tags,
		})
		if err != nil {
			return nil, fmt.Errorf("batch insert entity: %w", err)
		}
		result = append(result, entityFromRow(row))
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("tx commit: %w", err)
	}
	return result, nil
}

// BatchInsertItem holds the data for a single entity in a batch insert.
type BatchInsertItem struct {
	EntityType string
	Data       json.RawMessage
	Confidence float64
	Tags       []string
}

// BatchResolveEntities resolves multiple entities in a single transaction.
func (s *Store) BatchResolveEntities(ctx context.Context, items []BatchResolveItem) ([]matching.StoredEntity, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	q := s.queries.WithTx(tx)
	result := make([]matching.StoredEntity, 0, len(items))
	for i, item := range items {
		var stored matching.StoredEntity
		var err error
		switch item.Action {
		case "create":
			stored, err = applyMatchDecision(ctx, q, item.Input)
		case "update", "merge":
			stored, err = applyUpdateOrMerge(ctx, q, item.Action, item.MatchedEntityID, item.Input)
		default:
			return nil, fmt.Errorf("batch resolve item %d: unknown action %q", i, item.Action)
		}
		if err != nil {
			return nil, fmt.Errorf("batch resolve item %d: %w", i, err)
		}
		result = append(result, stored)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("tx commit: %w", err)
	}
	return result, nil
}

// BatchResolveItem holds the data for a single entity resolution in a batch.
type BatchResolveItem struct {
	Action          string
	MatchedEntityID string
	Input           MatchDecisionInput
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
		SourceUrn:      prov.SourceURN,
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
