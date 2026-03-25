package store_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/laenen-partners/entitystore/matching"
	"github.com/laenen-partners/entitystore/store"
)

// benchStore returns a shared store for benchmarks (container started once).
func benchStore(b *testing.B) *store.Store {
	b.Helper()
	// Use the shared test infrastructure (starts container on first call).
	t := &testing.T{}
	s := sharedTestStore(t)
	return s
}

func BenchmarkBatchWrite_SingleCreate(b *testing.B) {
	s := benchStore(b)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		results, err := s.BatchWrite(ctx, []store.BatchWriteOp{
			{WriteEntity: &store.WriteEntityOp{
				Action:     store.WriteActionCreate,
				Data:       testData(&testing.T{}, map[string]any{"name": fmt.Sprintf("bench-%d", i)}),
				Confidence: 0.9,
				Anchors:    []matching.AnchorQuery{{Field: "ref", Value: fmt.Sprintf("bench-%d", i)}},
				}},
		})
		if err != nil {
			b.Fatal(err)
		}
		// Cleanup inline to avoid accumulation.
		_ = s.DeleteEntity(ctx, results[0].Entity.ID)
	}
}

func BenchmarkBatchWrite_TenOps(b *testing.B) {
	s := benchStore(b)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ops := make([]store.BatchWriteOp, 10)
		for j := range ops {
			ops[j] = store.BatchWriteOp{WriteEntity: &store.WriteEntityOp{
				Action:     store.WriteActionCreate,
				Data:       testData(&testing.T{}, map[string]any{"name": fmt.Sprintf("bench-%d-%d", i, j)}),
				Confidence: 0.9,
				}}
		}
		results, err := s.BatchWrite(ctx, ops)
		if err != nil {
			b.Fatal(err)
		}
		for _, r := range results {
			_ = s.DeleteEntity(ctx, r.Entity.ID)
		}
	}
}

func BenchmarkFindByAnchors(b *testing.B) {
	s := benchStore(b)
	ctx := context.Background()

	// Setup: create an entity with an anchor.
	results, _ := s.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action:     store.WriteActionCreate,
			Data:       testData(&testing.T{}, map[string]any{"email": "bench@example.com"}),
			Confidence: 0.9,
			Anchors:    []matching.AnchorQuery{{Field: "email", Value: "bench@example.com"}},
		}},
	})
	id := results[0].Entity.ID
	defer func() { _ = s.DeleteEntity(ctx, id) }()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := s.FindByAnchors(ctx, entityType, []matching.AnchorQuery{
			{Field: "email", Value: "bench@example.com"},
		}, nil)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGetEntity(b *testing.B) {
	s := benchStore(b)
	ctx := context.Background()

	results, _ := s.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action:     store.WriteActionCreate,
			Data:       testData(&testing.T{}, map[string]any{"name": "bench-get"}),
			Confidence: 0.9,
		}},
	})
	id := results[0].Entity.ID
	defer func() { _ = s.DeleteEntity(ctx, id) }()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := s.GetEntity(ctx, id)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTraverse_Depth2(b *testing.B) {
	s := benchStore(b)
	ctx := context.Background()

	// Build a small graph: A→B→C.
	create := func(name string) string {
		results, _ := s.BatchWrite(ctx, []store.BatchWriteOp{
			{WriteEntity: &store.WriteEntityOp{
				Action: store.WriteActionCreate,
				Data:   testData(&testing.T{}, map[string]any{"name": name}),
				}},
		})
		return results[0].Entity.ID
	}
	link := func(src, tgt string) {
		s.BatchWrite(ctx, []store.BatchWriteOp{
			{UpsertRelation: &store.UpsertRelationOp{
				SourceID: src, TargetID: tgt,
				RelationType: "knows", Confidence: 0.9,
			}},
		})
	}

	a := create("A")
	b2 := create("B")
	c := create("C")
	link(a, b2)
	link(b2, c)
	defer func() {
		_ = s.DeleteEntity(ctx, a)
		_ = s.DeleteEntity(ctx, b2)
		_ = s.DeleteEntity(ctx, c)
	}()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := s.Traverse(ctx, a, &store.TraverseOpts{MaxDepth: 2})
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkConnectedEntities(b *testing.B) {
	s := benchStore(b)
	ctx := context.Background()

	// Create a hub entity with 10 connections.
	results, _ := s.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action: store.WriteActionCreate,
			Data:   testData(&testing.T{}, map[string]any{"name": "hub"}),
		}},
	})
	hubID := results[0].Entity.ID
	var ids []string
	ids = append(ids, hubID)

	for j := 0; j < 10; j++ {
		r, _ := s.BatchWrite(ctx, []store.BatchWriteOp{
			{WriteEntity: &store.WriteEntityOp{
				Action: store.WriteActionCreate,
				Data:   testData(&testing.T{}, map[string]any{"name": fmt.Sprintf("spoke-%d", j)}),
				}},
		})
		spokeID := r[0].Entity.ID
		ids = append(ids, spokeID)
		s.BatchWrite(ctx, []store.BatchWriteOp{
			{UpsertRelation: &store.UpsertRelationOp{
				SourceID: hubID, TargetID: spokeID,
				RelationType: "knows", Confidence: 0.9,
			}},
		})
	}
	defer func() {
		for _, id := range ids {
			_ = s.DeleteEntity(ctx, id)
		}
	}()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := s.ConnectedEntities(ctx, hubID)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGetRelationsFromEntity(b *testing.B) {
	s := benchStore(b)
	ctx := context.Background()

	results, _ := s.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action: store.WriteActionCreate,
			Data:   testData(&testing.T{}, map[string]any{"name": "src"}),
		}},
		{WriteEntity: &store.WriteEntityOp{
			Action: store.WriteActionCreate,
			Data:   testData(&testing.T{}, map[string]any{"name": "tgt"}),
		}},
	})
	srcID := results[0].Entity.ID
	tgtID := results[1].Entity.ID
	s.BatchWrite(ctx, []store.BatchWriteOp{
		{UpsertRelation: &store.UpsertRelationOp{
			SourceID: srcID, TargetID: tgtID,
			RelationType: "knows", Confidence: 0.9,
		}},
	})
	defer func() {
		_ = s.DeleteEntity(ctx, srcID)
		_ = s.DeleteEntity(ctx, tgtID)
	}()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := s.GetRelationsFromEntity(ctx, srcID, 0, nil)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStats(b *testing.B) {
	s := benchStore(b)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := s.Stats(ctx)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFindByTokens(b *testing.B) {
	s := benchStore(b)
	ctx := context.Background()

	results, _ := s.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action:     store.WriteActionCreate,
			Data:       testData(&testing.T{}, map[string]any{"name": "Alice Johnson"}),
			Confidence: 0.9,
			Tokens:     map[string][]string{"name": {"alice", "johnson"}},
		}},
	})
	id := results[0].Entity.ID
	defer func() { _ = s.DeleteEntity(ctx, id) }()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := s.FindByTokens(ctx, entityType, []string{"alice", "smith"}, 10, nil)
		if err != nil {
			b.Fatal(err)
		}
	}
}
