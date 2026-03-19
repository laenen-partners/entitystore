package store_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/laenen-partners/entitystore/matching"
	"github.com/laenen-partners/entitystore/store"
)

// helper creates a test entity and returns its ID. Cleans up on test end.
func createTestEntity(t *testing.T, s *store.Store, data proto.Message) string {
	t.Helper()
	ctx := context.Background()
	results, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action:     store.WriteActionCreate,
			Data:       data,
			Confidence: 0.9,
			Provenance: matching.ProvenanceEntry{
				SourceURN: "test:helper", ExtractedAt: time.Now(),
				ModelID: "test", Fields: []string{}, MatchMethod: "create",
			},
		}},
	})
	if err != nil {
		t.Fatalf("createTestEntity: %v", err)
	}
	id := results[0].Entity.ID
	t.Cleanup(func() { _ = s.DeleteEntity(ctx, id) })
	return id
}

// ---------------------------------------------------------------------------
// Update and Merge
// ---------------------------------------------------------------------------

func TestBatchWrite_Update(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	id := createTestEntity(t, s,
		testData(t, map[string]any{"name": "Alice", "title": "Engineer"}))

	newData := testData(t, map[string]any{"name": "Alice Updated", "title": "Senior Engineer", "phone": "+1234"})
	_, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action:          store.WriteActionUpdate,
			MatchedEntityID: id,
			Data:            newData,
			Confidence:      0.98,
			Provenance: matching.ProvenanceEntry{
				SourceURN: "test:update", ExtractedAt: time.Now(),
				ModelID: "test", Fields: []string{"name", "title", "phone"},
				MatchMethod: "anchor", MatchConfidence: 1.0,
			},
		}},
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := s.GetEntity(ctx, id)
	if err != nil {
		t.Fatalf("get entity: %v", err)
	}
	if got.Confidence != 0.98 {
		t.Errorf("confidence = %g, want 0.98", got.Confidence)
	}

	var result structpb.Struct
	if err := got.GetData(&result); err != nil {
		t.Fatalf("GetData: %v", err)
	}
	if result.Fields["name"].GetStringValue() != "Alice Updated" {
		t.Errorf("name = %q", result.Fields["name"].GetStringValue())
	}
}

func TestBatchWrite_Merge(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	id := createTestEntity(t, s,
		testData(t, map[string]any{"name": "Bob", "title": "Engineer", "email": "bob@test.com"}))

	mergeData := testData(t, map[string]any{"title": "Senior Engineer", "phone": "+9999"})
	_, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action:          store.WriteActionMerge,
			MatchedEntityID: id,
			Data:            mergeData,
			Confidence:      0.95,
			Provenance: matching.ProvenanceEntry{
				SourceURN: "test:merge", ExtractedAt: time.Now(),
				ModelID: "test", Fields: []string{"title", "phone"},
				MatchMethod: "token", MatchConfidence: 0.87,
			},
		}},
	})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}

	got, err := s.GetEntity(ctx, id)
	if err != nil {
		t.Fatalf("get entity: %v", err)
	}

	var data map[string]any
	if err := json.Unmarshal(got.Data, &data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if data["title"] != "Senior Engineer" {
		t.Errorf("title = %v, want Senior Engineer", data["title"])
	}
	if data["phone"] != "+9999" {
		t.Errorf("phone = %v, want +9999", data["phone"])
	}
	if data["name"] != "Bob" {
		t.Errorf("name = %v, want Bob (should be preserved)", data["name"])
	}
}

// ---------------------------------------------------------------------------
// Client-generated ID
// ---------------------------------------------------------------------------

func TestBatchWrite_CreateWithClientID(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	clientID := "550e8400-e29b-41d4-a716-446655440099"
	results, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action:     store.WriteActionCreate,
			ID:         clientID,
			Data:       testData(t, map[string]any{"number": "INV-099"}),
			Confidence: 0.9,
			Provenance: matching.ProvenanceEntry{
				SourceURN: "test:clientid", ExtractedAt: time.Now(),
				ModelID: "test", Fields: []string{"number"}, MatchMethod: "create",
			},
		}},
	})
	if err != nil {
		t.Fatalf("create with client ID: %v", err)
	}
	t.Cleanup(func() { _ = s.DeleteEntity(ctx, clientID) })

	if results[0].Entity.ID != clientID {
		t.Errorf("ID = %q, want %q", results[0].Entity.ID, clientID)
	}
}

// ---------------------------------------------------------------------------
// Tags — AddTags and RemoveTag
// ---------------------------------------------------------------------------

func TestAddTags(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	id := createTestEntity(t, s, testData(t, map[string]any{"name": "Tag Add Test"}))

	if err := s.SetTags(ctx, id, []string{"a", "b"}); err != nil {
		t.Fatalf("set tags: %v", err)
	}
	if err := s.AddTags(ctx, id, []string{"c", "d"}); err != nil {
		t.Fatalf("add tags: %v", err)
	}

	got, err := s.GetEntity(ctx, id)
	if err != nil {
		t.Fatalf("get entity: %v", err)
	}
	if len(got.Tags) != 4 {
		t.Errorf("expected 4 tags, got %d: %v", len(got.Tags), got.Tags)
	}
}

func TestRemoveTag(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	id := createTestEntity(t, s, testData(t, map[string]any{"name": "Tag Remove Test"}))

	if err := s.SetTags(ctx, id, []string{"keep", "remove", "also-keep"}); err != nil {
		t.Fatalf("set tags: %v", err)
	}
	if err := s.RemoveTag(ctx, id, "remove"); err != nil {
		t.Fatalf("remove tag: %v", err)
	}

	got, err := s.GetEntity(ctx, id)
	if err != nil {
		t.Fatalf("get entity: %v", err)
	}
	if len(got.Tags) != 2 {
		t.Errorf("expected 2 tags after remove, got %d: %v", len(got.Tags), got.Tags)
	}
}

// ---------------------------------------------------------------------------
// Tag-filtered queries
// ---------------------------------------------------------------------------

func TestFindByAnchors_WithTagFilter(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	results, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action:     store.WriteActionCreate,
			Data:       testData(t, map[string]any{"email": "tagfilter@test.com"}),
			Confidence: 0.9,
			Tags:       []string{"source:crm"},
			Anchors:    []matching.AnchorQuery{{Field: "email", Value: "tagfilter@test.com"}},
			Provenance: matching.ProvenanceEntry{
				SourceURN: "test:tagfilter", ExtractedAt: time.Now(),
				ModelID: "test", Fields: []string{"email"}, MatchMethod: "create",
			},
		}},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := results[0].Entity.ID
	t.Cleanup(func() { _ = s.DeleteEntity(ctx, id) })

	found, err := s.FindByAnchors(ctx, entityType,
		[]matching.AnchorQuery{{Field: "email", Value: "tagfilter@test.com"}},
		&matching.QueryFilter{Tags: []string{"source:crm"}})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(found) != 1 {
		t.Errorf("expected 1 with matching tag, got %d", len(found))
	}

	found, err = s.FindByAnchors(ctx, entityType,
		[]matching.AnchorQuery{{Field: "email", Value: "tagfilter@test.com"}},
		&matching.QueryFilter{Tags: []string{"source:other"}})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(found) != 0 {
		t.Errorf("expected 0 with non-matching tag, got %d", len(found))
	}
}

// ---------------------------------------------------------------------------
// Provenance
// ---------------------------------------------------------------------------

func TestGetProvenanceForEntity(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	results, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action:     store.WriteActionCreate,
			Data:       testData(t, map[string]any{"number": "PROV-001"}),
			Confidence: 0.92,
			Provenance: matching.ProvenanceEntry{
				SourceURN:   "doc:email-42",
				ExtractedAt: time.Now().Truncate(time.Millisecond),
				ModelID:     "gpt-4o",
				Confidence:  0.92,
				Fields:      []string{"number"},
				MatchMethod: "create",
			},
		}},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := results[0].Entity.ID
	t.Cleanup(func() { _ = s.DeleteEntity(ctx, id) })

	entries, err := s.GetProvenanceForEntity(ctx, id)
	if err != nil {
		t.Fatalf("get provenance: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 provenance entry, got %d", len(entries))
	}
	if entries[0].SourceURN != "doc:email-42" {
		t.Errorf("SourceURN = %q", entries[0].SourceURN)
	}
	if entries[0].ModelID != "gpt-4o" {
		t.Errorf("ModelID = %q", entries[0].ModelID)
	}
}

// ---------------------------------------------------------------------------
// Pagination
// ---------------------------------------------------------------------------

func TestGetEntitiesByType(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	for range 3 {
		createTestEntity(t, s, testData(t, map[string]any{"idx": "paginated"}))
		time.Sleep(10 * time.Millisecond)
	}

	page1, err := s.GetEntitiesByType(ctx, entityType, 2, nil)
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("expected 2 on page1, got %d", len(page1))
	}

	cursor := page1[len(page1)-1].UpdatedAt
	page2, err := s.GetEntitiesByType(ctx, entityType, 2, &cursor)
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2) < 1 {
		t.Fatalf("expected at least 1 on page2, got %d", len(page2))
	}
}

// ---------------------------------------------------------------------------
// Relations
// ---------------------------------------------------------------------------

func TestGetRelationsToEntity(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	personID := createTestEntity(t, s, testData(t, map[string]any{"name": "Alice"}))
	companyID := createTestEntity(t, s, testData(t, map[string]any{"name": "Acme"}))

	_, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{UpsertRelation: &store.UpsertRelationOp{
			SourceID: personID, TargetID: companyID,
			RelationType: "works_at", Confidence: 0.95,
		}},
	})
	if err != nil {
		t.Fatalf("upsert relation: %v", err)
	}

	rels, err := s.GetRelationsToEntity(ctx, companyID)
	if err != nil {
		t.Fatalf("get relations to: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 inbound relation, got %d", len(rels))
	}
	if rels[0].SourceID != personID {
		t.Errorf("unexpected source: %s", rels[0].SourceID)
	}
}

func TestFindConnectedByType(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	person := createTestEntity(t, s, testData(t, map[string]any{"name": "Alice"}))
	company1 := createTestEntity(t, s, testData(t, map[string]any{"name": "Acme"}))
	company2 := createTestEntity(t, s, testData(t, map[string]any{"name": "Globex"}))

	_, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{UpsertRelation: &store.UpsertRelationOp{
			SourceID: person, TargetID: company1,
			RelationType: "works_at", Confidence: 0.9,
		}},
		{UpsertRelation: &store.UpsertRelationOp{
			SourceID: person, TargetID: company2,
			RelationType: "invested_in", Confidence: 0.8,
		}},
	})
	if err != nil {
		t.Fatalf("create relations: %v", err)
	}

	found, err := s.FindConnectedByType(ctx, person, entityType, nil, nil)
	if err != nil {
		t.Fatalf("find connected: %v", err)
	}
	if len(found) != 2 {
		t.Errorf("expected 2 connected, got %d", len(found))
	}

	found, err = s.FindConnectedByType(ctx, person, entityType,
		[]string{"works_at"}, nil)
	if err != nil {
		t.Fatalf("find connected filtered: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("expected 1 with works_at, got %d", len(found))
	}
	if found[0].ID != company1 {
		t.Errorf("expected company1, got %s", found[0].ID)
	}
}

func TestFindEntitiesByRelation(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	person := createTestEntity(t, s, testData(t, map[string]any{"name": "FindRel"}))
	company := createTestEntity(t, s, testData(t, map[string]any{"name": "RelCo"}))

	_, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{UpsertRelation: &store.UpsertRelationOp{
			SourceID: person, TargetID: company,
			RelationType: "employed_by", Confidence: 0.9,
		}},
	})
	if err != nil {
		t.Fatalf("create relation: %v", err)
	}

	found, err := s.FindEntitiesByRelation(ctx, entityType, "employed_by", nil)
	if err != nil {
		t.Fatalf("find entities by relation: %v", err)
	}
	hasPersonID := false
	for _, e := range found {
		if e.ID == person {
			hasPersonID = true
		}
	}
	if !hasPersonID {
		t.Error("expected to find person in employed_by relation")
	}
}

// ---------------------------------------------------------------------------
// Relation with typed proto data
// ---------------------------------------------------------------------------

func TestUpsertRelation_WithProtoData(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	e1 := createTestEntity(t, s, testData(t, map[string]any{"name": "A"}))
	e2 := createTestEntity(t, s, testData(t, map[string]any{"name": "B"}))

	relData := testData(t, map[string]any{"role": "CEO", "since": 2020})

	_, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{UpsertRelation: &store.UpsertRelationOp{
			SourceID:     e1,
			TargetID:     e2,
			RelationType: "full_rel",
			Confidence:   0.9,
			Evidence:     "test evidence",
			Implied:      true,
			SourceURN:    "doc:test-123",
			Data:         relData,
		}},
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	rels, err := s.GetRelationsFromEntity(ctx, e1)
	if err != nil {
		t.Fatalf("get relations: %v", err)
	}
	if len(rels) == 0 {
		t.Fatal("expected at least 1 relation")
	}

	r := rels[0]
	if r.Evidence != "test evidence" {
		t.Errorf("Evidence = %q", r.Evidence)
	}
	if !r.Implied {
		t.Error("Implied should be true")
	}
	if r.SourceURN != "doc:test-123" {
		t.Errorf("SourceURN = %q", r.SourceURN)
	}
	if r.DataType != "google.protobuf.Struct" {
		t.Errorf("DataType = %q, want google.protobuf.Struct", r.DataType)
	}

	// Unmarshal the relation data back.
	var result structpb.Struct
	if err := r.GetData(&result); err != nil {
		t.Fatalf("GetData: %v", err)
	}
	if result.Fields["role"].GetStringValue() != "CEO" {
		t.Errorf("Data[role] = %v", result.Fields["role"])
	}
}

func TestUpsertRelation_NilData(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	e1 := createTestEntity(t, s, testData(t, map[string]any{"name": "X"}))
	e2 := createTestEntity(t, s, testData(t, map[string]any{"name": "Y"}))

	_, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{UpsertRelation: &store.UpsertRelationOp{
			SourceID:     e1,
			TargetID:     e2,
			RelationType: "no_data",
			Confidence:   0.8,
		}},
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	rels, err := s.GetRelationsFromEntity(ctx, e1)
	if err != nil {
		t.Fatalf("get relations: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(rels))
	}
	if rels[0].DataType != "" {
		t.Errorf("DataType = %q, want empty", rels[0].DataType)
	}
}

// ---------------------------------------------------------------------------
// Transactions
// ---------------------------------------------------------------------------

func TestTransaction_CommitAndRollback(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	t.Run("commit", func(t *testing.T) {
		tx, err := s.Tx(ctx)
		if err != nil {
			t.Fatalf("begin tx: %v", err)
		}

		ent, err := tx.WriteEntity(ctx, &store.WriteEntityOp{
			Action:     store.WriteActionCreate,
			Data:       testData(t, map[string]any{"name": "TxCommit"}),
			Confidence: 0.9,
			Provenance: matching.ProvenanceEntry{
				SourceURN: "test:tx", ExtractedAt: time.Now(),
				ModelID: "test", Fields: []string{"name"}, MatchMethod: "create",
			},
		})
		if err != nil {
			t.Fatalf("write in tx: %v", err)
		}
		t.Cleanup(func() { _ = s.DeleteEntity(ctx, ent.ID) })

		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit: %v", err)
		}

		got, err := s.GetEntity(ctx, ent.ID)
		if err != nil {
			t.Fatalf("get after commit: %v", err)
		}
		if got.ID != ent.ID {
			t.Errorf("ID mismatch after commit")
		}
	})

	t.Run("rollback", func(t *testing.T) {
		tx, err := s.Tx(ctx)
		if err != nil {
			t.Fatalf("begin tx: %v", err)
		}

		ent, err := tx.WriteEntity(ctx, &store.WriteEntityOp{
			Action:     store.WriteActionCreate,
			Data:       testData(t, map[string]any{"name": "TxRollback"}),
			Confidence: 0.9,
			Provenance: matching.ProvenanceEntry{
				SourceURN: "test:tx-rb", ExtractedAt: time.Now(),
				ModelID: "test", Fields: []string{"name"}, MatchMethod: "create",
			},
		})
		if err != nil {
			t.Fatalf("write in tx: %v", err)
		}

		if err := tx.Rollback(ctx); err != nil {
			t.Fatalf("rollback: %v", err)
		}

		_, err = s.GetEntity(ctx, ent.ID)
		if err == nil {
			t.Error("expected error getting entity after rollback")
			_ = s.DeleteEntity(ctx, ent.ID)
		}
	})
}

func TestTransaction_UpsertRelation(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	person := createTestEntity(t, s, testData(t, map[string]any{"name": "TxRelPerson"}))
	company := createTestEntity(t, s, testData(t, map[string]any{"name": "TxRelCo"}))

	tx, err := s.Tx(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}

	rel, err := tx.UpsertRelation(ctx, &store.UpsertRelationOp{
		SourceID:     person,
		TargetID:     company,
		RelationType: "tx_works_at",
		Confidence:   0.88,
		Evidence:     "Transaction test",
	})
	if err != nil {
		t.Fatalf("upsert relation in tx: %v", err)
	}
	if rel.RelationType != "tx_works_at" {
		t.Errorf("RelationType = %q", rel.RelationType)
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	rels, err := s.GetRelationsFromEntity(ctx, person)
	if err != nil {
		t.Fatalf("get relations: %v", err)
	}
	found := false
	for _, r := range rels {
		if r.RelationType == "tx_works_at" {
			found = true
		}
	}
	if !found {
		t.Error("relation not found after tx commit")
	}
}

// ---------------------------------------------------------------------------
// Update with anchors and tags
// ---------------------------------------------------------------------------

func TestBatchWrite_UpdateAddsTagsAndAnchors(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	id := createTestEntity(t, s, testData(t, map[string]any{"email": "update-anchor@test.com"}))

	_, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action:          store.WriteActionUpdate,
			MatchedEntityID: id,
			Data:            testData(t, map[string]any{"email": "update-anchor@test.com", "phone": "+1234"}),
			Confidence:      0.95,
			Tags:            []string{"updated:true"},
			Anchors:         []matching.AnchorQuery{{Field: "email", Value: "update-anchor@test.com"}},
			Provenance: matching.ProvenanceEntry{
				SourceURN: "test:update-anch", ExtractedAt: time.Now(),
				ModelID: "test", MatchMethod: "anchor", MatchConfidence: 1.0,
			},
		}},
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := s.GetEntity(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	hasTag := false
	for _, tag := range got.Tags {
		if tag == "updated:true" {
			hasTag = true
		}
	}
	if !hasTag {
		t.Error("expected updated:true tag")
	}
}

// ---------------------------------------------------------------------------
// Empty batch
// ---------------------------------------------------------------------------

func TestBatchWrite_EmptyOp(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	_, err := s.BatchWrite(ctx, []store.BatchWriteOp{{}})
	if err == nil {
		t.Error("expected error for empty operation")
	}
}

// ---------------------------------------------------------------------------
// Entity GetData convenience
// ---------------------------------------------------------------------------

func TestStoredEntity_GetData(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	id := createTestEntity(t, s, testData(t, map[string]any{"name": "GetDataTest", "age": 30}))

	got, err := s.GetEntity(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	var result structpb.Struct
	if err := got.GetData(&result); err != nil {
		t.Fatalf("GetData: %v", err)
	}
	if result.Fields["name"].GetStringValue() != "GetDataTest" {
		t.Errorf("name = %q", result.Fields["name"].GetStringValue())
	}
}

// ---------------------------------------------------------------------------
// Embedding via BatchWrite
// ---------------------------------------------------------------------------

func TestBatchWrite_CreateWithEmbedding(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	vec := make([]float32, 768)
	vec[0] = 1.0

	results, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action:     store.WriteActionCreate,
			Data:       testData(t, map[string]any{"name": "Embed Test"}),
			Confidence: 0.9,
			Embedding:  vec,
			Provenance: matching.ProvenanceEntry{
				SourceURN: "test:embed", ExtractedAt: time.Now(),
				ModelID: "test", Fields: []string{"name"}, MatchMethod: "create",
			},
		}},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := results[0].Entity.ID
	t.Cleanup(func() { _ = s.DeleteEntity(ctx, id) })

	found, err := s.FindByEmbedding(ctx, entityType, vec, 5, nil)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	hasID := false
	for _, e := range found {
		if e.ID == id {
			hasID = true
		}
	}
	if !hasID {
		t.Error("entity not found by embedding")
	}
}

// ---------------------------------------------------------------------------
// AnyTags (OR-based tag filtering)
// ---------------------------------------------------------------------------

func TestFindByAnchors_AnyTags(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	// Create 3 entities with different workspace tags and different anchors.
	create := func(email, tag string) string {
		t.Helper()
		results, err := s.BatchWrite(ctx, []store.BatchWriteOp{
			{WriteEntity: &store.WriteEntityOp{
				Action:     store.WriteActionCreate,
				Data:       testData(t, map[string]any{"email": email}),
				Confidence: 0.9,
				Tags:       []string{tag},
				Anchors:    []matching.AnchorQuery{{Field: "email", Value: email}},
				Provenance: matching.ProvenanceEntry{
					SourceURN: "test:anytags", ExtractedAt: time.Now(),
					ModelID: "test", Fields: []string{"email"}, MatchMethod: "create",
				},
			}},
		})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		id := results[0].Entity.ID
		t.Cleanup(func() { _ = s.DeleteEntity(ctx, id) })
		return id
	}

	id1 := create("anytag1@test.com", "ws:family")
	id2 := create("anytag2@test.com", "ws:holding")
	id3 := create("anytag3@test.com", "ws:foundation")
	_ = id3

	// Search all 3 anchors with AnyTags filter — should return only 2.
	allAnchors := []matching.AnchorQuery{
		{Field: "email", Value: "anytag1@test.com"},
		{Field: "email", Value: "anytag2@test.com"},
		{Field: "email", Value: "anytag3@test.com"},
	}

	found, err := s.FindByAnchors(ctx, entityType, allAnchors,
		&matching.QueryFilter{AnyTags: []string{"ws:family", "ws:holding"}})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(found) != 2 {
		t.Fatalf("expected 2 entities with AnyTags, got %d", len(found))
	}
	ids := map[string]bool{}
	for _, e := range found {
		ids[e.ID] = true
	}
	if !ids[id1] || !ids[id2] {
		t.Errorf("expected id1 and id2, got %v", ids)
	}

	// AnyTags + Tags combined — AND + OR.
	_ = s.AddTags(ctx, id1, []string{"role:customer"})

	found, err = s.FindByAnchors(ctx, entityType, allAnchors,
		&matching.QueryFilter{
			Tags:    []string{"role:customer"},           // AND: must have this
			AnyTags: []string{"ws:family", "ws:holding"}, // OR: must have at least one
		})
	if err != nil {
		t.Fatalf("find combined: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("expected 1 entity with combined filter, got %d", len(found))
	}
	if found[0].ID != id1 {
		t.Errorf("expected id1, got %s", found[0].ID)
	}

	// No filter — returns all 3.
	found, err = s.FindByAnchors(ctx, entityType, allAnchors, nil)
	if err != nil {
		t.Fatalf("find no filter: %v", err)
	}
	if len(found) != 3 {
		t.Errorf("expected 3 with no filter, got %d", len(found))
	}
}

func TestFindByTokens_AnyTags(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	createWithToken := func(tag string) string {
		t.Helper()
		results, err := s.BatchWrite(ctx, []store.BatchWriteOp{
			{WriteEntity: &store.WriteEntityOp{
				Action:     store.WriteActionCreate,
				Data:       testData(t, map[string]any{"name": "AnyTagToken"}),
				Confidence: 0.9,
				Tags:       []string{tag},
				Tokens:     map[string][]string{"name": {"anytagtoken"}},
				Provenance: matching.ProvenanceEntry{
					SourceURN: "test:anytags-token", ExtractedAt: time.Now(),
					ModelID: "test", Fields: []string{"name"}, MatchMethod: "create",
				},
			}},
		})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		id := results[0].Entity.ID
		t.Cleanup(func() { _ = s.DeleteEntity(ctx, id) })
		return id
	}

	_ = createWithToken("ws:alpha")
	_ = createWithToken("ws:beta")
	_ = createWithToken("ws:gamma")

	// AnyTags OR filter — should return 2 of 3.
	found, err := s.FindByTokens(ctx, entityType, []string{"anytagtoken"}, 10,
		&matching.QueryFilter{AnyTags: []string{"ws:alpha", "ws:beta"}})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(found) != 2 {
		t.Errorf("expected 2 with AnyTags, got %d", len(found))
	}
}

// ---------------------------------------------------------------------------
// ExcludeTag / UnlessTags (conditional visibility)
// ---------------------------------------------------------------------------

func TestExcludeTagUnlessFilter(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	// entity1: open (no vis:restricted)
	// entity2: restricted, has ag:finances
	// entity3: restricted, has ag:vehicles
	create := func(email string, tags []string) string {
		t.Helper()
		results, err := s.BatchWrite(ctx, []store.BatchWriteOp{
			{WriteEntity: &store.WriteEntityOp{
				Action:     store.WriteActionCreate,
				Data:       testData(t, map[string]any{"email": email}),
				Confidence: 0.9,
				Tags:       tags,
				Anchors:    []matching.AnchorQuery{{Field: "email", Value: email}},
				Provenance: matching.ProvenanceEntry{
					SourceURN: "test:exclude", ExtractedAt: time.Now(),
					ModelID: "test", Fields: []string{"email"}, MatchMethod: "create",
				},
			}},
		})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		id := results[0].Entity.ID
		t.Cleanup(func() { _ = s.DeleteEntity(ctx, id) })
		return id
	}

	id1 := create("excl-open@test.com", []string{"ws:family"})
	id2 := create("excl-fin@test.com", []string{"ws:family", "vis:restricted", "ag:finances"})
	id3 := create("excl-veh@test.com", []string{"ws:family", "vis:restricted", "ag:vehicles"})

	allAnchors := []matching.AnchorQuery{
		{Field: "email", Value: "excl-open@test.com"},
		{Field: "email", Value: "excl-fin@test.com"},
		{Field: "email", Value: "excl-veh@test.com"},
	}

	// Test 1: No exclude filter → returns all 3.
	found, err := s.FindByAnchors(ctx, entityType, allAnchors, nil)
	if err != nil {
		t.Fatalf("no filter: %v", err)
	}
	if len(found) != 3 {
		t.Fatalf("test1: expected 3, got %d", len(found))
	}

	// Test 2: ExcludeTag="vis:restricted", UnlessTags=[] → only open entity.
	found, err = s.FindByAnchors(ctx, entityType, allAnchors,
		&matching.QueryFilter{ExcludeTag: "vis:restricted"})
	if err != nil {
		t.Fatalf("exclude only: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("test2: expected 1, got %d", len(found))
	}
	if found[0].ID != id1 {
		t.Errorf("test2: expected id1, got %s", found[0].ID)
	}

	// Test 3: ExcludeTag="vis:restricted", UnlessTags=["ag:finances"] → open + finances.
	found, err = s.FindByAnchors(ctx, entityType, allAnchors,
		&matching.QueryFilter{ExcludeTag: "vis:restricted", UnlessTags: []string{"ag:finances"}})
	if err != nil {
		t.Fatalf("unless finances: %v", err)
	}
	if len(found) != 2 {
		t.Fatalf("test3: expected 2, got %d", len(found))
	}
	ids := map[string]bool{}
	for _, e := range found {
		ids[e.ID] = true
	}
	if !ids[id1] || !ids[id2] {
		t.Errorf("test3: expected id1+id2, got %v", ids)
	}

	// Test 4: ExcludeTag="vis:restricted", UnlessTags=["ag:finances","ag:vehicles"] → all 3.
	found, err = s.FindByAnchors(ctx, entityType, allAnchors,
		&matching.QueryFilter{ExcludeTag: "vis:restricted", UnlessTags: []string{"ag:finances", "ag:vehicles"}})
	if err != nil {
		t.Fatalf("unless both: %v", err)
	}
	if len(found) != 3 {
		t.Fatalf("test4: expected 3, got %d", len(found))
	}

	// Test 5: Combined with AnyTags — scope to workspace + visibility.
	found, err = s.FindByAnchors(ctx, entityType, allAnchors,
		&matching.QueryFilter{
			AnyTags:    []string{"ws:family"},
			ExcludeTag: "vis:restricted",
			UnlessTags: []string{"ag:finances"},
		})
	if err != nil {
		t.Fatalf("combined: %v", err)
	}
	if len(found) != 2 {
		t.Fatalf("test5: expected 2, got %d", len(found))
	}

	_ = id3 // used in test 4
}
