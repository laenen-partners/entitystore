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
	"github.com/laenen-partners/entitystore/store"

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

	// Apply migrations.
	if err := store.Migrate(context.Background(), s.Pool()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

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

// ---------------------------------------------------------------------------
// Entity CRUD
// ---------------------------------------------------------------------------

func TestE2E_InsertAndGetEntity(t *testing.T) {
	client := startE2E(t)
	ctx := context.Background()

	data := `{"name":"Alice","email":"alice@example.com"}`
	insertResp, err := client.InsertEntity(ctx, connect.NewRequest(&esv1.InsertEntityRequest{
		EntityType: "entities.v1.Person",
		Data:       []byte(data),
		Confidence: 0.95,
	}))
	if err != nil {
		t.Fatalf("InsertEntity: %v", err)
	}
	ent := insertResp.Msg.Entity
	if ent.Id == "" {
		t.Fatal("InsertEntity returned empty ID")
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
	_, err = client.DeleteEntity(ctx, connect.NewRequest(&esv1.DeleteEntityRequest{
		Id: ent.Id,
	}))
	if err != nil {
		t.Fatalf("DeleteEntity: %v", err)
	}
}

func TestE2E_UpdateEntity(t *testing.T) {
	client := startE2E(t)
	ctx := context.Background()

	insertResp, err := client.InsertEntity(ctx, connect.NewRequest(&esv1.InsertEntityRequest{
		EntityType: "entities.v1.Person",
		Data:       []byte(`{"name":"Bob"}`),
		Confidence: 0.8,
	}))
	if err != nil {
		t.Fatalf("InsertEntity: %v", err)
	}
	id := insertResp.Msg.Entity.Id

	// Update.
	_, err = client.UpdateEntity(ctx, connect.NewRequest(&esv1.UpdateEntityRequest{
		Id:         id,
		Data:       []byte(`{"name":"Bob Updated","role":"admin"}`),
		Confidence: 0.99,
	}))
	if err != nil {
		t.Fatalf("UpdateEntity: %v", err)
	}

	// Verify.
	getResp, err := client.GetEntity(ctx, connect.NewRequest(&esv1.GetEntityRequest{Id: id}))
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

	_, _ = client.DeleteEntity(ctx, connect.NewRequest(&esv1.DeleteEntityRequest{Id: id}))
}

func TestE2E_GetEntitiesByType(t *testing.T) {
	client := startE2E(t)
	ctx := context.Background()

	// Insert two entities of the same type.
	var ids []string
	for _, name := range []string{"Alpha", "Beta"} {
		resp, err := client.InsertEntity(ctx, connect.NewRequest(&esv1.InsertEntityRequest{
			EntityType: "entities.v1.E2ETypeTest",
			Data:       []byte(`{"name":"` + name + `"}`),
			Confidence: 0.9,
		}))
		if err != nil {
			t.Fatalf("InsertEntity %s: %v", name, err)
		}
		ids = append(ids, resp.Msg.Entity.Id)
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

	insertResp, err := client.InsertEntity(ctx, connect.NewRequest(&esv1.InsertEntityRequest{
		EntityType: "entities.v1.Person",
		Data:       []byte(`{"name":"ToDelete"}`),
		Confidence: 0.5,
	}))
	if err != nil {
		t.Fatalf("InsertEntity: %v", err)
	}
	id := insertResp.Msg.Entity.Id

	_, err = client.DeleteEntity(ctx, connect.NewRequest(&esv1.DeleteEntityRequest{Id: id}))
	if err != nil {
		t.Fatalf("DeleteEntity: %v", err)
	}

	// Verify it's gone.
	_, err = client.GetEntity(ctx, connect.NewRequest(&esv1.GetEntityRequest{Id: id}))
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

	// Insert entity and set up anchors via the store directly is not possible
	// through the RPC. Instead, we verify the RPC returns no error and handles
	// empty results gracefully.
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

	// Create a dummy embedding vector (3 dimensions).
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
	// No entities with embeddings yet, so expect empty.
	if len(resp.Msg.Entities) != 0 {
		t.Errorf("expected 0 results, got %d", len(resp.Msg.Entities))
	}
}

// ---------------------------------------------------------------------------
// Relations
// ---------------------------------------------------------------------------

func TestE2E_Relations(t *testing.T) {
	client := startE2E(t)
	ctx := context.Background()

	// Create two entities.
	r1, err := client.InsertEntity(ctx, connect.NewRequest(&esv1.InsertEntityRequest{
		EntityType: "entities.v1.Person",
		Data:       []byte(`{"name":"Alice"}`),
		Confidence: 0.9,
	}))
	if err != nil {
		t.Fatalf("InsertEntity Alice: %v", err)
	}
	r2, err := client.InsertEntity(ctx, connect.NewRequest(&esv1.InsertEntityRequest{
		EntityType: "entities.v1.Company",
		Data:       []byte(`{"name":"Acme Corp"}`),
		Confidence: 0.9,
	}))
	if err != nil {
		t.Fatalf("InsertEntity Acme: %v", err)
	}

	aliceID := r1.Msg.Entity.Id
	acmeID := r2.Msg.Entity.Id

	// Create relation.
	relResp, err := client.UpsertRelation(ctx, connect.NewRequest(&esv1.UpsertRelationRequest{
		SourceId:     aliceID,
		TargetId:     acmeID,
		RelationType: "employed_by",
		Confidence:   0.95,
		Evidence:     "extracted from contract",
	}))
	if err != nil {
		t.Fatalf("UpsertRelation: %v", err)
	}
	if relResp.Msg.Relation.RelationType != "employed_by" {
		t.Errorf("RelationType = %q, want employed_by", relResp.Msg.Relation.RelationType)
	}

	// Get relations from Alice.
	fromResp, err := client.GetRelationsFromEntity(ctx, connect.NewRequest(&esv1.GetRelationsFromEntityRequest{
		EntityId: aliceID,
	}))
	if err != nil {
		t.Fatalf("GetRelationsFromEntity: %v", err)
	}
	if len(fromResp.Msg.Relations) != 1 {
		t.Fatalf("expected 1 relation from Alice, got %d", len(fromResp.Msg.Relations))
	}
	if fromResp.Msg.Relations[0].TargetId != acmeID {
		t.Errorf("relation target = %q, want %q", fromResp.Msg.Relations[0].TargetId, acmeID)
	}

	// Get relations to Acme.
	toResp, err := client.GetRelationsToEntity(ctx, connect.NewRequest(&esv1.GetRelationsToEntityRequest{
		EntityId: acmeID,
	}))
	if err != nil {
		t.Fatalf("GetRelationsToEntity: %v", err)
	}
	if len(toResp.Msg.Relations) != 1 {
		t.Fatalf("expected 1 relation to Acme, got %d", len(toResp.Msg.Relations))
	}

	// FindConnectedByType.
	connResp, err := client.FindConnectedByType(ctx, connect.NewRequest(&esv1.FindConnectedByTypeRequest{
		EntityId:      aliceID,
		EntityType:    "entities.v1.Company",
		RelationTypes: []string{"employed_by"},
	}))
	if err != nil {
		t.Fatalf("FindConnectedByType: %v", err)
	}
	if len(connResp.Msg.Entities) != 1 {
		t.Fatalf("expected 1 connected company, got %d", len(connResp.Msg.Entities))
	}
	if connResp.Msg.Entities[0].Id != acmeID {
		t.Errorf("connected entity = %q, want %q", connResp.Msg.Entities[0].Id, acmeID)
	}

	// Clean up.
	_, _ = client.DeleteEntity(ctx, connect.NewRequest(&esv1.DeleteEntityRequest{Id: aliceID}))
	_, _ = client.DeleteEntity(ctx, connect.NewRequest(&esv1.DeleteEntityRequest{Id: acmeID}))
}

// ---------------------------------------------------------------------------
// Upsert relation with data
// ---------------------------------------------------------------------------

func TestE2E_UpsertRelationWithData(t *testing.T) {
	client := startE2E(t)
	ctx := context.Background()

	r1, err := client.InsertEntity(ctx, connect.NewRequest(&esv1.InsertEntityRequest{
		EntityType: "entities.v1.Person",
		Data:       []byte(`{"name":"Carol"}`),
		Confidence: 0.9,
	}))
	if err != nil {
		t.Fatalf("InsertEntity: %v", err)
	}
	r2, err := client.InsertEntity(ctx, connect.NewRequest(&esv1.InsertEntityRequest{
		EntityType: "entities.v1.Company",
		Data:       []byte(`{"name":"TechCo"}`),
		Confidence: 0.9,
	}))
	if err != nil {
		t.Fatalf("InsertEntity: %v", err)
	}

	carolID := r1.Msg.Entity.Id
	techID := r2.Msg.Entity.Id

	relData, _ := json.Marshal(map[string]any{"role": "CTO", "since": "2024"})
	relResp, err := client.UpsertRelation(ctx, connect.NewRequest(&esv1.UpsertRelationRequest{
		SourceId:     carolID,
		TargetId:     techID,
		RelationType: "employed_by",
		Confidence:   0.99,
		Evidence:     "board minutes",
		Data:         relData,
	}))
	if err != nil {
		t.Fatalf("UpsertRelation: %v", err)
	}
	if relResp.Msg.Relation.Confidence != 0.99 {
		t.Errorf("Confidence = %v, want 0.99", relResp.Msg.Relation.Confidence)
	}

	_, _ = client.DeleteEntity(ctx, connect.NewRequest(&esv1.DeleteEntityRequest{Id: carolID}))
	_, _ = client.DeleteEntity(ctx, connect.NewRequest(&esv1.DeleteEntityRequest{Id: techID}))
}
