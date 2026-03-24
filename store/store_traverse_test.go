package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/laenen-partners/entitystore"
	"github.com/laenen-partners/entitystore/matching"
	"github.com/laenen-partners/entitystore/store"
)

// createTraverseEntity creates an entity and returns its ID. Cleaned up on test end.
func createTraverseEntity(t *testing.T, s *store.Store, name string, tags []string) string {
	t.Helper()
	ctx := context.Background()
	results, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action:     store.WriteActionCreate,
			Data:       testData(t, map[string]any{"name": name}),
			Confidence: 0.9,
			Tags:       tags,
			Provenance: matching.ProvenanceEntry{
				SourceURN:   "test:traverse",
				ExtractedAt: time.Now(),
				ModelID:     "test",
				MatchMethod: "create",
			},
		}},
	})
	if err != nil {
		t.Fatalf("create entity %s: %v", name, err)
	}
	id := results[0].Entity.ID
	t.Cleanup(func() { _ = s.DeleteEntity(ctx, id) })
	return id
}


func linkEntities(t *testing.T, s *store.Store, sourceID, targetID, relationType string, confidence float64) {
	t.Helper()
	ctx := context.Background()
	_, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{UpsertRelation: &store.UpsertRelationOp{
			SourceID:     sourceID,
			TargetID:     targetID,
			RelationType: relationType,
			Confidence:   confidence,
		}},
	})
	if err != nil {
		t.Fatalf("link %s -> %s: %v", sourceID, targetID, err)
	}
}

func TestTraverse_LinearChain(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	a := createTraverseEntity(t, s, "A", nil)
	b := createTraverseEntity(t, s, "B", nil)
	c := createTraverseEntity(t, s, "C", nil)

	linkEntities(t, s, a, b, "knows", 0.9)
	linkEntities(t, s, b, c, "knows", 0.9)

	results, err := s.Traverse(ctx, a, &store.TraverseOpts{MaxDepth: 2})
	if err != nil {
		t.Fatalf("traverse: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// B should be at depth 1.
	if results[0].Entity.ID != b {
		t.Errorf("expected B at depth 1, got %s", results[0].Entity.ID)
	}
	if results[0].Depth != 1 {
		t.Errorf("expected depth 1, got %d", results[0].Depth)
	}
	if len(results[0].Path) != 1 {
		t.Errorf("expected 1 edge in path, got %d", len(results[0].Path))
	}

	// C should be at depth 2.
	if results[1].Entity.ID != c {
		t.Errorf("expected C at depth 2, got %s", results[1].Entity.ID)
	}
	if results[1].Depth != 2 {
		t.Errorf("expected depth 2, got %d", results[1].Depth)
	}
	if len(results[1].Path) != 2 {
		t.Errorf("expected 2 edges in path, got %d", len(results[1].Path))
	}
}

func TestTraverse_Bidirectional(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	a := createTraverseEntity(t, s, "A", nil)
	b := createTraverseEntity(t, s, "B", nil)
	c := createTraverseEntity(t, s, "C", nil)

	linkEntities(t, s, a, b, "knows", 0.9) // A→B
	linkEntities(t, s, c, a, "knows", 0.9) // C→A

	results, err := s.Traverse(ctx, a, &store.TraverseOpts{MaxDepth: 1})
	if err != nil {
		t.Fatalf("traverse: %v", err)
	}

	ids := make(map[string]bool)
	for _, r := range results {
		ids[r.Entity.ID] = true
	}
	if !ids[b] || !ids[c] {
		t.Errorf("expected B and C reachable from A, got IDs: %v", ids)
	}
}

func TestTraverse_OutboundOnly(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	a := createTraverseEntity(t, s, "A", nil)
	b := createTraverseEntity(t, s, "B", nil)
	c := createTraverseEntity(t, s, "C", nil)

	linkEntities(t, s, a, b, "knows", 0.9) // A→B
	linkEntities(t, s, c, a, "knows", 0.9) // C→A

	results, err := s.Traverse(ctx, a, &store.TraverseOpts{
		Direction: store.DirectionOutbound,
		MaxDepth:  1,
	})
	if err != nil {
		t.Fatalf("traverse: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result (B only), got %d", len(results))
	}
	if results[0].Entity.ID != b {
		t.Errorf("expected B, got %s", results[0].Entity.ID)
	}
}

func TestTraverse_InboundOnly(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	a := createTraverseEntity(t, s, "A", nil)
	b := createTraverseEntity(t, s, "B", nil)
	c := createTraverseEntity(t, s, "C", nil)

	linkEntities(t, s, a, b, "knows", 0.9) // A→B
	linkEntities(t, s, c, a, "knows", 0.9) // C→A

	results, err := s.Traverse(ctx, a, &store.TraverseOpts{
		Direction: store.DirectionInbound,
		MaxDepth:  1,
	})
	if err != nil {
		t.Fatalf("traverse: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result (C only), got %d", len(results))
	}
	if results[0].Entity.ID != c {
		t.Errorf("expected C, got %s", results[0].Entity.ID)
	}
}

func TestTraverse_CyclePrevention(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	a := createTraverseEntity(t, s, "A", nil)
	b := createTraverseEntity(t, s, "B", nil)
	c := createTraverseEntity(t, s, "C", nil)

	linkEntities(t, s, a, b, "knows", 0.9)
	linkEntities(t, s, b, c, "knows", 0.9)
	linkEntities(t, s, c, a, "knows", 0.9) // cycle back to A

	results, err := s.Traverse(ctx, a, &store.TraverseOpts{MaxDepth: 10})
	if err != nil {
		t.Fatalf("traverse: %v", err)
	}

	// Should find exactly B and C, no duplicates.
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	ids := make(map[string]bool)
	for _, r := range results {
		ids[r.Entity.ID] = true
	}
	if !ids[b] || !ids[c] {
		t.Errorf("expected B and C, got IDs: %v", ids)
	}
}

func TestTraverse_DepthLimit(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	a := createTraverseEntity(t, s, "A", nil)
	b := createTraverseEntity(t, s, "B", nil)
	c := createTraverseEntity(t, s, "C", nil)
	d := createTraverseEntity(t, s, "D", nil)

	linkEntities(t, s, a, b, "knows", 0.9)
	linkEntities(t, s, b, c, "knows", 0.9)
	linkEntities(t, s, c, d, "knows", 0.9)

	results, err := s.Traverse(ctx, a, &store.TraverseOpts{MaxDepth: 1})
	if err != nil {
		t.Fatalf("traverse: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result at depth 1, got %d", len(results))
	}
	if results[0].Entity.ID != b {
		t.Errorf("expected B, got %s", results[0].Entity.ID)
	}
}

func TestTraverse_RelationTypeFilter(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	a := createTraverseEntity(t, s, "A", nil)
	b := createTraverseEntity(t, s, "B", nil)
	c := createTraverseEntity(t, s, "C", nil)

	linkEntities(t, s, a, b, "knows", 0.9)
	linkEntities(t, s, a, c, "employed_by", 0.9)

	results, err := s.Traverse(ctx, a, &store.TraverseOpts{
		MaxDepth:      1,
		RelationTypes: []string{"knows"},
	})
	if err != nil {
		t.Fatalf("traverse: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Entity.ID != b {
		t.Errorf("expected B, got %s", results[0].Entity.ID)
	}
}

func TestTraverse_EntityTypeFilter(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	a := createTraverseEntity(t, s, "A", nil)
	b := createTraverseEntity(t, s, "B", nil)
	c := createTraverseEntity(t, s, "C", nil)

	linkEntities(t, s, a, b, "knows", 0.9)
	linkEntities(t, s, a, c, "knows", 0.9)

	// All entities are google.protobuf.Struct — filtering for that type returns all.
	results, err := s.Traverse(ctx, a, &store.TraverseOpts{
		MaxDepth:   1,
		EntityType: entityType,
	})
	if err != nil {
		t.Fatalf("traverse: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results for matching type, got %d", len(results))
	}

	// Filtering for a non-existent type returns nothing.
	results, err = s.Traverse(ctx, a, &store.TraverseOpts{
		MaxDepth:   1,
		EntityType: "nonexistent.Type",
	})
	if err != nil {
		t.Fatalf("traverse: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results for non-matching type, got %d", len(results))
	}
}

func TestTraverse_MinConfidence(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	a := createTraverseEntity(t, s, "A", nil)
	b := createTraverseEntity(t, s, "B", nil)
	c := createTraverseEntity(t, s, "C", nil)

	linkEntities(t, s, a, b, "knows", 0.9)
	linkEntities(t, s, a, c, "knows", 0.3)

	results, err := s.Traverse(ctx, a, &store.TraverseOpts{
		MaxDepth:      1,
		MinConfidence: 0.5,
	})
	if err != nil {
		t.Fatalf("traverse: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Entity.ID != b {
		t.Errorf("expected B (high confidence), got %s", results[0].Entity.ID)
	}
}

func TestTraverse_TagFilter(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	a := createTraverseEntity(t, s, "A", []string{"ws:acme"})
	b := createTraverseEntity(t, s, "B", []string{"ws:acme"})
	c := createTraverseEntity(t, s, "C", []string{"ws:other"})

	linkEntities(t, s, a, b, "knows", 0.9)
	linkEntities(t, s, a, c, "knows", 0.9)

	results, err := s.Traverse(ctx, a, &store.TraverseOpts{
		MaxDepth: 1,
		Filter:   &matching.QueryFilter{Tags: []string{"ws:acme"}},
	})
	if err != nil {
		t.Fatalf("traverse: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Entity.ID != b {
		t.Errorf("expected B (ws:acme), got %s", results[0].Entity.ID)
	}
}

func TestTraverse_MaxResults(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	a := createTraverseEntity(t, s, "A", nil)
	b := createTraverseEntity(t, s, "B", nil)
	c := createTraverseEntity(t, s, "C", nil)
	d := createTraverseEntity(t, s, "D", nil)

	linkEntities(t, s, a, b, "knows", 0.9)
	linkEntities(t, s, a, c, "knows", 0.9)
	linkEntities(t, s, a, d, "knows", 0.9)

	results, err := s.Traverse(ctx, a, &store.TraverseOpts{
		MaxDepth:   1,
		MaxResults: 2,
	})
	if err != nil {
		t.Fatalf("traverse: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results (capped), got %d", len(results))
	}
}

func TestTraverse_NoRelations(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	a := createTraverseEntity(t, s, "alone", nil)

	results, err := s.Traverse(ctx, a, nil)
	if err != nil {
		t.Fatalf("traverse: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

func TestTraverse_DefaultOpts(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	a := createTraverseEntity(t, s, "A", nil)
	b := createTraverseEntity(t, s, "B", nil)

	linkEntities(t, s, a, b, "knows", 0.9)

	// nil opts should use defaults.
	results, err := s.Traverse(ctx, a, nil)
	if err != nil {
		t.Fatalf("traverse: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
}

func TestTraverse_PathContentValidation(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	a := createTraverseEntity(t, s, "A", nil)
	b := createTraverseEntity(t, s, "B", nil)
	c := createTraverseEntity(t, s, "C", nil)

	linkEntities(t, s, a, b, "knows", 0.85)
	linkEntities(t, s, b, c, "employed_by", 0.72)

	results, err := s.Traverse(ctx, a, &store.TraverseOpts{MaxDepth: 2})
	if err != nil {
		t.Fatalf("traverse: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// Depth 1: A→B via "knows".
	r1 := results[0]
	if len(r1.Path) != 1 {
		t.Fatalf("expected 1 edge at depth 1, got %d", len(r1.Path))
	}
	edge1 := r1.Path[0]
	if edge1.RelationType != "knows" {
		t.Errorf("edge1 relation type: got %q, want %q", edge1.RelationType, "knows")
	}
	if edge1.FromID != a {
		t.Errorf("edge1 from: got %s, want %s", edge1.FromID, a)
	}
	if edge1.ToID != b {
		t.Errorf("edge1 to: got %s, want %s", edge1.ToID, b)
	}
	if edge1.Confidence != 0.85 {
		t.Errorf("edge1 confidence: got %f, want 0.85", edge1.Confidence)
	}

	// Depth 2: A→B→C, path should have both edges.
	r2 := results[1]
	if len(r2.Path) != 2 {
		t.Fatalf("expected 2 edges at depth 2, got %d", len(r2.Path))
	}
	// First edge in path is the same A→B.
	if r2.Path[0].RelationType != "knows" || r2.Path[0].FromID != a || r2.Path[0].ToID != b {
		t.Errorf("depth-2 path[0] mismatch: %+v", r2.Path[0])
	}
	// Second edge is B→C via "employed_by".
	if r2.Path[1].RelationType != "employed_by" {
		t.Errorf("depth-2 path[1] relation type: got %q, want %q", r2.Path[1].RelationType, "employed_by")
	}
	if r2.Path[1].FromID != b || r2.Path[1].ToID != c {
		t.Errorf("depth-2 path[1] from/to mismatch: %+v", r2.Path[1])
	}
	if r2.Path[1].Confidence != 0.72 {
		t.Errorf("depth-2 path[1] confidence: got %f, want 0.72", r2.Path[1].Confidence)
	}
}

func TestTraverse_EntityFieldIntegrity(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	a := createTraverseEntity(t, s, "A", nil)
	b := createTraverseEntity(t, s, "B", []string{"tag:test"})

	linkEntities(t, s, a, b, "knows", 0.9)

	results, err := s.Traverse(ctx, a, &store.TraverseOpts{MaxDepth: 1})
	if err != nil {
		t.Fatalf("traverse: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	got := results[0].Entity
	if got.ID != b {
		t.Errorf("ID: got %s, want %s", got.ID, b)
	}
	if got.EntityType != entityType {
		t.Errorf("EntityType: got %s, want %s", got.EntityType, entityType)
	}
	if got.Confidence != 0.9 {
		t.Errorf("Confidence: got %f, want 0.9", got.Confidence)
	}
	if len(got.Tags) != 1 || got.Tags[0] != "tag:test" {
		t.Errorf("Tags: got %v, want [tag:test]", got.Tags)
	}
	if len(got.Data) == 0 {
		t.Error("Data should not be empty")
	}
	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt should not be zero")
	}
	if got.UpdatedAt.IsZero() {
		t.Error("UpdatedAt should not be zero")
	}
}

func TestTraverse_InvalidUUID(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	_, err := s.Traverse(ctx, "not-a-uuid", nil)
	if err == nil {
		t.Fatal("expected error for invalid UUID")
	}
}

func TestTraverse_NonexistentStartEntity(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	results, err := s.Traverse(ctx, "00000000-0000-0000-0000-000000000000", nil)
	if err != nil {
		t.Fatalf("traverse: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results for nonexistent entity, got %d", len(results))
	}
}

func TestTraverse_WithinTransaction(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	a := createTraverseEntity(t, s, "A", nil)
	b := createTraverseEntity(t, s, "B", nil)
	linkEntities(t, s, a, b, "knows", 0.9)

	txStore, err := s.Tx(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer txStore.Rollback(ctx) //nolint:errcheck

	// Use Store.WithTx to traverse inside transaction.
	pool := s.Pool()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	txS := s.WithTx(tx)
	results, err := txS.Traverse(ctx, a, &store.TraverseOpts{MaxDepth: 1})
	if err != nil {
		t.Fatalf("traverse in tx: %v", err)
	}
	if len(results) != 1 || results[0].Entity.ID != b {
		t.Errorf("expected B in tx traverse, got %v", results)
	}
}

func TestTraverse_MixedRelationTypesAcrossHops(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	a := createTraverseEntity(t, s, "A", nil)
	b := createTraverseEntity(t, s, "B", nil)
	c := createTraverseEntity(t, s, "C", nil)

	linkEntities(t, s, a, b, "knows", 0.9)
	linkEntities(t, s, b, c, "employed_by", 0.9)

	// Only following "knows" should stop at B.
	results, err := s.Traverse(ctx, a, &store.TraverseOpts{
		MaxDepth:      2,
		RelationTypes: []string{"knows"},
	})
	if err != nil {
		t.Fatalf("traverse: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 (B only), got %d", len(results))
	}

	// Following both types should reach C.
	results, err = s.Traverse(ctx, a, &store.TraverseOpts{
		MaxDepth:      2,
		RelationTypes: []string{"knows", "employed_by"},
	})
	if err != nil {
		t.Fatalf("traverse: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 (B and C), got %d", len(results))
	}
}

func TestTraverse_TagFilterBlocksTransitiveReach(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	// A→B→C, but B has wrong tag — should block path to C.
	a := createTraverseEntity(t, s, "A", []string{"ws:acme"})
	b := createTraverseEntity(t, s, "B", []string{"ws:other"})
	c := createTraverseEntity(t, s, "C", []string{"ws:acme"})

	linkEntities(t, s, a, b, "knows", 0.9)
	linkEntities(t, s, b, c, "knows", 0.9)

	results, err := s.Traverse(ctx, a, &store.TraverseOpts{
		MaxDepth: 3,
		Filter:   &matching.QueryFilter{Tags: []string{"ws:acme"}},
	})
	if err != nil {
		t.Fatalf("traverse: %v", err)
	}

	// B is excluded by tag, and since traversal can't pass through B, C is unreachable.
	if len(results) != 0 {
		ids := make([]string, len(results))
		for i, r := range results {
			ids[i] = r.Entity.ID
		}
		t.Fatalf("expected 0 results (B blocks path to C), got %d: %v", len(results), ids)
	}
}

func TestTraverse_MinConfidenceBlocksTransitiveReach(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	// A→B (low confidence) → C (high confidence).
	a := createTraverseEntity(t, s, "A", nil)
	b := createTraverseEntity(t, s, "B", nil)
	c := createTraverseEntity(t, s, "C", nil)

	linkEntities(t, s, a, b, "knows", 0.3) // below threshold
	linkEntities(t, s, b, c, "knows", 0.9)

	results, err := s.Traverse(ctx, a, &store.TraverseOpts{
		MaxDepth:      3,
		MinConfidence: 0.5,
	})
	if err != nil {
		t.Fatalf("traverse: %v", err)
	}

	// A→B is below threshold, so B and C are both unreachable.
	if len(results) != 0 {
		t.Fatalf("expected 0 results (low-confidence edge blocks), got %d", len(results))
	}
}

func TestTraverse_ExcludeTagFilter(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	a := createTraverseEntity(t, s, "A", nil)
	b := createTraverseEntity(t, s, "B", []string{"archived"})
	c := createTraverseEntity(t, s, "C", nil)

	linkEntities(t, s, a, b, "knows", 0.9)
	linkEntities(t, s, a, c, "knows", 0.9)

	results, err := s.Traverse(ctx, a, &store.TraverseOpts{
		MaxDepth: 1,
		Filter:   &matching.QueryFilter{ExcludeTag: "archived"},
	})
	if err != nil {
		t.Fatalf("traverse: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Entity.ID != c {
		t.Errorf("expected C (non-archived), got %s", results[0].Entity.ID)
	}
}

func TestTraverse_ExcludeTagWithUnlessTags(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	a := createTraverseEntity(t, s, "A", nil)
	b := createTraverseEntity(t, s, "B", []string{"archived", "important"})
	c := createTraverseEntity(t, s, "C", []string{"archived"})

	linkEntities(t, s, a, b, "knows", 0.9)
	linkEntities(t, s, a, c, "knows", 0.9)

	results, err := s.Traverse(ctx, a, &store.TraverseOpts{
		MaxDepth: 1,
		Filter: &matching.QueryFilter{
			ExcludeTag: "archived",
			UnlessTags: []string{"important"},
		},
	})
	if err != nil {
		t.Fatalf("traverse: %v", err)
	}

	// B has "archived" but also "important" (exempted). C is excluded.
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Entity.ID != b {
		t.Errorf("expected B (exempt via important), got %s", results[0].Entity.ID)
	}
}

func TestTraverse_AnyTagsFilter(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	a := createTraverseEntity(t, s, "A", nil)
	b := createTraverseEntity(t, s, "B", []string{"team:alpha"})
	c := createTraverseEntity(t, s, "C", []string{"team:beta"})
	d := createTraverseEntity(t, s, "D", []string{"team:gamma"})

	linkEntities(t, s, a, b, "knows", 0.9)
	linkEntities(t, s, a, c, "knows", 0.9)
	linkEntities(t, s, a, d, "knows", 0.9)

	results, err := s.Traverse(ctx, a, &store.TraverseOpts{
		MaxDepth: 1,
		Filter:   &matching.QueryFilter{AnyTags: []string{"team:alpha", "team:beta"}},
	})
	if err != nil {
		t.Fatalf("traverse: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results (alpha + beta), got %d", len(results))
	}
	ids := make(map[string]bool)
	for _, r := range results {
		ids[r.Entity.ID] = true
	}
	if !ids[b] || !ids[c] {
		t.Errorf("expected B and C, got %v", ids)
	}
}

func TestTraverse_DiamondDeduplication(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	//     A
	//    / \
	//   B   C
	//    \ /
	//     D
	a := createTraverseEntity(t, s, "A", nil)
	b := createTraverseEntity(t, s, "B", nil)
	c := createTraverseEntity(t, s, "C", nil)
	d := createTraverseEntity(t, s, "D", nil)

	linkEntities(t, s, a, b, "knows", 0.9)
	linkEntities(t, s, a, c, "knows", 0.9)
	linkEntities(t, s, b, d, "knows", 0.9)
	linkEntities(t, s, c, d, "knows", 0.9)

	results, err := s.Traverse(ctx, a, &store.TraverseOpts{MaxDepth: 2})
	if err != nil {
		t.Fatalf("traverse: %v", err)
	}

	// Should get B, C (depth 1) and D (depth 2) — D only once.
	if len(results) != 3 {
		t.Fatalf("expected 3 results (B, C, D), got %d", len(results))
	}
	ids := make(map[string]int)
	for _, r := range results {
		ids[r.Entity.ID]++
	}
	if ids[d] != 1 {
		t.Errorf("D should appear exactly once, got %d", ids[d])
	}
}

func TestTraverse_ScopedExcludeTag(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	es, err := entitystore.New(entitystore.WithPgStore(s.Pool()))
	if err != nil {
		t.Fatalf("create entity store: %v", err)
	}

	a := createTraverseEntity(t, s, "A", nil)
	b := createTraverseEntity(t, s, "B", []string{"deleted"})
	c := createTraverseEntity(t, s, "C", []string{"deleted", "pinned"})
	d := createTraverseEntity(t, s, "D", nil)

	linkEntities(t, s, a, b, "knows", 0.9)
	linkEntities(t, s, a, c, "knows", 0.9)
	linkEntities(t, s, a, d, "knows", 0.9)

	scoped := es.Scoped(entitystore.ScopeConfig{
		ExcludeTag: "deleted",
		UnlessTags: []string{"pinned"},
	})

	results, err := scoped.Traverse(ctx, a, &store.TraverseOpts{MaxDepth: 1})
	if err != nil {
		t.Fatalf("scoped traverse: %v", err)
	}

	// B excluded (deleted, no exemption), C included (deleted but pinned), D included (no deleted tag).
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	ids := make(map[string]bool)
	for _, r := range results {
		ids[r.Entity.ID] = true
	}
	if !ids[c] || !ids[d] {
		t.Errorf("expected C and D, got %v", ids)
	}
	if ids[b] {
		t.Error("B should be excluded")
	}
}

func TestTraverse_MaxDepthClamping(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	a := createTraverseEntity(t, s, "A", nil)
	b := createTraverseEntity(t, s, "B", nil)
	linkEntities(t, s, a, b, "knows", 0.9)

	// MaxDepth > 10 should be clamped — should still work, not error.
	results, err := s.Traverse(ctx, a, &store.TraverseOpts{MaxDepth: 50})
	if err != nil {
		t.Fatalf("traverse: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
}

func TestTraverse_ScopedStore(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	es, err := entitystore.New(entitystore.WithPgStore(s.Pool()))
	if err != nil {
		t.Fatalf("create entity store: %v", err)
	}

	// Create entities with different tags via the inner store.
	a := createTraverseEntity(t, s, "A", []string{"ws:acme"})
	b := createTraverseEntity(t, s, "B", []string{"ws:acme"})
	c := createTraverseEntity(t, s, "C", []string{"ws:other"})

	linkEntities(t, s, a, b, "knows", 0.9)
	linkEntities(t, s, a, c, "knows", 0.9)

	scoped := es.Scoped(entitystore.ScopeConfig{
		RequireTags: []string{"ws:acme"},
	})

	results, err := scoped.Traverse(ctx, a, &store.TraverseOpts{MaxDepth: 1})
	if err != nil {
		t.Fatalf("scoped traverse: %v", err)
	}

	// Should only find B (ws:acme), not C (ws:other).
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Entity.ID != b {
		t.Errorf("expected B, got %s", results[0].Entity.ID)
	}
}
