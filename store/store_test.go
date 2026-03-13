package store_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/laenen-partners/entitystore/matching"
	"github.com/laenen-partners/entitystore/store"
)

// sharedConnStr caches the connection string so all tests share one container.
var _sharedConnStr string

func sharedTestStore(t *testing.T) *store.Store {
	t.Helper()
	ctx := context.Background()

	if _sharedConnStr == "" {
		pg, err := postgres.Run(ctx,
			"pgvector/pgvector:pg17",
			postgres.WithDatabase("entitystore_test"),
			postgres.WithUsername("test"),
			postgres.WithPassword("test"),
			postgres.BasicWaitStrategies(),
		)
		if err != nil {
			t.Fatalf("start postgres container: %v", err)
		}
		// Container lives for the duration of the test binary.
		// Not cleaned up per-test to amortize startup cost.
		connStr, err := pg.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			t.Fatalf("get connection string: %v", err)
		}
		if err := store.Migrate(connStr); err != nil {
			t.Fatalf("migrate: %v", err)
		}
		_sharedConnStr = connStr
	}

	pool, err := pgxpool.New(ctx, _sharedConnStr)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	t.Cleanup(pool.Close)

	return store.NewFromPool(pool)
}

func TestBatchWrite_CreateAndFindByAnchor(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	data := json.RawMessage(`{"email":"alice@example.com","name":"Alice"}`)
	results, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action:     store.WriteActionCreate,
			EntityType: "entities.v1.Person",
			Data:       data,
			Confidence: 0.95,
			Anchors: []matching.AnchorQuery{
				{Field: "email", Value: "alice@example.com"},
			},
			Provenance: matching.ProvenanceEntry{
				SourceURN:   "test:anchor",
				ExtractedAt: time.Now(),
				ModelID:     "test",
				Fields:      []string{"email", "name"},
				MatchMethod: "create",
			},
		}},
	})
	if err != nil {
		t.Fatalf("batch write: %v", err)
	}
	if len(results) != 1 || results[0].Entity == nil {
		t.Fatal("expected 1 entity result")
	}
	ent := results[0].Entity

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

func TestBatchWrite_CreateWithTokens(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	data := json.RawMessage(`{"name":"Acme Corp","industry":"technology"}`)
	results, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action:     store.WriteActionCreate,
			EntityType: "entities.v1.Company",
			Data:       data,
			Confidence: 0.9,
			Tokens:     map[string][]string{"name": {"acme", "corp"}},
			Provenance: matching.ProvenanceEntry{
				SourceURN:   "test:tokens",
				ExtractedAt: time.Now(),
				ModelID:     "test",
				Fields:      []string{"name"},
				MatchMethod: "create",
			},
		}},
	})
	if err != nil {
		t.Fatalf("batch write: %v", err)
	}
	ent := results[0].Entity

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

func TestBatchWrite_MixedEntitiesAndRelations(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	// Create two entities and a relation in a single batch.
	results, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action:     store.WriteActionCreate,
			EntityType: "entities.v1.Person",
			Data:       json.RawMessage(`{"name":"Alice"}`),
			Confidence: 0.9,
			Provenance: matching.ProvenanceEntry{
				SourceURN: "test:mixed", ExtractedAt: time.Now(),
				ModelID: "test", Fields: []string{"name"}, MatchMethod: "create",
			},
		}},
		{WriteEntity: &store.WriteEntityOp{
			Action:     store.WriteActionCreate,
			EntityType: "entities.v1.Company",
			Data:       json.RawMessage(`{"name":"Acme"}`),
			Confidence: 0.9,
			Provenance: matching.ProvenanceEntry{
				SourceURN: "test:mixed", ExtractedAt: time.Now(),
				ModelID: "test", Fields: []string{"name"}, MatchMethod: "create",
			},
		}},
	})
	if err != nil {
		t.Fatalf("batch write entities: %v", err)
	}
	e1 := results[0].Entity
	e2 := results[1].Entity

	// Now upsert a relation between them.
	relResults, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{UpsertRelation: &store.UpsertRelationOp{
			SourceID:     e1.ID,
			TargetID:     e2.ID,
			RelationType: "employed_by",
			Confidence:   0.95,
			Evidence:     "Alice works at Acme",
		}},
	})
	if err != nil {
		t.Fatalf("batch write relation: %v", err)
	}
	if len(relResults) != 1 || relResults[0].Relation == nil {
		t.Fatal("expected 1 relation result")
	}
	if relResults[0].Relation.RelationType != "employed_by" {
		t.Errorf("expected relation type employed_by, got %s", relResults[0].Relation.RelationType)
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

func TestBatchWrite_CreateWithProvenance(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	results, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action:     store.WriteActionCreate,
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
				SourceURN:       "doc-002",
				ExtractedAt:     time.Now(),
				ModelID:         "gemini-2.5-flash",
				Confidence:      0.92,
				Fields:          []string{"invoice_number", "total"},
				MatchMethod:     "create",
				MatchConfidence: 0.0,
			},
		}},
	})
	if err != nil {
		t.Fatalf("batch write: %v", err)
	}
	ent := results[0].Entity
	if ent.EntityType != "entities.v1.Invoice" {
		t.Errorf("expected entities.v1.Invoice, got %s", ent.EntityType)
	}

	if err := s.DeleteEntity(ctx, ent.ID); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
}

func TestTags(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	results, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action:     store.WriteActionCreate,
			EntityType: "entities.v1.Person",
			Data:       json.RawMessage(`{"name":"Tag Test"}`),
			Confidence: 0.9,
			Provenance: matching.ProvenanceEntry{
				SourceURN: "test:tags", ExtractedAt: time.Now(),
				ModelID: "test", Fields: []string{"name"}, MatchMethod: "create",
			},
		}},
	})
	if err != nil {
		t.Fatalf("batch write: %v", err)
	}
	ent := results[0].Entity

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
