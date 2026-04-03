package store

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	eventsv1 "github.com/laenen-partners/entitystore/gen/entitystore/events/v1"
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
// EntityType is derived automatically from Data via proto.MessageName().
type WriteEntityOp struct {
	Action          WriteAction
	ID              string        // Optional: client-generated UUID for create.
	Data            proto.Message // EntityType is derived from the proto message name.
	Confidence      float64
	Tags            []string
	MatchedEntityID string // Required for update and merge.
	Anchors         []matching.AnchorQuery
	Tokens          map[string][]string
	Embedding       []float32
	DisplayName     string
	Version         int // Required for update and merge. Ignored for create.
	Events          []proto.Message
}

// UpsertRelationOp describes a single relation upsert within a batch.
// DataType is derived automatically from Data via proto.MessageName().
type UpsertRelationOp struct {
	SourceID     string
	TargetID     string
	RelationType string
	Confidence   float64
	Evidence     string
	Implied      bool
	SourceURN    string
	Data         proto.Message // Optional: typed relation payload. DataType is derived automatically.
	Events       []proto.Message
}

// PreCondition is a check evaluated inside the BatchWrite transaction before
// applying the associated operation. If any precondition fails, the entire
// batch is rolled back.
type PreCondition struct {
	// What to look up.
	EntityType string
	Anchors    []matching.AnchorQuery

	// What to assert.
	MustExist    bool   // true → fail if no entity matches
	MustNotExist bool   // true → fail if any entity matches (uniqueness)
	TagRequired  string // if set, matched entity must carry this tag
	TagForbidden string // if set, matched entity must NOT carry this tag
}

// PreConditionError is returned when a precondition check fails during BatchWrite.
type PreConditionError struct {
	OpIndex   int          // which BatchWriteOp failed
	Condition PreCondition // the failing precondition
	Violation string       // "not_found", "already_exists", "tag_required", "tag_forbidden"
}

func (e *PreConditionError) Error() string {
	return fmt.Sprintf("precondition failed on op %d: %s for %s",
		e.OpIndex, e.Violation, e.Condition.EntityType)
}

// BatchWriteOp is a single operation in a batch — either an entity write or a relation upsert.
type BatchWriteOp struct {
	WriteEntity    *WriteEntityOp
	UpsertRelation *UpsertRelationOp
	PreConditions  []PreCondition // checked before applying this op
}

// BatchWriteResult is the result of a single operation in a batch.
type BatchWriteResult struct {
	Entity   *matching.StoredEntity
	Relation *matching.StoredRelation
}

// BatchWrite executes mixed entity writes and relation upserts in a single transaction.
// If the Store was created with WithTx, operations execute within that external
// transaction (the caller is responsible for commit/rollback).
func (s *Store) BatchWrite(ctx context.Context, ops []BatchWriteOp) ([]BatchWriteResult, error) {
	if len(ops) > MaxBatchSize {
		return nil, fmt.Errorf("batch size %d exceeds maximum %d", len(ops), MaxBatchSize)
	}
	for i, op := range ops {
		if op.WriteEntity != nil {
			if err := validateTags(op.WriteEntity.Tags); err != nil {
				return nil, fmt.Errorf("op %d: %w", i, err)
			}
		}
		if op.UpsertRelation != nil {
			if err := validateRelationType(op.UpsertRelation.RelationType); err != nil {
				return nil, fmt.Errorf("op %d: %w", i, err)
			}
		}
	}
	s.log.DebugContext(ctx, "BatchWrite", "ops", len(ops))
	if s.tx != nil {
		// Operating within an external transaction — use it directly.
		return s.executeBatchOps(ctx, s.queries, ops)
	}

	// Normal path — begin and manage our own transaction.
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	results, err := s.executeBatchOps(ctx, s.queries.WithTx(tx), ops)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("tx commit: %w", err)
	}
	return results, nil
}

func (s *Store) executeBatchOps(ctx context.Context, q *dbgen.Queries, ops []BatchWriteOp) ([]BatchWriteResult, error) {
	results := make([]BatchWriteResult, 0, len(ops))

	for i, op := range ops {
		if err := evaluatePreConditions(ctx, q, i, op.PreConditions); err != nil {
			return nil, err
		}

		switch {
		case op.WriteEntity != nil:
			ent, err := applyEntityWrite(ctx, q, op.WriteEntity)
			if err != nil {
				return nil, fmt.Errorf("op %d (write_entity): %w", i, err)
			}
			results = append(results, BatchWriteResult{Entity: &ent})

		case op.UpsertRelation != nil:
			sr, err := toStoredRelation(op.UpsertRelation)
			if err != nil {
				return nil, fmt.Errorf("op %d (upsert_relation): %w", i, err)
			}
			rel, err := upsertRelation(ctx, q, sr)
			if err != nil {
				return nil, fmt.Errorf("op %d (upsert_relation): %w", i, err)
			}
			// Emit standard RelationCreated event + any caller-defined events.
			relOp := op.UpsertRelation
			stdEvent := &eventsv1.RelationCreated{
				SourceId:     rel.SourceID,
				TargetId:     rel.TargetID,
				RelationType: rel.RelationType,
				Confidence:   rel.Confidence,
			}
			sourceUID, _ := uuid.Parse(rel.SourceID)
			targetUID, _ := uuid.Parse(rel.TargetID)
			rk := relationKeyStr(sourceUID, targetUID, rel.RelationType)
			allRelEvents := append([]proto.Message{stdEvent}, relOp.Events...)
			if err := insertEvents(ctx, q, uuid.Nil, rk, nil, allRelEvents); err != nil {
				return nil, fmt.Errorf("op %d (upsert_relation events): %w", i, err)
			}
			results = append(results, BatchWriteResult{Relation: &rel})

		default:
			return nil, fmt.Errorf("op %d: empty operation", i)
		}
	}

	return results, nil
}

// DeleteEntity soft-deletes an entity (sets deleted_at).
func (s *Store) DeleteEntity(ctx context.Context, id string) error {
	uid, err := uuid.Parse(id)
	if err != nil {
		return fmt.Errorf("parse entity id: %w", err)
	}

	doDelete := func(q *dbgen.Queries) error {
		// Fetch entity type for the event before deleting.
		row, err := q.GetEntity(ctx, uid)
		if err != nil {
			return fmt.Errorf("get entity for delete event: %w", err)
		}
		if err := q.DeleteEntity(ctx, uid); err != nil {
			return fmt.Errorf("delete entity: %w", err)
		}
		evt := &eventsv1.EntityDeleted{
			EntityId:   id,
			EntityType: row.EntityType,
		}
		return insertEvents(ctx, q, uid, "", row.Tags, []proto.Message{evt})
	}

	if s.tx != nil {
		return doDelete(s.queries)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := doDelete(s.queries.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// DeleteRelationByKey removes a specific relation by source, target, and type.
func (s *Store) DeleteRelationByKey(ctx context.Context, sourceID, targetID, relationType string) error {
	if err := validateRelationType(relationType); err != nil {
		return err
	}
	sourceUID, err := uuid.Parse(sourceID)
	if err != nil {
		return fmt.Errorf("parse source id: %w", err)
	}
	targetUID, err := uuid.Parse(targetID)
	if err != nil {
		return fmt.Errorf("parse target id: %w", err)
	}

	doDelete := func(q *dbgen.Queries) error {
		if err := q.DeleteRelationByKey(ctx, dbgen.DeleteRelationByKeyParams{
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
		return insertEvents(ctx, q, uuid.Nil, rk, nil, []proto.Message{evt})
	}

	if s.tx != nil {
		return doDelete(s.queries)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := doDelete(s.queries.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// UpdateRelationData updates the typed data on an existing relation.
func (s *Store) UpdateRelationData(ctx context.Context, sourceID, targetID, relationType string, data proto.Message) (matching.StoredRelation, error) {
	if err := validateRelationType(relationType); err != nil {
		return matching.StoredRelation{}, err
	}
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

	row, err := s.queries.UpdateRelationData(ctx, dbgen.UpdateRelationDataParams{
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

// ---------------------------------------------------------------------------
// Precondition evaluation
// ---------------------------------------------------------------------------

func evaluatePreConditions(ctx context.Context, q *dbgen.Queries, opIndex int, pcs []PreCondition) error {
	for _, pc := range pcs {
		if pc.MustExist && pc.MustNotExist {
			return fmt.Errorf("precondition on op %d: MustExist and MustNotExist are mutually exclusive", opIndex)
		}

		entities, err := findByAnchorsTx(ctx, q, pc.EntityType, pc.Anchors)
		if err != nil {
			return fmt.Errorf("precondition query op %d: %w", opIndex, err)
		}

		if pc.MustExist && len(entities) == 0 {
			return &PreConditionError{OpIndex: opIndex, Condition: pc, Violation: "not_found"}
		}
		if pc.MustNotExist && len(entities) > 0 {
			return &PreConditionError{OpIndex: opIndex, Condition: pc, Violation: "already_exists"}
		}
		if pc.TagRequired != "" && !entitiesHaveTag(entities, pc.TagRequired) {
			return &PreConditionError{OpIndex: opIndex, Condition: pc, Violation: "tag_required"}
		}
		if pc.TagForbidden != "" && entitiesHaveTag(entities, pc.TagForbidden) {
			return &PreConditionError{OpIndex: opIndex, Condition: pc, Violation: "tag_forbidden"}
		}
	}
	return nil
}

// findByAnchorsTx performs an anchor lookup using the transaction-scoped queries.
func findByAnchorsTx(ctx context.Context, q *dbgen.Queries, entityType string, anchors []matching.AnchorQuery) ([]matching.StoredEntity, error) {
	seen := make(map[uuid.UUID]struct{})
	var result []matching.StoredEntity

	for _, aq := range anchors {
		rows, err := q.FindByAnchors(ctx, dbgen.FindByAnchorsParams{
			EntityType:      entityType,
			AnchorField:     aq.Field,
			NormalizedValue: aq.Value,
			Tags:            []string{},
			AnyTags:         []string{},
			ExcludeTag:      "",
			UnlessTags:      []string{},
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

// entitiesHaveTag returns true if any entity in the slice carries the given tag.
func entitiesHaveTag(entities []matching.StoredEntity, tag string) bool {
	for _, e := range entities {
		if slices.Contains(e.Tags, tag) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

func toStoredRelation(op *UpsertRelationOp) (matching.StoredRelation, error) {
	rel := matching.StoredRelation{
		SourceID:     op.SourceID,
		TargetID:     op.TargetID,
		RelationType: op.RelationType,
		Confidence:   op.Confidence,
		Evidence:     op.Evidence,
		Implied:      op.Implied,
		SourceURN:    op.SourceURN,
	}
	if op.Data != nil {
		rel.DataType = string(proto.MessageName(op.Data))
		b, err := protojson.Marshal(op.Data)
		if err != nil {
			return matching.StoredRelation{}, fmt.Errorf("marshal relation data: %w", err)
		}
		rel.Data = b
	}
	return rel, nil
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

func marshalEntityData(op *WriteEntityOp) (entityType string, data json.RawMessage, err error) {
	if op.Data == nil {
		return "", nil, fmt.Errorf("Data must not be nil")
	}
	entityType = string(proto.MessageName(op.Data))
	if entityType == "" {
		return "", nil, fmt.Errorf("could not derive entity type from proto message")
	}
	data, err = protojson.Marshal(op.Data)
	if err != nil {
		return "", nil, fmt.Errorf("marshal entity data: %w", err)
	}
	return entityType, data, nil
}

func applyCreate(ctx context.Context, q *dbgen.Queries, op *WriteEntityOp) (matching.StoredEntity, error) {
	entityType, data, err := marshalEntityData(op)
	if err != nil {
		return matching.StoredEntity{}, err
	}

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
			ID: uid, EntityType: entityType,
			Data: data, Confidence: op.Confidence, Tags: tags, DisplayName: op.DisplayName,
		})
		if err != nil {
			return matching.StoredEntity{}, fmt.Errorf("insert entity with id: %w", err)
		}
		entityID = row.ID
		ent = entityFromRow(row)
	} else {
		row, err := q.InsertEntity(ctx, dbgen.InsertEntityParams{
			EntityType:  entityType,
			Data:        data,
			Confidence:  op.Confidence,
			Tags:        tags,
			DisplayName: op.DisplayName,
		})
		if err != nil {
			return matching.StoredEntity{}, fmt.Errorf("insert entity: %w", err)
		}
		entityID = row.ID
		ent = entityFromRow(row)
	}

	if err := upsertAnchors(ctx, q, entityID, entityType, op.Anchors); err != nil {
		return matching.StoredEntity{}, err
	}
	if err := upsertTokens(ctx, q, entityID, entityType, op.Tokens); err != nil {
		return matching.StoredEntity{}, err
	}
	if err := updateEmbedding(ctx, q, entityID, op.Embedding); err != nil {
		return matching.StoredEntity{}, err
	}

	// Emit standard EntityCreated event + any caller-defined events.
	stdEvent := &eventsv1.EntityCreated{
		EntityId:   entityID.String(),
		EntityType: entityType,
		Confidence: op.Confidence,
		Tags:       tags,
	}
	allEvents := append([]proto.Message{stdEvent}, op.Events...)
	if err := insertEvents(ctx, q, entityID, "", tags, allEvents); err != nil {
		return matching.StoredEntity{}, err
	}

	return ent, nil
}

func applyUpdateOrMerge(ctx context.Context, q *dbgen.Queries, op *WriteEntityOp) (matching.StoredEntity, error) {
	entityType, data, err := marshalEntityData(op)
	if err != nil {
		return matching.StoredEntity{}, err
	}

	uid, err := uuid.Parse(op.MatchedEntityID)
	if err != nil {
		return matching.StoredEntity{}, fmt.Errorf("parse entity id: %w", err)
	}

	switch op.Action {
	case WriteActionUpdate:
		tag, err := q.UpdateEntityData(ctx, dbgen.UpdateEntityDataParams{
			ID: uid, Data: data, Confidence: op.Confidence, DisplayName: op.DisplayName,
			ExpectedVersion: int32(op.Version),
		})
		if err != nil {
			return matching.StoredEntity{}, fmt.Errorf("update entity: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return matching.StoredEntity{}, ErrConflict
		}
	case WriteActionMerge:
		tag, err := q.MergeEntityData(ctx, dbgen.MergeEntityDataParams{
			ID: uid, Data: data, Confidence: op.Confidence, DisplayName: op.DisplayName,
			ExpectedVersion: int32(op.Version),
		})
		if err != nil {
			return matching.StoredEntity{}, fmt.Errorf("merge entity: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return matching.StoredEntity{}, ErrConflict
		}
	}

	if len(op.Tags) > 0 {
		if err := q.AddEntityTags(ctx, dbgen.AddEntityTagsParams{
			EntityID: uid, Tags: op.Tags,
		}); err != nil {
			return matching.StoredEntity{}, fmt.Errorf("add tags: %w", err)
		}
	}

	if err := upsertAnchors(ctx, q, uid, entityType, op.Anchors); err != nil {
		return matching.StoredEntity{}, err
	}
	if err := upsertTokens(ctx, q, uid, entityType, op.Tokens); err != nil {
		return matching.StoredEntity{}, err
	}
	if err := updateEmbedding(ctx, q, uid, op.Embedding); err != nil {
		return matching.StoredEntity{}, err
	}

	row, err := q.GetEntity(ctx, uid)
	if err != nil {
		return matching.StoredEntity{}, fmt.Errorf("get entity: %w", err)
	}
	ent := entityFromRow(row)

	// Emit standard event + any caller-defined events.
	var stdEvent proto.Message
	switch op.Action {
	case WriteActionUpdate:
		stdEvent = &eventsv1.EntityUpdated{
			EntityId:   uid.String(),
			EntityType: entityType,
			Confidence: op.Confidence,
			Tags:       ent.Tags,
		}
	case WriteActionMerge:
		stdEvent = &eventsv1.EntityMerged{
			WinnerId:   uid.String(),
			EntityType: entityType,
		}
	}
	allEvents := append([]proto.Message{stdEvent}, op.Events...)
	if err := insertEvents(ctx, q, uid, "", ent.Tags, allEvents); err != nil {
		return matching.StoredEntity{}, err
	}

	return ent, nil
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
		dataJSON = rel.Data
	}

	row, err := q.UpsertRelation(ctx, dbgen.UpsertRelationParams{
		SourceID:     sourceUID,
		TargetID:     targetUID,
		RelationType: rel.RelationType,
		Confidence:   rel.Confidence,
		Evidence:     evidence,
		Implied:      rel.Implied,
		SourceUrn:    srcURN,
		DataType:     rel.DataType,
		Data:         dataJSON,
	})
	if err != nil {
		return matching.StoredRelation{}, fmt.Errorf("upsert relation: %w", err)
	}
	return relationFromRow(row), nil
}
