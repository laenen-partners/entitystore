package store_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/laenen-partners/entitystore/matching"
	"github.com/laenen-partners/entitystore/store"
)

// helper creates a test entity and returns its ID. Cleans up on test end.
func createTestEntity(t *testing.T, s *store.Store, entityType string, data json.RawMessage) string {
	t.Helper()
	ctx := context.Background()
	results, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action:     store.WriteActionCreate,
			EntityType: entityType,
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

	id := createTestEntity(t, s, "test.v1.Person",
		json.RawMessage(`{"name":"Alice","title":"Engineer"}`))

	// Full update — replaces all data.
	newData := json.RawMessage(`{"name":"Alice Updated","title":"Senior Engineer","phone":"+1234"}`)
	_, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action:          store.WriteActionUpdate,
			EntityType:      "test.v1.Person",
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

	var data map[string]string
	if err := json.Unmarshal(got.Data, &data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if data["name"] != "Alice Updated" {
		t.Errorf("name = %q", data["name"])
	}
	if data["phone"] != "+1234" {
		t.Errorf("phone = %q", data["phone"])
	}
}

func TestBatchWrite_Merge(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	id := createTestEntity(t, s, "test.v1.Person",
		json.RawMessage(`{"name":"Bob","title":"Engineer","email":"bob@test.com"}`))

	// Merge — only updates provided fields, keeps others.
	mergeData := json.RawMessage(`{"title":"Senior Engineer","phone":"+9999"}`)
	_, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action:          store.WriteActionMerge,
			EntityType:      "test.v1.Person",
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

	var data map[string]string
	if err := json.Unmarshal(got.Data, &data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Merged field should be updated.
	if data["title"] != "Senior Engineer" {
		t.Errorf("title = %q, want Senior Engineer", data["title"])
	}
	// New field from merge.
	if data["phone"] != "+9999" {
		t.Errorf("phone = %q, want +9999", data["phone"])
	}
	// Original field should be preserved.
	if data["name"] != "Bob" {
		t.Errorf("name = %q, want Bob (should be preserved)", data["name"])
	}
	if data["email"] != "bob@test.com" {
		t.Errorf("email = %q, want bob@test.com (should be preserved)", data["email"])
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
			EntityType: "test.v1.Invoice",
			Data:       json.RawMessage(`{"number":"INV-099"}`),
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

	got, err := s.GetEntity(ctx, clientID)
	if err != nil {
		t.Fatalf("get entity: %v", err)
	}
	if got.ID != clientID {
		t.Errorf("get by client ID failed, got %q", got.ID)
	}
}

// ---------------------------------------------------------------------------
// Tags — AddTags and RemoveTag
// ---------------------------------------------------------------------------

func TestAddTags(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	id := createTestEntity(t, s, "test.v1.Person",
		json.RawMessage(`{"name":"Tag Add Test"}`))

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

	id := createTestEntity(t, s, "test.v1.Person",
		json.RawMessage(`{"name":"Tag Remove Test"}`))

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
	for _, tag := range got.Tags {
		if tag == "remove" {
			t.Error("tag 'remove' should have been removed")
		}
	}
}

// ---------------------------------------------------------------------------
// Tag-filtered queries
// ---------------------------------------------------------------------------

func TestFindByAnchors_WithTagFilter(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	// Create entity with tags and anchor.
	results, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action:     store.WriteActionCreate,
			EntityType: "test.v1.Person",
			Data:       json.RawMessage(`{"email":"tagfilter@test.com"}`),
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

	// Matching tag — should find.
	found, err := s.FindByAnchors(ctx, "test.v1.Person",
		[]matching.AnchorQuery{{Field: "email", Value: "tagfilter@test.com"}},
		&matching.QueryFilter{Tags: []string{"source:crm"}})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(found) != 1 {
		t.Errorf("expected 1 with matching tag, got %d", len(found))
	}

	// Non-matching tag — should not find.
	found, err = s.FindByAnchors(ctx, "test.v1.Person",
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

	now := time.Now().Truncate(time.Millisecond)
	results, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action:     store.WriteActionCreate,
			EntityType: "test.v1.Invoice",
			Data:       json.RawMessage(`{"number":"PROV-001"}`),
			Confidence: 0.92,
			Provenance: matching.ProvenanceEntry{
				SourceURN:       "doc:email-42",
				ExtractedAt:     now,
				ModelID:         "gpt-4o",
				Confidence:      0.92,
				Fields:          []string{"number"},
				MatchMethod:     "create",
				MatchConfidence: 0.0,
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
	p := entries[0]
	if p.SourceURN != "doc:email-42" {
		t.Errorf("SourceURN = %q", p.SourceURN)
	}
	if p.ModelID != "gpt-4o" {
		t.Errorf("ModelID = %q", p.ModelID)
	}
	if p.Confidence != 0.92 {
		t.Errorf("Confidence = %g", p.Confidence)
	}
	if p.MatchMethod != "create" {
		t.Errorf("MatchMethod = %q", p.MatchMethod)
	}
}

// ---------------------------------------------------------------------------
// Pagination — GetEntitiesByType
// ---------------------------------------------------------------------------

func TestGetEntitiesByType(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	// Create 3 entities.
	ids := make([]string, 3)
	for i := range 3 {
		ids[i] = createTestEntity(t, s, "test.v1.Paginated",
			json.RawMessage(`{"index":"`+string(rune('A'+i))+`"}`))
		time.Sleep(10 * time.Millisecond) // ensure distinct timestamps
	}

	// Page 1: get 2.
	page1, err := s.GetEntitiesByType(ctx, "test.v1.Paginated", 2, nil)
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("expected 2 on page1, got %d", len(page1))
	}

	// Page 2: use cursor from last entity.
	cursor := page1[len(page1)-1].UpdatedAt
	page2, err := s.GetEntitiesByType(ctx, "test.v1.Paginated", 2, &cursor)
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2) < 1 {
		t.Fatalf("expected at least 1 on page2, got %d", len(page2))
	}

	// Ensure no overlap.
	page1IDs := map[string]bool{}
	for _, e := range page1 {
		page1IDs[e.ID] = true
	}
	for _, e := range page2 {
		if page1IDs[e.ID] {
			t.Errorf("entity %s appears on both pages", e.ID)
		}
	}
}

// ---------------------------------------------------------------------------
// Relations — GetRelationsToEntity, FindConnectedByType, FindEntitiesByRelation
// ---------------------------------------------------------------------------

func TestGetRelationsToEntity(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	personID := createTestEntity(t, s, "test.v1.Person", json.RawMessage(`{"name":"Alice"}`))
	companyID := createTestEntity(t, s, "test.v1.Company", json.RawMessage(`{"name":"Acme"}`))

	_, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{UpsertRelation: &store.UpsertRelationOp{
			SourceID: personID, TargetID: companyID,
			RelationType: "works_at", Confidence: 0.95,
		}},
	})
	if err != nil {
		t.Fatalf("upsert relation: %v", err)
	}

	// Inbound relations to company.
	rels, err := s.GetRelationsToEntity(ctx, companyID)
	if err != nil {
		t.Fatalf("get relations to: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 inbound relation, got %d", len(rels))
	}
	if rels[0].SourceID != personID || rels[0].TargetID != companyID {
		t.Errorf("unexpected relation: %+v", rels[0])
	}
}

func TestFindConnectedByType(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	person := createTestEntity(t, s, "test.v1.Person", json.RawMessage(`{"name":"Alice"}`))
	company1 := createTestEntity(t, s, "test.v1.Company", json.RawMessage(`{"name":"Acme"}`))
	company2 := createTestEntity(t, s, "test.v1.Company", json.RawMessage(`{"name":"Globex"}`))

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

	// Find all connected companies (any relation type).
	found, err := s.FindConnectedByType(ctx, person, "test.v1.Company", nil, nil)
	if err != nil {
		t.Fatalf("find connected: %v", err)
	}
	if len(found) != 2 {
		t.Errorf("expected 2 connected companies, got %d", len(found))
	}

	// Filter by relation type.
	found, err = s.FindConnectedByType(ctx, person, "test.v1.Company",
		[]string{"works_at"}, nil)
	if err != nil {
		t.Fatalf("find connected filtered: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("expected 1 with works_at filter, got %d", len(found))
	}
	if found[0].ID != company1 {
		t.Errorf("expected company1, got %s", found[0].ID)
	}
}

func TestFindEntitiesByRelation(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	person := createTestEntity(t, s, "test.v1.Person", json.RawMessage(`{"name":"FindRel"}`))
	company := createTestEntity(t, s, "test.v1.Company", json.RawMessage(`{"name":"RelCo"}`))

	_, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{UpsertRelation: &store.UpsertRelationOp{
			SourceID: person, TargetID: company,
			RelationType: "employed_by", Confidence: 0.9,
		}},
	})
	if err != nil {
		t.Fatalf("create relation: %v", err)
	}

	// Find persons that have an "employed_by" relation.
	found, err := s.FindEntitiesByRelation(ctx, "test.v1.Person", "employed_by", nil)
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
// Transactions
// ---------------------------------------------------------------------------

func TestTransaction_CommitAndRollback(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	// Commit path.
	t.Run("commit", func(t *testing.T) {
		tx, err := s.Tx(ctx)
		if err != nil {
			t.Fatalf("begin tx: %v", err)
		}

		ent, err := tx.WriteEntity(ctx, &store.WriteEntityOp{
			Action:     store.WriteActionCreate,
			EntityType: "test.v1.TxPerson",
			Data:       json.RawMessage(`{"name":"TxCommit"}`),
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

		// Should be visible after commit.
		got, err := s.GetEntity(ctx, ent.ID)
		if err != nil {
			t.Fatalf("get after commit: %v", err)
		}
		if got.ID != ent.ID {
			t.Errorf("ID mismatch after commit")
		}
	})

	// Rollback path.
	t.Run("rollback", func(t *testing.T) {
		tx, err := s.Tx(ctx)
		if err != nil {
			t.Fatalf("begin tx: %v", err)
		}

		ent, err := tx.WriteEntity(ctx, &store.WriteEntityOp{
			Action:     store.WriteActionCreate,
			EntityType: "test.v1.TxPerson",
			Data:       json.RawMessage(`{"name":"TxRollback"}`),
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

		// Should NOT be visible after rollback.
		_, err = s.GetEntity(ctx, ent.ID)
		if err == nil {
			t.Error("expected error getting entity after rollback")
			_ = s.DeleteEntity(ctx, ent.ID) // cleanup just in case
		}
	})
}

func TestTransaction_UpsertRelation(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	// Pre-create entities outside tx.
	person := createTestEntity(t, s, "test.v1.Person", json.RawMessage(`{"name":"TxRelPerson"}`))
	company := createTestEntity(t, s, "test.v1.Company", json.RawMessage(`{"name":"TxRelCo"}`))

	tx, err := s.Tx(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}

	rel, err := tx.UpsertRelation(ctx, matching.StoredRelation{
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

	// Verify relation is visible.
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
// Relation with optional fields (SourceURN, Data)
// ---------------------------------------------------------------------------

func TestUpsertRelation_WithOptionalFields(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	e1 := createTestEntity(t, s, "test.v1.Person", json.RawMessage(`{"name":"A"}`))
	e2 := createTestEntity(t, s, "test.v1.Company", json.RawMessage(`{"name":"B"}`))

	_, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{UpsertRelation: &store.UpsertRelationOp{
			SourceID:     e1,
			TargetID:     e2,
			RelationType: "full_rel",
			Confidence:   0.9,
			Evidence:     "test evidence",
			Implied:      true,
			SourceURN:    "doc:test-123",
			Data:         map[string]any{"role": "CEO", "since": 2020},
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
	if r.Data["role"] != "CEO" {
		t.Errorf("Data[role] = %v", r.Data["role"])
	}
}

// ---------------------------------------------------------------------------
// Update with new anchors and tags
// ---------------------------------------------------------------------------

func TestBatchWrite_UpdateAddsTagsAndAnchors(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	id := createTestEntity(t, s, "test.v1.Person",
		json.RawMessage(`{"email":"update-anchor@test.com"}`))

	_, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action:          store.WriteActionUpdate,
			EntityType:      "test.v1.Person",
			MatchedEntityID: id,
			Data:            json.RawMessage(`{"email":"update-anchor@test.com","phone":"+1234"}`),
			Confidence:      0.95,
			Tags:            []string{"updated:true"},
			Anchors: []matching.AnchorQuery{
				{Field: "email", Value: "update-anchor@test.com"},
			},
			Provenance: matching.ProvenanceEntry{
				SourceURN: "test:update-anch", ExtractedAt: time.Now(),
				ModelID: "test", MatchMethod: "anchor", MatchConfidence: 1.0,
			},
		}},
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	// Verify tags were added.
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
		t.Error("expected updated:true tag after update")
	}

	// Verify anchor works.
	found, err := s.FindByAnchors(ctx, "test.v1.Person",
		[]matching.AnchorQuery{{Field: "email", Value: "update-anchor@test.com"}}, nil)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(found) != 1 || found[0].ID != id {
		t.Errorf("expected to find entity by anchor after update")
	}
}

// ---------------------------------------------------------------------------
// Empty batch operation
// ---------------------------------------------------------------------------

func TestBatchWrite_EmptyOp(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	_, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{}, // neither WriteEntity nor UpsertRelation
	})
	if err == nil {
		t.Error("expected error for empty operation")
	}
}

// ---------------------------------------------------------------------------
// Embedding with update via BatchWrite
// ---------------------------------------------------------------------------

func TestBatchWrite_CreateWithEmbedding(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	vec := make([]float32, 768)
	vec[0] = 1.0

	results, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action:     store.WriteActionCreate,
			EntityType: "test.v1.Person",
			Data:       json.RawMessage(`{"name":"Embed Test"}`),
			Confidence: 0.9,
			Embedding:  vec,
			Provenance: matching.ProvenanceEntry{
				SourceURN: "test:embed", ExtractedAt: time.Now(),
				ModelID: "test", Fields: []string{"name"}, MatchMethod: "create",
			},
		}},
	})
	if err != nil {
		t.Fatalf("create with embedding: %v", err)
	}
	id := results[0].Entity.ID
	t.Cleanup(func() { _ = s.DeleteEntity(ctx, id) })

	// Should be findable by embedding.
	found, err := s.FindByEmbedding(ctx, "test.v1.Person", vec, 5, nil)
	if err != nil {
		t.Fatalf("find by embedding: %v", err)
	}
	hasID := false
	for _, e := range found {
		if e.ID == id {
			hasID = true
		}
	}
	if !hasID {
		t.Error("entity not found by embedding after create")
	}
}
