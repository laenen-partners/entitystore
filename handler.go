package entitystore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"connectrpc.com/connect"

	entitystorev1 "github.com/laenen-partners/entitystore/gen/entitystore/v1"
	"github.com/laenen-partners/entitystore/gen/entitystore/v1/entitystorev1connect"
	"github.com/laenen-partners/entitystore/matching"
	entitystore "github.com/laenen-partners/entitystore/store"
)

// Handler implements the EntityStoreService connect-go handler.
type Handler struct {
	entitystorev1connect.UnimplementedEntityStoreServiceHandler
	store *entitystore.Store
}

// --- Reads ---

func (h *Handler) GetEntity(ctx context.Context, req *connect.Request[entitystorev1.GetEntityRequest]) (*connect.Response[entitystorev1.GetEntityResponse], error) {
	ent, err := h.store.GetEntity(ctx, req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	return connect.NewResponse(&entitystorev1.GetEntityResponse{
		Entity: toProtoEntity(ent),
	}), nil
}

func (h *Handler) GetEntitiesByType(ctx context.Context, req *connect.Request[entitystorev1.GetEntitiesByTypeRequest]) (*connect.Response[entitystorev1.GetEntitiesByTypeResponse], error) {
	var cursor *time.Time
	if req.Msg.PageToken != "" {
		t, err := time.Parse(time.RFC3339Nano, req.Msg.PageToken)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		cursor = &t
	}
	pageSize := req.Msg.PageSize
	if pageSize <= 0 {
		pageSize = 100
	}
	entities, err := h.store.GetEntitiesByType(ctx, req.Msg.EntityType, pageSize, cursor)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	resp := &entitystorev1.GetEntitiesByTypeResponse{
		Entities: toProtoEntities(entities),
	}
	if int32(len(entities)) == pageSize {
		last := entities[len(entities)-1]
		resp.NextPageToken = last.UpdatedAt.Format(time.RFC3339Nano)
	}
	return connect.NewResponse(resp), nil
}

func (h *Handler) FindByAnchors(ctx context.Context, req *connect.Request[entitystorev1.FindByAnchorsRequest]) (*connect.Response[entitystorev1.FindByAnchorsResponse], error) {
	anchors := make([]matching.AnchorQuery, len(req.Msg.Anchors))
	for i, a := range req.Msg.Anchors {
		anchors[i] = matching.AnchorQuery{Field: a.Field, Value: a.Value}
	}
	entities, err := h.store.FindByAnchors(ctx, req.Msg.EntityType, anchors, toFilter(req.Msg.Filter))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&entitystorev1.FindByAnchorsResponse{
		Entities: toProtoEntities(entities),
	}), nil
}

func (h *Handler) FindByTokens(ctx context.Context, req *connect.Request[entitystorev1.FindByTokensRequest]) (*connect.Response[entitystorev1.FindByTokensResponse], error) {
	entities, err := h.store.FindByTokens(ctx, req.Msg.EntityType, req.Msg.Tokens, int(req.Msg.Limit), toFilter(req.Msg.Filter))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&entitystorev1.FindByTokensResponse{
		Entities: toProtoEntities(entities),
	}), nil
}

func (h *Handler) FindByEmbedding(ctx context.Context, req *connect.Request[entitystorev1.FindByEmbeddingRequest]) (*connect.Response[entitystorev1.FindByEmbeddingResponse], error) {
	entities, err := h.store.FindByEmbedding(ctx, req.Msg.EntityType, req.Msg.Embedding, int(req.Msg.TopK), toFilter(req.Msg.Filter))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&entitystorev1.FindByEmbeddingResponse{
		Entities: toProtoEntities(entities),
	}), nil
}

func (h *Handler) FindConnectedByType(ctx context.Context, req *connect.Request[entitystorev1.FindConnectedByTypeRequest]) (*connect.Response[entitystorev1.FindConnectedByTypeResponse], error) {
	entities, err := h.store.FindConnectedByType(ctx, req.Msg.EntityId, req.Msg.EntityType, req.Msg.RelationTypes, toFilter(req.Msg.Filter))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&entitystorev1.FindConnectedByTypeResponse{
		Entities: toProtoEntities(entities),
	}), nil
}

func (h *Handler) GetRelationsFromEntity(ctx context.Context, req *connect.Request[entitystorev1.GetRelationsFromEntityRequest]) (*connect.Response[entitystorev1.GetRelationsFromEntityResponse], error) {
	rels, err := h.store.GetRelationsFromEntity(ctx, req.Msg.EntityId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&entitystorev1.GetRelationsFromEntityResponse{
		Relations: toProtoRelations(rels),
	}), nil
}

func (h *Handler) GetRelationsToEntity(ctx context.Context, req *connect.Request[entitystorev1.GetRelationsToEntityRequest]) (*connect.Response[entitystorev1.GetRelationsToEntityResponse], error) {
	rels, err := h.store.GetRelationsToEntity(ctx, req.Msg.EntityId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&entitystorev1.GetRelationsToEntityResponse{
		Relations: toProtoRelations(rels),
	}), nil
}

// --- Writes ---

func (h *Handler) BatchWrite(ctx context.Context, req *connect.Request[entitystorev1.BatchWriteRequest]) (*connect.Response[entitystorev1.BatchWriteResponse], error) {
	if len(req.Msg.Operations) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("at least one operation is required"))
	}

	ops := make([]entitystore.BatchWriteOp, len(req.Msg.Operations))
	for i, pbOp := range req.Msg.Operations {
		switch v := pbOp.Operation.(type) {
		case *entitystorev1.BatchWriteOp_WriteEntity:
			ops[i] = entitystore.BatchWriteOp{
				WriteEntity: toWriteEntityOp(v.WriteEntity),
			}
		case *entitystorev1.BatchWriteOp_UpsertRelation:
			ops[i] = entitystore.BatchWriteOp{
				UpsertRelation: toUpsertRelationOp(v.UpsertRelation),
			}
		default:
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("operation %d: empty", i))
		}
	}

	results, err := h.store.BatchWrite(ctx, ops)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	pbResults := make([]*entitystorev1.BatchWriteResult, len(results))
	for i, r := range results {
		pbResults[i] = &entitystorev1.BatchWriteResult{}
		if r.Entity != nil {
			pbResults[i].Result = &entitystorev1.BatchWriteResult_Entity{
				Entity: toProtoEntity(*r.Entity),
			}
		} else if r.Relation != nil {
			pbResults[i].Result = &entitystorev1.BatchWriteResult_Relation{
				Relation: toProtoRelation(*r.Relation),
			}
		}
	}

	return connect.NewResponse(&entitystorev1.BatchWriteResponse{
		Results: pbResults,
	}), nil
}

func (h *Handler) DeleteEntity(ctx context.Context, req *connect.Request[entitystorev1.DeleteEntityRequest]) (*connect.Response[entitystorev1.DeleteEntityResponse], error) {
	if err := h.store.DeleteEntity(ctx, req.Msg.Id); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&entitystorev1.DeleteEntityResponse{}), nil
}

// --- Tags ---

func (h *Handler) SetTags(ctx context.Context, req *connect.Request[entitystorev1.SetTagsRequest]) (*connect.Response[entitystorev1.SetTagsResponse], error) {
	if err := h.store.SetTags(ctx, req.Msg.EntityId, req.Msg.Tags); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&entitystorev1.SetTagsResponse{}), nil
}

func (h *Handler) AddTags(ctx context.Context, req *connect.Request[entitystorev1.AddTagsRequest]) (*connect.Response[entitystorev1.AddTagsResponse], error) {
	if err := h.store.AddTags(ctx, req.Msg.EntityId, req.Msg.Tags); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	ent, err := h.store.GetEntity(ctx, req.Msg.EntityId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&entitystorev1.AddTagsResponse{
		Tags: ent.Tags,
	}), nil
}

func (h *Handler) RemoveTag(ctx context.Context, req *connect.Request[entitystorev1.RemoveTagRequest]) (*connect.Response[entitystorev1.RemoveTagResponse], error) {
	if err := h.store.RemoveTag(ctx, req.Msg.EntityId, req.Msg.Tag); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&entitystorev1.RemoveTagResponse{}), nil
}

// ---------------------------------------------------------------------------
// Conversion helpers
// ---------------------------------------------------------------------------

func toWriteEntityOp(msg *entitystorev1.WriteEntityOp) *entitystore.WriteEntityOp {
	action := entitystore.WriteActionCreate
	switch msg.Action {
	case entitystorev1.WriteAction_WRITE_ACTION_UPDATE:
		action = entitystore.WriteActionUpdate
	case entitystorev1.WriteAction_WRITE_ACTION_MERGE:
		action = entitystore.WriteActionMerge
	}

	anchors := make([]matching.AnchorQuery, len(msg.Anchors))
	for i, a := range msg.Anchors {
		anchors[i] = matching.AnchorQuery{Field: a.Field, Value: a.Value}
	}

	tokens := make(map[string][]string, len(msg.Tokens))
	for field, tl := range msg.Tokens {
		tokens[field] = tl.Values
	}

	var embedding []float32
	if len(msg.Embedding) > 0 {
		embedding = msg.Embedding
	}

	fields := msg.Fields
	if fields == nil {
		fields = []string{}
	}

	return &entitystore.WriteEntityOp{
		Action:          action,
		ID:              msg.Id,
		EntityType:      msg.EntityType,
		Data:            json.RawMessage(msg.Data),
		Confidence:      msg.Confidence,
		Tags:            msg.Tags,
		MatchedEntityID: msg.MatchedEntityId,
		Anchors:         anchors,
		Tokens:          tokens,
		Embedding:       embedding,
		Provenance: matching.ProvenanceEntry{
			SourceURN:       msg.SourceUrn,
			ModelID:         msg.ModelId,
			Confidence:      msg.Confidence,
			Fields:          fields,
			MatchMethod:     msg.MatchMethod,
			MatchConfidence: msg.MatchConfidence,
		},
	}
}

func toUpsertRelationOp(msg *entitystorev1.UpsertRelationOp) *entitystore.UpsertRelationOp {
	var data map[string]any
	if len(msg.Data) > 0 {
		_ = json.Unmarshal(msg.Data, &data)
	}
	return &entitystore.UpsertRelationOp{
		SourceID:     msg.SourceId,
		TargetID:     msg.TargetId,
		RelationType: msg.RelationType,
		Confidence:   msg.Confidence,
		Evidence:     msg.Evidence,
		Implied:      msg.Implied,
		SourceURN:    msg.SourceUrn,
		Data:         data,
	}
}

func toFilter(f *entitystorev1.QueryFilter) *matching.QueryFilter {
	if f == nil || len(f.Tags) == 0 {
		return nil
	}
	return &matching.QueryFilter{Tags: f.Tags}
}

func toProtoEntity(e matching.StoredEntity) *entitystorev1.Entity {
	return &entitystorev1.Entity{
		Id:         e.ID,
		EntityType: e.EntityType,
		Data:       []byte(e.Data),
		Confidence: e.Confidence,
		Tags:       e.Tags,
		CreatedAt:  e.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:  e.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

func toProtoEntities(entities []matching.StoredEntity) []*entitystorev1.Entity {
	result := make([]*entitystorev1.Entity, len(entities))
	for i, e := range entities {
		result[i] = toProtoEntity(e)
	}
	return result
}

func toProtoRelation(r matching.StoredRelation) *entitystorev1.Relation {
	var data []byte
	if len(r.Data) > 0 {
		data, _ = json.Marshal(r.Data)
	}
	return &entitystorev1.Relation{
		Id:           r.ID,
		SourceId:     r.SourceID,
		TargetId:     r.TargetID,
		RelationType: r.RelationType,
		Confidence:   r.Confidence,
		Evidence:     r.Evidence,
		Implied:      r.Implied,
		SourceUrn:    r.SourceURN,
		Data:         data,
		CreatedAt:    r.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

func toProtoRelations(rels []matching.StoredRelation) []*entitystorev1.Relation {
	result := make([]*entitystorev1.Relation, len(rels))
	for i, r := range rels {
		result[i] = toProtoRelation(r)
	}
	return result
}
