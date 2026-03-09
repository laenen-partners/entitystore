package entitystore_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"testing"

	"connectrpc.com/connect"

	esv1 "github.com/laenen-partners/entitystore/gen/entitystore/v1"
	"github.com/laenen-partners/entitystore/gen/entitystore/v1/entitystorev1connect"

	entitystore "github.com/laenen-partners/entitystore"
)

// startE2E boots the EntityStore server backed by real PostgreSQL.
// Requires: task infra:up (Postgres on :5433).
func startE2E(t *testing.T) entitystorev1connect.EntityStoreServiceClient {
	t.Helper()

	dbURL := envOrSkip(t, "DATABASE_URL")

	cfg := entitystore.Config{DatabaseURL: dbURL}
	handler, s, err := entitystore.New(cfg)
	if err != nil {
		t.Fatalf("entitystore.New: %v", err)
	}
	t.Cleanup(s.Close)

	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	return entitystorev1connect.NewEntityStoreServiceClient(ts.Client(), ts.URL)
}

func envOrSkip(t *testing.T, key string) string {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		t.Skipf("skipping: %s not set (run task infra:up)", key)
	}
	return v
}

// batchWriteEntity is a helper that creates a single entity via BatchWrite.
func batchWriteEntity(t *testing.T, client entitystorev1connect.EntityStoreServiceClient, entityType string, data []byte, confidence float64) *esv1.Entity {
	t.Helper()
	resp, err := client.BatchWrite(context.Background(), connect.NewRequest(&esv1.BatchWriteRequest{
		Operations: []*esv1.BatchWriteOp{
			{Operation: &esv1.BatchWriteOp_WriteEntity{
				WriteEntity: &esv1.WriteEntityOp{
					Action:     esv1.WriteAction_WRITE_ACTION_CREATE,
					EntityType: entityType,
					Data:       data,
					Confidence: confidence,
					SourceUrn:  "test:e2e",
					ModelId:    "test",
					Fields:     []string{},
				},
			}},
		},
	}))
	if err != nil {
		t.Fatalf("BatchWrite (create %s): %v", entityType, err)
	}
	result := resp.Msg.Results[0].GetEntity()
	if result == nil {
		t.Fatalf("BatchWrite returned nil entity for %s", entityType)
	}
	return result
}

// ---------------------------------------------------------------------------
// Entity CRUD
// ---------------------------------------------------------------------------

func TestE2E_BatchWrite_CreateAndGetEntity(t *testing.T) {
	client := startE2E(t)
	ctx := context.Background()

	data := `{"name":"Alice","email":"alice@example.com"}`
	ent := batchWriteEntity(t, client, "entities.v1.Person", []byte(data), 0.95)
	if ent.Id == "" {
		t.Fatal("BatchWrite returned empty ID")
	}
	if ent.EntityType != "entities.v1.Person" {
		t.Errorf("EntityType = %q, want entities.v1.Person", ent.EntityType)
	}

	// Get it back.
	getResp, err := client.GetEntity(ctx, connect.NewRequest(&esv1.GetEntityRequest{
		Id: ent.Id,
	}))
	if err != nil {
		t.Fatalf("GetEntity: %v", err)
	}
	if getResp.Msg.Entity.Id != ent.Id {
		t.Errorf("GetEntity.Id = %q, want %q", getResp.Msg.Entity.Id, ent.Id)
	}

	// Clean up.
	_, err = client.DeleteEntity(ctx, connect.NewRequest(&esv1.DeleteEntityRequest{Id: ent.Id}))
	if err != nil {
		t.Fatalf("DeleteEntity: %v", err)
	}
}

func TestE2E_BatchWrite_UpdateEntity(t *testing.T) {
	client := startE2E(t)
	ctx := context.Background()

	ent := batchWriteEntity(t, client, "entities.v1.Person", []byte(`{"name":"Bob"}`), 0.8)

	// Update via BatchWrite.
	_, err := client.BatchWrite(ctx, connect.NewRequest(&esv1.BatchWriteRequest{
		Operations: []*esv1.BatchWriteOp{
			{Operation: &esv1.BatchWriteOp_WriteEntity{
				WriteEntity: &esv1.WriteEntityOp{
					Action:          esv1.WriteAction_WRITE_ACTION_UPDATE,
					EntityType:      "entities.v1.Person",
					MatchedEntityId: ent.Id,
					Data:            []byte(`{"name":"Bob Updated","role":"admin"}`),
					Confidence:      0.99,
					SourceUrn:       "test:e2e",
					ModelId:         "test",
					Fields:          []string{"name", "role"},
					MatchMethod:     "anchor",
				},
			}},
		},
	}))
	if err != nil {
		t.Fatalf("BatchWrite (update): %v", err)
	}

	// Verify.
	getResp, err := client.GetEntity(ctx, connect.NewRequest(&esv1.GetEntityRequest{Id: ent.Id}))
	if err != nil {
		t.Fatalf("GetEntity: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(getResp.Msg.Entity.Data, &got); err != nil {
		t.Fatalf("unmarshal data: %v", err)
	}
	if got["name"] != "Bob Updated" {
		t.Errorf("name = %v, want Bob Updated", got["name"])
	}

	_, _ = client.DeleteEntity(ctx, connect.NewRequest(&esv1.DeleteEntityRequest{Id: ent.Id}))
}

func TestE2E_GetEntitiesByType(t *testing.T) {
	client := startE2E(t)
	ctx := context.Background()

	// Insert two entities of the same type via BatchWrite.
	resp, err := client.BatchWrite(ctx, connect.NewRequest(&esv1.BatchWriteRequest{
		Operations: []*esv1.BatchWriteOp{
			{Operation: &esv1.BatchWriteOp_WriteEntity{
				WriteEntity: &esv1.WriteEntityOp{
					Action: esv1.WriteAction_WRITE_ACTION_CREATE, EntityType: "entities.v1.E2ETypeTest",
					Data: []byte(`{"name":"Alpha"}`), Confidence: 0.9,
					SourceUrn: "test:e2e", ModelId: "test", Fields: []string{"name"},
				},
			}},
			{Operation: &esv1.BatchWriteOp_WriteEntity{
				WriteEntity: &esv1.WriteEntityOp{
					Action: esv1.WriteAction_WRITE_ACTION_CREATE, EntityType: "entities.v1.E2ETypeTest",
					Data: []byte(`{"name":"Beta"}`), Confidence: 0.9,
					SourceUrn: "test:e2e", ModelId: "test", Fields: []string{"name"},
				},
			}},
		},
	}))
	if err != nil {
		t.Fatalf("BatchWrite: %v", err)
	}

	var ids []string
	for _, r := range resp.Msg.Results {
		ids = append(ids, r.GetEntity().Id)
	}

	listResp, err := client.GetEntitiesByType(ctx, connect.NewRequest(&esv1.GetEntitiesByTypeRequest{
		EntityType: "entities.v1.E2ETypeTest",
	}))
	if err != nil {
		t.Fatalf("GetEntitiesByType: %v", err)
	}
	if len(listResp.Msg.Entities) < 2 {
		t.Errorf("expected at least 2 entities, got %d", len(listResp.Msg.Entities))
	}

	for _, id := range ids {
		_, _ = client.DeleteEntity(ctx, connect.NewRequest(&esv1.DeleteEntityRequest{Id: id}))
	}
}

func TestE2E_DeleteEntity(t *testing.T) {
	client := startE2E(t)
	ctx := context.Background()

	ent := batchWriteEntity(t, client, "entities.v1.Person", []byte(`{"name":"ToDelete"}`), 0.5)

	_, err := client.DeleteEntity(ctx, connect.NewRequest(&esv1.DeleteEntityRequest{Id: ent.Id}))
	if err != nil {
		t.Fatalf("DeleteEntity: %v", err)
	}

	// Verify it's gone.
	_, err = client.GetEntity(ctx, connect.NewRequest(&esv1.GetEntityRequest{Id: ent.Id}))
	if err == nil {
		t.Fatal("expected error after delete, got nil")
	}
}

// ---------------------------------------------------------------------------
// Search: anchors, tokens, embeddings
// ---------------------------------------------------------------------------

func TestE2E_FindByAnchors(t *testing.T) {
	client := startE2E(t)
	ctx := context.Background()

	resp, err := client.FindByAnchors(ctx, connect.NewRequest(&esv1.FindByAnchorsRequest{
		EntityType: "entities.v1.Person",
		Anchors: []*esv1.AnchorQuery{
			{Field: "email", Value: "nonexistent@example.com"},
		},
	}))
	if err != nil {
		t.Fatalf("FindByAnchors: %v", err)
	}
	if len(resp.Msg.Entities) != 0 {
		t.Errorf("expected 0 results for nonexistent anchor, got %d", len(resp.Msg.Entities))
	}
}

func TestE2E_FindByTokens(t *testing.T) {
	client := startE2E(t)
	ctx := context.Background()

	resp, err := client.FindByTokens(ctx, connect.NewRequest(&esv1.FindByTokensRequest{
		EntityType: "entities.v1.Company",
		Tokens:     []string{"nonexistent", "tokens"},
		Limit:      10,
	}))
	if err != nil {
		t.Fatalf("FindByTokens: %v", err)
	}
	if len(resp.Msg.Entities) != 0 {
		t.Errorf("expected 0 results for nonexistent tokens, got %d", len(resp.Msg.Entities))
	}
}

func TestE2E_FindByEmbedding(t *testing.T) {
	client := startE2E(t)
	ctx := context.Background()

	embedding := make([]float32, 3)
	for i := range embedding {
		embedding[i] = float32(i) * 0.1
	}

	resp, err := client.FindByEmbedding(ctx, connect.NewRequest(&esv1.FindByEmbeddingRequest{
		EntityType: "entities.v1.Person",
		Embedding:  embedding,
		TopK:       5,
	}))
	if err != nil {
		t.Fatalf("FindByEmbedding: %v", err)
	}
	if len(resp.Msg.Entities) != 0 {
		t.Errorf("expected 0 results, got %d", len(resp.Msg.Entities))
	}
}

// ---------------------------------------------------------------------------
// Relations via BatchWrite
// ---------------------------------------------------------------------------

func TestE2E_BatchWrite_Relations(t *testing.T) {
	client := startE2E(t)
	ctx := context.Background()

	// Create two entities.
	alice := batchWriteEntity(t, client, "entities.v1.Person", []byte(`{"name":"Alice"}`), 0.9)
	acme := batchWriteEntity(t, client, "entities.v1.Company", []byte(`{"name":"Acme Corp"}`), 0.9)

	// Create relation via BatchWrite.
	relResp, err := client.BatchWrite(ctx, connect.NewRequest(&esv1.BatchWriteRequest{
		Operations: []*esv1.BatchWriteOp{
			{Operation: &esv1.BatchWriteOp_UpsertRelation{
				UpsertRelation: &esv1.UpsertRelationOp{
					SourceId:     alice.Id,
					TargetId:     acme.Id,
					RelationType: "employed_by",
					Confidence:   0.95,
					Evidence:     "extracted from contract",
				},
			}},
		},
	}))
	if err != nil {
		t.Fatalf("BatchWrite (relation): %v", err)
	}
	rel := relResp.Msg.Results[0].GetRelation()
	if rel.RelationType != "employed_by" {
		t.Errorf("RelationType = %q, want employed_by", rel.RelationType)
	}

	// Get relations from Alice.
	fromResp, err := client.GetRelationsFromEntity(ctx, connect.NewRequest(&esv1.GetRelationsFromEntityRequest{
		EntityId: alice.Id,
	}))
	if err != nil {
		t.Fatalf("GetRelationsFromEntity: %v", err)
	}
	if len(fromResp.Msg.Relations) != 1 {
		t.Fatalf("expected 1 relation from Alice, got %d", len(fromResp.Msg.Relations))
	}
	if fromResp.Msg.Relations[0].TargetId != acme.Id {
		t.Errorf("relation target = %q, want %q", fromResp.Msg.Relations[0].TargetId, acme.Id)
	}

	// Get relations to Acme.
	toResp, err := client.GetRelationsToEntity(ctx, connect.NewRequest(&esv1.GetRelationsToEntityRequest{
		EntityId: acme.Id,
	}))
	if err != nil {
		t.Fatalf("GetRelationsToEntity: %v", err)
	}
	if len(toResp.Msg.Relations) != 1 {
		t.Fatalf("expected 1 relation to Acme, got %d", len(toResp.Msg.Relations))
	}

	// FindConnectedByType.
	connResp, err := client.FindConnectedByType(ctx, connect.NewRequest(&esv1.FindConnectedByTypeRequest{
		EntityId:      alice.Id,
		EntityType:    "entities.v1.Company",
		RelationTypes: []string{"employed_by"},
	}))
	if err != nil {
		t.Fatalf("FindConnectedByType: %v", err)
	}
	if len(connResp.Msg.Entities) != 1 {
		t.Fatalf("expected 1 connected company, got %d", len(connResp.Msg.Entities))
	}
	if connResp.Msg.Entities[0].Id != acme.Id {
		t.Errorf("connected entity = %q, want %q", connResp.Msg.Entities[0].Id, acme.Id)
	}

	// Clean up.
	_, _ = client.DeleteEntity(ctx, connect.NewRequest(&esv1.DeleteEntityRequest{Id: alice.Id}))
	_, _ = client.DeleteEntity(ctx, connect.NewRequest(&esv1.DeleteEntityRequest{Id: acme.Id}))
}

func TestE2E_BatchWrite_RelationWithData(t *testing.T) {
	client := startE2E(t)
	ctx := context.Background()

	carol := batchWriteEntity(t, client, "entities.v1.Person", []byte(`{"name":"Carol"}`), 0.9)
	tech := batchWriteEntity(t, client, "entities.v1.Company", []byte(`{"name":"TechCo"}`), 0.9)

	relData, _ := json.Marshal(map[string]any{"role": "CTO", "since": "2024"})
	relResp, err := client.BatchWrite(ctx, connect.NewRequest(&esv1.BatchWriteRequest{
		Operations: []*esv1.BatchWriteOp{
			{Operation: &esv1.BatchWriteOp_UpsertRelation{
				UpsertRelation: &esv1.UpsertRelationOp{
					SourceId:     carol.Id,
					TargetId:     tech.Id,
					RelationType: "employed_by",
					Confidence:   0.99,
					Evidence:     "board minutes",
					Data:         relData,
				},
			}},
		},
	}))
	if err != nil {
		t.Fatalf("BatchWrite (relation with data): %v", err)
	}
	rel := relResp.Msg.Results[0].GetRelation()
	if rel.Confidence != 0.99 {
		t.Errorf("Confidence = %v, want 0.99", rel.Confidence)
	}

	_, _ = client.DeleteEntity(ctx, connect.NewRequest(&esv1.DeleteEntityRequest{Id: carol.Id}))
	_, _ = client.DeleteEntity(ctx, connect.NewRequest(&esv1.DeleteEntityRequest{Id: tech.Id}))
}

// ---------------------------------------------------------------------------
// Mixed batch: entities + relations in one call
// ---------------------------------------------------------------------------

func TestE2E_BatchWrite_MixedEntitiesAndRelations(t *testing.T) {
	client := startE2E(t)
	ctx := context.Background()

	// First create the entities (relations need valid IDs).
	createResp, err := client.BatchWrite(ctx, connect.NewRequest(&esv1.BatchWriteRequest{
		Operations: []*esv1.BatchWriteOp{
			{Operation: &esv1.BatchWriteOp_WriteEntity{
				WriteEntity: &esv1.WriteEntityOp{
					Action: esv1.WriteAction_WRITE_ACTION_CREATE, EntityType: "entities.v1.Person",
					Data: []byte(`{"name":"Dave"}`), Confidence: 0.9,
					SourceUrn: "test:mixed", ModelId: "test", Fields: []string{"name"},
				},
			}},
			{Operation: &esv1.BatchWriteOp_WriteEntity{
				WriteEntity: &esv1.WriteEntityOp{
					Action: esv1.WriteAction_WRITE_ACTION_CREATE, EntityType: "entities.v1.Company",
					Data: []byte(`{"name":"MixCo"}`), Confidence: 0.9,
					SourceUrn: "test:mixed", ModelId: "test", Fields: []string{"name"},
				},
			}},
		},
	}))
	if err != nil {
		t.Fatalf("BatchWrite (create): %v", err)
	}
	daveID := createResp.Msg.Results[0].GetEntity().Id
	mixcoID := createResp.Msg.Results[1].GetEntity().Id

	// Now create a relation in the same batch as an update.
	mixedResp, err := client.BatchWrite(ctx, connect.NewRequest(&esv1.BatchWriteRequest{
		Operations: []*esv1.BatchWriteOp{
			{Operation: &esv1.BatchWriteOp_WriteEntity{
				WriteEntity: &esv1.WriteEntityOp{
					Action: esv1.WriteAction_WRITE_ACTION_UPDATE, EntityType: "entities.v1.Person",
					MatchedEntityId: daveID,
					Data: []byte(`{"name":"Dave Updated"}`), Confidence: 0.95,
					SourceUrn: "test:mixed", ModelId: "test", Fields: []string{"name"},
					MatchMethod: "anchor",
				},
			}},
			{Operation: &esv1.BatchWriteOp_UpsertRelation{
				UpsertRelation: &esv1.UpsertRelationOp{
					SourceId: daveID, TargetId: mixcoID,
					RelationType: "works_at", Confidence: 0.9,
				},
			}},
		},
	}))
	if err != nil {
		t.Fatalf("BatchWrite (mixed): %v", err)
	}
	if len(mixedResp.Msg.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(mixedResp.Msg.Results))
	}
	// First result should be entity.
	if mixedResp.Msg.Results[0].GetEntity() == nil {
		t.Error("expected entity result at index 0")
	}
	// Second result should be relation.
	if mixedResp.Msg.Results[1].GetRelation() == nil {
		t.Error("expected relation result at index 1")
	}

	_, _ = client.DeleteEntity(ctx, connect.NewRequest(&esv1.DeleteEntityRequest{Id: daveID}))
	_, _ = client.DeleteEntity(ctx, connect.NewRequest(&esv1.DeleteEntityRequest{Id: mixcoID}))
}
