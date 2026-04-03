package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/laenen-partners/entitystore"
	"github.com/laenen-partners/entitystore/matching"
	"github.com/laenen-partners/entitystore/store"
)

// scopedTestStore creates an EntityStore wrapping the shared test store.
func scopedTestStore(t *testing.T) *entitystore.EntityStore {
	t.Helper()
	s := sharedTestStore(t)
	es, err := entitystore.New(entitystore.WithPgStore(s.Pool()))
	if err != nil {
		t.Fatalf("create entity store: %v", err)
	}
	return es
}

// createScopedEntity creates an entity with given tags and anchor, returning its ID.
func createScopedEntity(t *testing.T, es *entitystore.EntityStore, anchor string, tags []string) string {
	t.Helper()
	ctx := context.Background()

	results, err := es.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action:     store.WriteActionCreate,
			Data:       testData(t, map[string]any{"name": anchor}),
			Confidence: 0.9,
			Tags:       tags,
			Anchors:    []matching.AnchorQuery{{Field: "ref", Value: anchor}},
		}},
	})
	if err != nil {
		t.Fatalf("create entity: %v", err)
	}
	id := results[0].Entity.ID
	t.Cleanup(func() { _ = es.DeleteEntity(ctx, id) })
	return id
}

func TestScopedStore_RequireTags_Filters(t *testing.T) {
	es := scopedTestStore(t)
	ctx := context.Background()

	// Create two entities: one with workspace tag, one without.
	createScopedEntity(t, es, "scoped-ws-a", []string{"ws:acme"})
	createScopedEntity(t, es, "scoped-ws-b", []string{"ws:other"})

	scoped := es.Scoped(entitystore.ScopeConfig{
		RequireTags: []string{"ws:acme"},
	})

	found, err := scoped.FindByAnchors(ctx, entityType,
		[]matching.AnchorQuery{
			{Field: "ref", Value: "scoped-ws-a"},
			{Field: "ref", Value: "scoped-ws-b"},
		}, nil)
	if err != nil {
		t.Fatalf("find by anchors: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("expected 1 entity, got %d", len(found))
	}
}

func TestScopedStore_GetEntity_HidesOutOfScope(t *testing.T) {
	es := scopedTestStore(t)
	ctx := context.Background()

	id := createScopedEntity(t, es, "scoped-hidden", []string{"ws:other"})

	scoped := es.Scoped(entitystore.ScopeConfig{
		RequireTags: []string{"ws:acme"},
	})

	_, err := scoped.GetEntity(ctx, id)
	if !errors.Is(err, entitystore.ErrAccessDenied) {
		t.Fatalf("expected ErrAccessDenied, got %v", err)
	}
}

func TestScopedStore_GetEntity_AllowsInScope(t *testing.T) {
	es := scopedTestStore(t)
	ctx := context.Background()

	id := createScopedEntity(t, es, "scoped-visible", []string{"ws:acme"})

	scoped := es.Scoped(entitystore.ScopeConfig{
		RequireTags: []string{"ws:acme"},
	})

	ent, err := scoped.GetEntity(ctx, id)
	if err != nil {
		t.Fatalf("get entity: %v", err)
	}
	if ent.ID != id {
		t.Errorf("expected entity %s, got %s", id, ent.ID)
	}
}

func TestScopedStore_ExcludeTag(t *testing.T) {
	es := scopedTestStore(t)
	ctx := context.Background()

	createScopedEntity(t, es, "scoped-exc-normal", []string{"ws:acme"})
	createScopedEntity(t, es, "scoped-exc-restricted", []string{"ws:acme", "restricted"})

	scoped := es.Scoped(entitystore.ScopeConfig{
		RequireTags: []string{"ws:acme"},
		ExcludeTag:  "restricted",
	})

	found, err := scoped.FindByAnchors(ctx, entityType,
		[]matching.AnchorQuery{
			{Field: "ref", Value: "scoped-exc-normal"},
			{Field: "ref", Value: "scoped-exc-restricted"},
		}, nil)
	if err != nil {
		t.Fatalf("find by anchors: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("expected 1 entity (restricted excluded), got %d", len(found))
	}
}

func TestScopedStore_UnlessTags_Exempts(t *testing.T) {
	es := scopedTestStore(t)
	ctx := context.Background()

	createScopedEntity(t, es, "scoped-unless-exempt", []string{"ws:acme", "restricted", "admin"})

	scoped := es.Scoped(entitystore.ScopeConfig{
		RequireTags: []string{"ws:acme"},
		ExcludeTag:  "restricted",
		UnlessTags:  []string{"admin"},
	})

	found, err := scoped.FindByAnchors(ctx, entityType,
		[]matching.AnchorQuery{{Field: "ref", Value: "scoped-unless-exempt"}}, nil)
	if err != nil {
		t.Fatalf("find by anchors: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("expected 1 entity (exempted by unless tag), got %d", len(found))
	}
}

func TestScopedStore_AutoTags_OnCreate(t *testing.T) {
	es := scopedTestStore(t)
	ctx := context.Background()

	scoped := es.Scoped(entitystore.ScopeConfig{
		RequireTags: []string{"ws:acme"},
		AutoTags:    []string{"ws:acme", "source:scoped"},
	})

	results, err := scoped.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action:     store.WriteActionCreate,
			Data:       testData(t, map[string]any{"name": "auto-tagged"}),
			Confidence: 0.9,
			Tags:       []string{"extra"},
			Anchors:    []matching.AnchorQuery{{Field: "ref", Value: "scoped-auto-tag"}},
		}},
	})
	if err != nil {
		t.Fatalf("batch write: %v", err)
	}
	id := results[0].Entity.ID
	t.Cleanup(func() { _ = es.DeleteEntity(ctx, id) })

	// Verify entity has both explicit and auto tags.
	ent, err := es.GetEntity(ctx, id)
	if err != nil {
		t.Fatalf("get entity: %v", err)
	}

	for _, expected := range []string{"extra", "ws:acme", "source:scoped"} {
		found := false
		for _, tag := range ent.Tags {
			if tag == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected tag %q, got tags %v", expected, ent.Tags)
		}
	}
}

func TestScopedStore_AutoTags_NotOnUpdate(t *testing.T) {
	es := scopedTestStore(t)
	ctx := context.Background()

	id := createScopedEntity(t, es, "scoped-update-no-auto", []string{"ws:acme"})

	scoped := es.Scoped(entitystore.ScopeConfig{
		AutoTags: []string{"auto:should-not-appear"},
	})

	ent, err := es.GetEntity(ctx, id)
	if err != nil {
		t.Fatalf("get entity: %v", err)
	}
	_, err = scoped.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action:          store.WriteActionUpdate,
			MatchedEntityID: id,
			Version:         ent.Version,
			Data:            testData(t, map[string]any{"name": "updated"}),
			Confidence:      0.95,
		}},
	})
	if err != nil {
		t.Fatalf("batch write: %v", err)
	}

	ent, err = es.GetEntity(ctx, id)
	if err != nil {
		t.Fatalf("get entity: %v", err)
	}
	for _, tag := range ent.Tags {
		if tag == "auto:should-not-appear" {
			t.Error("auto tags should not be applied on update")
		}
	}
}

func TestScopedStore_DoesNotMutateCaller(t *testing.T) {
	es := scopedTestStore(t)
	ctx := context.Background()

	scoped := es.Scoped(entitystore.ScopeConfig{
		AutoTags: []string{"ws:acme"},
	})

	originalTags := []string{"explicit"}
	op := store.BatchWriteOp{
		WriteEntity: &store.WriteEntityOp{
			Action:     store.WriteActionCreate,
			Data:       testData(t, map[string]any{"name": "no-mutate"}),
			Confidence: 0.9,
			Tags:       originalTags,
		},
	}

	results, err := scoped.BatchWrite(ctx, []store.BatchWriteOp{op})
	if err != nil {
		t.Fatalf("batch write: %v", err)
	}
	t.Cleanup(func() { _ = es.DeleteEntity(ctx, results[0].Entity.ID) })

	// Original tags slice must not be modified.
	if len(originalTags) != 1 || originalTags[0] != "explicit" {
		t.Errorf("caller's tags slice was mutated: %v", originalTags)
	}
}

func TestScopedStore_GetEntitiesByType_Filtered(t *testing.T) {
	es := scopedTestStore(t)
	ctx := context.Background()

	createScopedEntity(t, es, "scoped-type-a", []string{"ws:acme"})
	createScopedEntity(t, es, "scoped-type-b", []string{"ws:other"})

	scoped := es.Scoped(entitystore.ScopeConfig{
		RequireTags: []string{"ws:acme"},
	})

	found, err := scoped.GetEntitiesByType(ctx, entityType, 100, nil, nil)
	if err != nil {
		t.Fatalf("get entities by type: %v", err)
	}

	for _, e := range found {
		visible := false
		for _, tag := range e.Tags {
			if tag == "ws:acme" {
				visible = true
				break
			}
		}
		if !visible {
			t.Errorf("entity %s missing required tag ws:acme, has %v", e.ID, e.Tags)
		}
	}
}
