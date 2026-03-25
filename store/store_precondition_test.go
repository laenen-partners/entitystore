package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/laenen-partners/entitystore/matching"
	"github.com/laenen-partners/entitystore/store"
)

// createEntityWithAnchorsAndTags is a helper that creates an entity with anchors and tags,
// returning its ID and cleaning up on test completion.
func createEntityWithAnchorsAndTags(t *testing.T, s *store.Store, anchors []matching.AnchorQuery, tags []string) string {
	t.Helper()
	ctx := context.Background()

	results, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action:     store.WriteActionCreate,
			Data:       testData(t, map[string]any{"name": "test entity"}),
			Confidence: 0.9,
			Anchors:    anchors,
			Tags:       tags,
		}},
	})
	if err != nil {
		t.Fatalf("create entity: %v", err)
	}
	id := results[0].Entity.ID
	t.Cleanup(func() { _ = s.DeleteEntity(ctx, id) })
	return id
}

func TestPreCondition_MustExist_Passes(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	createEntityWithAnchorsAndTags(t, s,
		[]matching.AnchorQuery{{Field: "ref", Value: "ruleset-1"}},
		nil,
	)

	// Write with precondition that the entity must exist — should succeed.
	results, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{
			WriteEntity: &store.WriteEntityOp{
				Action:     store.WriteActionCreate,
				Data:       testData(t, map[string]any{"product": "A"}),
				Confidence: 0.9,
			},
			PreConditions: []store.PreCondition{
				{
					EntityType: entityType,
					Anchors:    []matching.AnchorQuery{{Field: "ref", Value: "ruleset-1"}},
					MustExist:  true,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("batch write should succeed: %v", err)
	}
	t.Cleanup(func() { _ = s.DeleteEntity(ctx, results[0].Entity.ID) })
}

func TestPreCondition_MustExist_Fails(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	_, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{
			WriteEntity: &store.WriteEntityOp{
				Action:     store.WriteActionCreate,
				Data:       testData(t, map[string]any{"product": "A"}),
				Confidence: 0.9,
			},
			PreConditions: []store.PreCondition{
				{
					EntityType: entityType,
					Anchors:    []matching.AnchorQuery{{Field: "ref", Value: "nonexistent"}},
					MustExist:  true,
				},
			},
		},
	})

	var pcErr *store.PreConditionError
	if !errors.As(err, &pcErr) {
		t.Fatalf("expected PreConditionError, got %v", err)
	}
	if pcErr.Violation != "not_found" {
		t.Errorf("expected violation not_found, got %s", pcErr.Violation)
	}
	if pcErr.OpIndex != 0 {
		t.Errorf("expected op index 0, got %d", pcErr.OpIndex)
	}
}

func TestPreCondition_MustNotExist_Passes(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	results, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{
			WriteEntity: &store.WriteEntityOp{
				Action:     store.WriteActionCreate,
				Data:       testData(t, map[string]any{"product": "A"}),
				Confidence: 0.9,
			},
			PreConditions: []store.PreCondition{
				{
					EntityType:   entityType,
					Anchors:      []matching.AnchorQuery{{Field: "ref", Value: "unique-value"}},
					MustNotExist: true,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("batch write should succeed: %v", err)
	}
	t.Cleanup(func() { _ = s.DeleteEntity(ctx, results[0].Entity.ID) })
}

func TestPreCondition_MustNotExist_Fails(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	createEntityWithAnchorsAndTags(t, s,
		[]matching.AnchorQuery{{Field: "ref", Value: "taken-value"}},
		nil,
	)

	_, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{
			WriteEntity: &store.WriteEntityOp{
				Action:     store.WriteActionCreate,
				Data:       testData(t, map[string]any{"product": "A"}),
				Confidence: 0.9,
			},
			PreConditions: []store.PreCondition{
				{
					EntityType:   entityType,
					Anchors:      []matching.AnchorQuery{{Field: "ref", Value: "taken-value"}},
					MustNotExist: true,
				},
			},
		},
	})

	var pcErr *store.PreConditionError
	if !errors.As(err, &pcErr) {
		t.Fatalf("expected PreConditionError, got %v", err)
	}
	if pcErr.Violation != "already_exists" {
		t.Errorf("expected violation already_exists, got %s", pcErr.Violation)
	}
}

func TestPreCondition_TagRequired_Passes(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	createEntityWithAnchorsAndTags(t, s,
		[]matching.AnchorQuery{{Field: "ref", Value: "active-ruleset"}},
		[]string{"active"},
	)

	results, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{
			WriteEntity: &store.WriteEntityOp{
				Action:     store.WriteActionCreate,
				Data:       testData(t, map[string]any{"product": "B"}),
				Confidence: 0.9,
			},
			PreConditions: []store.PreCondition{
				{
					EntityType:  entityType,
					Anchors:     []matching.AnchorQuery{{Field: "ref", Value: "active-ruleset"}},
					MustExist:   true,
					TagRequired: "active",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("batch write should succeed: %v", err)
	}
	t.Cleanup(func() { _ = s.DeleteEntity(ctx, results[0].Entity.ID) })
}

func TestPreCondition_TagRequired_Fails(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	createEntityWithAnchorsAndTags(t, s,
		[]matching.AnchorQuery{{Field: "ref", Value: "inactive-ruleset"}},
		[]string{"inactive"},
	)

	_, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{
			WriteEntity: &store.WriteEntityOp{
				Action:     store.WriteActionCreate,
				Data:       testData(t, map[string]any{"product": "B"}),
				Confidence: 0.9,
			},
			PreConditions: []store.PreCondition{
				{
					EntityType:  entityType,
					Anchors:     []matching.AnchorQuery{{Field: "ref", Value: "inactive-ruleset"}},
					MustExist:   true,
					TagRequired: "active",
				},
			},
		},
	})

	var pcErr *store.PreConditionError
	if !errors.As(err, &pcErr) {
		t.Fatalf("expected PreConditionError, got %v", err)
	}
	if pcErr.Violation != "tag_required" {
		t.Errorf("expected violation tag_required, got %s", pcErr.Violation)
	}
}

func TestPreCondition_TagForbidden_Passes(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	createEntityWithAnchorsAndTags(t, s,
		[]matching.AnchorQuery{{Field: "ref", Value: "enabled-ruleset"}},
		[]string{"active"},
	)

	results, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{
			WriteEntity: &store.WriteEntityOp{
				Action:     store.WriteActionCreate,
				Data:       testData(t, map[string]any{"product": "C"}),
				Confidence: 0.9,
			},
			PreConditions: []store.PreCondition{
				{
					EntityType:   entityType,
					Anchors:      []matching.AnchorQuery{{Field: "ref", Value: "enabled-ruleset"}},
					MustExist:    true,
					TagForbidden: "disabled:true",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("batch write should succeed: %v", err)
	}
	t.Cleanup(func() { _ = s.DeleteEntity(ctx, results[0].Entity.ID) })
}

func TestPreCondition_TagForbidden_Fails(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	createEntityWithAnchorsAndTags(t, s,
		[]matching.AnchorQuery{{Field: "ref", Value: "disabled-ruleset"}},
		[]string{"disabled:true"},
	)

	_, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{
			WriteEntity: &store.WriteEntityOp{
				Action:     store.WriteActionCreate,
				Data:       testData(t, map[string]any{"product": "C"}),
				Confidence: 0.9,
			},
			PreConditions: []store.PreCondition{
				{
					EntityType:   entityType,
					Anchors:      []matching.AnchorQuery{{Field: "ref", Value: "disabled-ruleset"}},
					MustExist:    true,
					TagForbidden: "disabled:true",
				},
			},
		},
	})

	var pcErr *store.PreConditionError
	if !errors.As(err, &pcErr) {
		t.Fatalf("expected PreConditionError, got %v", err)
	}
	if pcErr.Violation != "tag_forbidden" {
		t.Errorf("expected violation tag_forbidden, got %s", pcErr.Violation)
	}
}

func TestPreCondition_MutuallyExclusive(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	_, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{
			WriteEntity: &store.WriteEntityOp{
				Action:     store.WriteActionCreate,
				Data:       testData(t, map[string]any{"product": "D"}),
				Confidence: 0.9,
			},
			PreConditions: []store.PreCondition{
				{
					EntityType:   entityType,
					Anchors:      []matching.AnchorQuery{{Field: "ref", Value: "x"}},
					MustExist:    true,
					MustNotExist: true,
				},
			},
		},
	})
	if err == nil {
		t.Fatal("expected error for mutually exclusive MustExist and MustNotExist")
	}
}

func TestPreCondition_BatchRollback(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	// Op 0 creates an entity, op 1 has a failing precondition.
	// The entire batch should roll back — op 0's entity should not exist.
	_, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{
			WriteEntity: &store.WriteEntityOp{
				Action:     store.WriteActionCreate,
				Data:       testData(t, map[string]any{"product": "rollback-test"}),
				Confidence: 0.9,
				Anchors:    []matching.AnchorQuery{{Field: "ref", Value: "rollback-product"}},
			},
		},
		{
			WriteEntity: &store.WriteEntityOp{
				Action:     store.WriteActionCreate,
				Data:       testData(t, map[string]any{"product": "E"}),
				Confidence: 0.9,
			},
			PreConditions: []store.PreCondition{
				{
					EntityType: entityType,
					Anchors:    []matching.AnchorQuery{{Field: "ref", Value: "ghost"}},
					MustExist:  true,
				},
			},
		},
	})

	var pcErr *store.PreConditionError
	if !errors.As(err, &pcErr) {
		t.Fatalf("expected PreConditionError, got %v", err)
	}
	if pcErr.OpIndex != 1 {
		t.Errorf("expected failure on op 1, got op %d", pcErr.OpIndex)
	}

	// Verify op 0's entity was rolled back.
	found, err := s.FindByAnchors(ctx, entityType, []matching.AnchorQuery{
		{Field: "ref", Value: "rollback-product"},
	}, nil)
	if err != nil {
		t.Fatalf("find by anchors: %v", err)
	}
	if len(found) != 0 {
		t.Errorf("expected 0 entities after rollback, got %d", len(found))
		for _, e := range found {
			_ = s.DeleteEntity(ctx, e.ID)
		}
	}
}

func TestPreCondition_NoPreconditions(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	// Ensure BatchWrite still works normally without preconditions.
	results, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action:     store.WriteActionCreate,
			Data:       testData(t, map[string]any{"product": "no-pc"}),
			Confidence: 0.9,
		}},
	})
	if err != nil {
		t.Fatalf("batch write should succeed: %v", err)
	}
	t.Cleanup(func() { _ = s.DeleteEntity(ctx, results[0].Entity.ID) })
}
