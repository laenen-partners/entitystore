package entitystore

import (
	"context"
	"encoding/json"

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
	entities, err := h.store.GetEntitiesByType(ctx, req.Msg.EntityType)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&entitystorev1.GetEntitiesByTypeResponse{
		Entities: toProtoEntities(entities),
	}), nil
}

func (h *Handler) InsertEntity(ctx context.Context, req *connect.Request[entitystorev1.InsertEntityRequest]) (*connect.Response[entitystorev1.InsertEntityResponse], error) {
	ent, err := h.store.InsertEntity(ctx, req.Msg.EntityType, json.RawMessage(req.Msg.Data), req.Msg.Confidence)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&entitystorev1.InsertEntityResponse{
		Entity: toProtoEntity(ent),
	}), nil
}

func (h *Handler) UpdateEntity(ctx context.Context, req *connect.Request[entitystorev1.UpdateEntityRequest]) (*connect.Response[entitystorev1.UpdateEntityResponse], error) {
	if err := h.store.UpdateEntity(ctx, req.Msg.Id, json.RawMessage(req.Msg.Data), req.Msg.Confidence); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&entitystorev1.UpdateEntityResponse{}), nil
}

func (h *Handler) DeleteEntity(ctx context.Context, req *connect.Request[entitystorev1.DeleteEntityRequest]) (*connect.Response[entitystorev1.DeleteEntityResponse], error) {
	if err := h.store.DeleteEntity(ctx, req.Msg.Id); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&entitystorev1.DeleteEntityResponse{}), nil
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

func (h *Handler) UpsertRelation(ctx context.Context, req *connect.Request[entitystorev1.UpsertRelationRequest]) (*connect.Response[entitystorev1.UpsertRelationResponse], error) {
	var data map[string]any
	if len(req.Msg.Data) > 0 {
		_ = json.Unmarshal(req.Msg.Data, &data)
	}
	rel, err := h.store.UpsertRelation(ctx, matching.StoredRelation{
		SourceID:     req.Msg.SourceId,
		TargetID:     req.Msg.TargetId,
		RelationType: req.Msg.RelationType,
		Confidence:   req.Msg.Confidence,
		Evidence:     req.Msg.Evidence,
		Implied:      req.Msg.Implied,
		DocumentID:   req.Msg.DocumentId,
		Data:         data,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&entitystorev1.UpsertRelationResponse{
		Relation: toProtoRelation(rel),
	}), nil
}

func (h *Handler) GetRelationsFromEntity(ctx context.Context, req *connect.Request[entitystorev1.GetRelationsFromEntityRequest]) (*connect.Response[entitystorev1.GetRelationsResponse], error) {
	rels, err := h.store.GetRelationsFromEntity(ctx, req.Msg.EntityId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&entitystorev1.GetRelationsResponse{
		Relations: toProtoRelations(rels),
	}), nil
}

func (h *Handler) GetRelationsToEntity(ctx context.Context, req *connect.Request[entitystorev1.GetRelationsToEntityRequest]) (*connect.Response[entitystorev1.GetRelationsResponse], error) {
	rels, err := h.store.GetRelationsToEntity(ctx, req.Msg.EntityId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&entitystorev1.GetRelationsResponse{
		Relations: toProtoRelations(rels),
	}), nil
}

// ---------------------------------------------------------------------------
// Conversion helpers
// ---------------------------------------------------------------------------

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
		DocumentId:   r.DocumentID,
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
