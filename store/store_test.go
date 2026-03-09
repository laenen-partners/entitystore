package store_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/laenen-partners/entitystore/matching"
	"github.com/laenen-partners/entitystore/store"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	connStr := os.Getenv("ENTITY_TEST_DB")
	if connStr == "" {
		t.Skip("ENTITY_TEST_DB not set; skipping integration test")
	}
	ctx := context.Background()
	s, err := store.New(ctx, connStr)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

func TestInsertAndFindByAnchor(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	data := json.RawMessage(`{"email":"alice@example.com","name":"Alice"}`)
	ent, err := s.InsertEntity(ctx, "entities.v1.Person", data, 0.95)
	if err != nil {
		t.Fatalf("insert entity: %v", err)
	}

	err = s.UpsertAnchors(ctx, ent.ID, "entities.v1.Person", []matching.AnchorQuery{
		{Field: "email", Value: "alice@example.com"},
	})
	if err != nil {
		t.Fatalf("upsert anchors: %v", err)
	}

	found, err := s.FindByAnchors(ctx, "entities.v1.Person", []matching.AnchorQuery{
		{Field: "email", Value: "alice@example.com"},
	}, nil)
	if err != nil {
		t.Fatalf("find by anchors: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("expected 1 match, got %d", len(found))
	}
	if found[0].ID != ent.ID {
		t.Errorf("expected entity %s, got %s", ent.ID, found[0].ID)
	}

	if err := s.DeleteEntity(ctx, ent.ID); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
}

func TestInsertAndFindByTokens(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	data := json.RawMessage(`{"name":"Acme Corp","industry":"technology"}`)
	ent, err := s.InsertEntity(ctx, "entities.v1.Company", data, 0.9)
	if err != nil {
		t.Fatalf("insert entity: %v", err)
	}

	err = s.UpsertTokens(ctx, ent.ID, "entities.v1.Company", "name", []string{"acme", "corp"})
	if err != nil {
		t.Fatalf("upsert tokens: %v", err)
	}

	found, err := s.FindByTokens(ctx, "entities.v1.Company", []string{"acme", "inc"}, 10, nil)
	if err != nil {
		t.Fatalf("find by tokens: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("expected 1 match, got %d", len(found))
	}

	if err := s.DeleteEntity(ctx, ent.ID); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
}

func TestRelations(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	e1, err := s.InsertEntity(ctx, "entities.v1.Person", json.RawMessage(`{"name":"Alice"}`), 0.9)
	if err != nil {
		t.Fatalf("insert e1: %v", err)
	}
	e2, err := s.InsertEntity(ctx, "entities.v1.Company", json.RawMessage(`{"name":"Acme"}`), 0.9)
	if err != nil {
		t.Fatalf("insert e2: %v", err)
	}

	rel, err := s.UpsertRelation(ctx, matching.StoredRelation{
		SourceID:     e1.ID,
		TargetID:     e2.ID,
		RelationType: "employed_by",
		Confidence:   0.95,
		Evidence:     "Alice works at Acme",
	})
	if err != nil {
		t.Fatalf("upsert relation: %v", err)
	}
	if rel.RelationType != "employed_by" {
		t.Errorf("expected relation type employed_by, got %s", rel.RelationType)
	}

	fromRels, err := s.GetRelationsFromEntity(ctx, e1.ID)
	if err != nil {
		t.Fatalf("get relations from: %v", err)
	}
	if len(fromRels) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(fromRels))
	}

	connected, err := s.ConnectedEntities(ctx, e1.ID)
	if err != nil {
		t.Fatalf("connected entities: %v", err)
	}
	if len(connected) != 1 || connected[0].ID != e2.ID {
		t.Errorf("expected connected entity %s, got %v", e2.ID, connected)
	}

	if err := s.DeleteEntity(ctx, e1.ID); err != nil {
		t.Fatalf("cleanup e1: %v", err)
	}
	if err := s.DeleteEntity(ctx, e2.ID); err != nil {
		t.Fatalf("cleanup e2: %v", err)
	}
}

func TestApplyMatchDecision(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	ent, err := s.ApplyMatchDecision(ctx, store.MatchDecisionInput{
		EntityType: "entities.v1.Invoice",
		Data:       json.RawMessage(`{"invoice_number":"INV-001","total":"1000.00"}`),
		Confidence: 0.92,
		Tags:       []string{"tenant:acme", "status:new"},
		Anchors: []matching.AnchorQuery{
			{Field: "invoice_number", Value: "inv-001"},
		},
		Tokens: map[string][]string{
			"description": {"consulting", "services", "q1"},
		},
		Provenance: matching.ProvenanceEntry{
			SourceURN:      "doc-002",
			ExtractedAt:     time.Now(),
			ModelID:         "gemini-2.5-flash",
			Confidence:      0.92,
			Fields:          []string{"invoice_number", "total"},
			MatchMethod:     "create",
			MatchConfidence: 0.0,
		},
	})
	if err != nil {
		t.Fatalf("apply match decision: %v", err)
	}
	if ent.EntityType != "entities.v1.Invoice" {
		t.Errorf("expected entities.v1.Invoice, got %s", ent.EntityType)
	}

	if err := s.DeleteEntity(ctx, ent.ID); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
}

func TestTags(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	ent, err := s.InsertEntity(ctx, "entities.v1.Person", json.RawMessage(`{"name":"Tag Test"}`), 0.9)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	if err := s.SetTags(ctx, ent.ID, []string{"tenant:acme", "pii:true"}); err != nil {
		t.Fatalf("set tags: %v", err)
	}
	got, err := s.GetEntity(ctx, ent.ID)
	if err != nil {
		t.Fatalf("get entity: %v", err)
	}
	if len(got.Tags) != 2 {
		t.Errorf("expected 2 tags after set, got %d: %v", len(got.Tags), got.Tags)
	}

	if err := s.DeleteEntity(ctx, ent.ID); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
}
