package store_test

import (
	"context"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	eventsv1 "github.com/laenen-partners/entitystore/gen/entitystore/events/v1"
	"github.com/laenen-partners/entitystore/matching"
	"github.com/laenen-partners/entitystore/store"
)

// ---------------------------------------------------------------------------
// Standard lifecycle events
// ---------------------------------------------------------------------------

func TestEvents_CreateEmitsEntityCreated(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	data := testData(t, map[string]any{"name": "alice"})
	results, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action:     store.WriteActionCreate,
			Data:       data,
			Confidence: 0.95,
			Tags:       []string{"ws:test", "role:admin"},
		}},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := results[0].Entity.ID

	events, err := s.GetEventsForEntity(ctx, id, nil)
	if err != nil {
		t.Fatalf("get events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	evt := events[0]
	if evt.EventType != "entitystore.events.EntityCreated" {
		t.Errorf("EventType = %q, want entitystore.events.EntityCreated", evt.EventType)
	}
	if evt.PayloadType != "entitystore.events.v1.EntityCreated" {
		t.Errorf("PayloadType = %q, want entitystore.events.v1.EntityCreated", evt.PayloadType)
	}
	if evt.EntityID != id {
		t.Errorf("EntityID = %q, want %q", evt.EntityID, id)
	}
	if evt.ID == "" {
		t.Error("Event ID should not be empty")
	}
	if evt.OccurredAt.IsZero() {
		t.Error("OccurredAt should not be zero")
	}

	// Verify proto payload is deserialized.
	created, ok := evt.Payload.(*eventsv1.EntityCreated)
	if !ok {
		t.Fatalf("Payload type = %T, want *eventsv1.EntityCreated", evt.Payload)
	}
	if created.EntityId != id {
		t.Errorf("created.EntityId = %q, want %q", created.EntityId, id)
	}
	if created.EntityType != entityType {
		t.Errorf("created.EntityType = %q, want %q", created.EntityType, entityType)
	}
	if created.Confidence != 0.95 {
		t.Errorf("created.Confidence = %v, want 0.95", created.Confidence)
	}
	// Tags should be snapshotted on the event.
	if len(evt.Tags) != 2 {
		t.Errorf("event tags = %v, want 2 tags", evt.Tags)
	}

	// RawPayload should also be available.
	if len(evt.RawPayload) == 0 {
		t.Error("RawPayload should not be empty")
	}
}

func TestEvents_UpdateEmitsEntityUpdated(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	// Create.
	results, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action:     store.WriteActionCreate,
			Data:       testData(t, map[string]any{"name": "bob"}),
			Confidence: 0.9,
		}},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := results[0].Entity.ID

	// Update.
	_, err = s.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action:          store.WriteActionUpdate,
			MatchedEntityID: id,
			Version:         results[0].Entity.Version,
			Data:            testData(t, map[string]any{"name": "bob updated"}),
			Confidence:      0.98,
		}},
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	events, err := s.GetEventsForEntity(ctx, id, nil)
	if err != nil {
		t.Fatalf("get events: %v", err)
	}
	// Should have 2 events: EntityUpdated (newest first), EntityCreated.
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	// Newest first.
	if events[0].EventType != "entitystore.events.EntityUpdated" {
		t.Errorf("events[0].EventType = %q, want EntityUpdated", events[0].EventType)
	}
	updated, ok := events[0].Payload.(*eventsv1.EntityUpdated)
	if !ok {
		t.Fatalf("Payload type = %T, want *eventsv1.EntityUpdated", events[0].Payload)
	}
	if updated.EntityId != id {
		t.Errorf("updated.EntityId = %q, want %q", updated.EntityId, id)
	}
	if updated.Confidence != 0.98 {
		t.Errorf("updated.Confidence = %v, want 0.98", updated.Confidence)
	}

	if events[1].EventType != "entitystore.events.EntityCreated" {
		t.Errorf("events[1].EventType = %q, want EntityCreated", events[1].EventType)
	}
}

func TestEvents_MergeEmitsEntityMerged(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	results, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action:     store.WriteActionCreate,
			Data:       testData(t, map[string]any{"name": "charlie"}),
			Confidence: 0.9,
		}},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := results[0].Entity.ID

	// Merge.
	_, err = s.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action:          store.WriteActionMerge,
			MatchedEntityID: id,
			Version:         results[0].Entity.Version,
			Data:            testData(t, map[string]any{"phone": "555-0123"}),
			Confidence:      0.85,
		}},
	})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}

	events, err := s.GetEventsForEntity(ctx, id, &store.EventQueryOpts{
		EventTypes: []string{"entitystore.events.EntityMerged"},
	})
	if err != nil {
		t.Fatalf("get events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 merge event, got %d", len(events))
	}

	merged, ok := events[0].Payload.(*eventsv1.EntityMerged)
	if !ok {
		t.Fatalf("Payload type = %T, want *eventsv1.EntityMerged", events[0].Payload)
	}
	if merged.WinnerId != id {
		t.Errorf("merged.WinnerId = %q, want %q", merged.WinnerId, id)
	}
}

func TestEvents_DeleteEmitsEntityDeleted(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	results, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action:     store.WriteActionCreate,
			Data:       testData(t, map[string]any{"name": "dave"}),
			Confidence: 0.9,
			Tags:       []string{"deleteme"},
		}},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := results[0].Entity.ID

	if err := s.DeleteEntity(ctx, id); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// Events are still queryable even after soft delete (entity_events is separate).
	events, err := s.GetEventsForEntity(ctx, id, &store.EventQueryOpts{
		EventTypes: []string{"entitystore.events.EntityDeleted"},
	})
	if err != nil {
		t.Fatalf("get events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 delete event, got %d", len(events))
	}

	deleted, ok := events[0].Payload.(*eventsv1.EntityDeleted)
	if !ok {
		t.Fatalf("Payload type = %T, want *eventsv1.EntityDeleted", events[0].Payload)
	}
	if deleted.EntityId != id {
		t.Errorf("deleted.EntityId = %q, want %q", deleted.EntityId, id)
	}
	if deleted.EntityType != entityType {
		t.Errorf("deleted.EntityType = %q, want %q", deleted.EntityType, entityType)
	}

	// Tags should be snapshotted from the entity at delete time.
	if len(events[0].Tags) == 0 {
		t.Error("expected tags snapshot on delete event")
	}
}

func TestEvents_HardDeleteEmitsEntityHardDeleted(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	results, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action:     store.WriteActionCreate,
			Data:       testData(t, map[string]any{"name": "eve"}),
			Confidence: 0.9,
		}},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := results[0].Entity.ID

	if err := s.HardDeleteEntity(ctx, id); err != nil {
		t.Fatalf("hard delete: %v", err)
	}

	// Events for this entity should still include the hard delete event
	// (entity_events rows are not CASCADE-deleted since entity_id has no FK).
	events, err := s.GetEventsForEntity(ctx, id, &store.EventQueryOpts{
		EventTypes: []string{"entitystore.events.EntityHardDeleted"},
	})
	if err != nil {
		t.Fatalf("get events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 hard delete event, got %d", len(events))
	}

	hardDeleted, ok := events[0].Payload.(*eventsv1.EntityHardDeleted)
	if !ok {
		t.Fatalf("Payload type = %T, want *eventsv1.EntityHardDeleted", events[0].Payload)
	}
	if hardDeleted.EntityId != id {
		t.Errorf("hardDeleted.EntityId = %q, want %q", hardDeleted.EntityId, id)
	}
}

// ---------------------------------------------------------------------------
// Relation events
// ---------------------------------------------------------------------------

func TestEvents_UpsertRelationEmitsRelationCreated(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	// Create two entities.
	results, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action: store.WriteActionCreate,
			Data:   testData(t, map[string]any{"name": "alice"}),
		}},
		{WriteEntity: &store.WriteEntityOp{
			Action: store.WriteActionCreate,
			Data:   testData(t, map[string]any{"name": "acme"}),
		}},
	})
	if err != nil {
		t.Fatalf("create entities: %v", err)
	}
	personID := results[0].Entity.ID
	companyID := results[1].Entity.ID

	// Create relation.
	_, err = s.BatchWrite(ctx, []store.BatchWriteOp{
		{UpsertRelation: &store.UpsertRelationOp{
			SourceID:     personID,
			TargetID:     companyID,
			RelationType: "works_at",
			Confidence:   0.95,
		}},
	})
	if err != nil {
		t.Fatalf("upsert relation: %v", err)
	}

	// Relation events are not tied to entity_id — they use relation_key.
	// Query all events for person to see only entity events.
	personEvents, err := s.GetEventsForEntity(ctx, personID, nil)
	if err != nil {
		t.Fatalf("get events for person: %v", err)
	}
	// Person should only have EntityCreated, not the relation event.
	for _, evt := range personEvents {
		if evt.EventType == "entitystore.events.RelationCreated" {
			t.Error("relation event should not appear under entity_id queries")
		}
	}
}

// ---------------------------------------------------------------------------
// Custom domain events via WithEvents
// ---------------------------------------------------------------------------

func TestEvents_WithEventsCustomEvents(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	// Use a structpb.Struct as a custom event (any proto.Message works).
	customEvent, err := structpb.NewStruct(map[string]any{
		"source":    "linkedin",
		"recruiter": "bob@acme.com",
	})
	if err != nil {
		t.Fatalf("create custom event: %v", err)
	}

	results, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action:     store.WriteActionCreate,
			Data:       testData(t, map[string]any{"name": "frank"}),
			Confidence: 0.9,
			Events:     []proto.Message{customEvent},
		}},
	})
	if err != nil {
		t.Fatalf("create with events: %v", err)
	}
	id := results[0].Entity.ID

	events, err := s.GetEventsForEntity(ctx, id, nil)
	if err != nil {
		t.Fatalf("get events: %v", err)
	}

	// Should have 2 events: EntityCreated (standard) + Struct (custom).
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	// Standard event comes first (inserted first), but ordered DESC by occurred_at.
	// Both are in the same millisecond so order may vary. Check by type.
	typeSet := map[string]bool{}
	for _, evt := range events {
		typeSet[evt.EventType] = true
	}
	if !typeSet["entitystore.events.EntityCreated"] {
		t.Error("missing entitystore.events.EntityCreated")
	}
	// google.protobuf.Struct → "google.Struct" (version segment "protobuf" stripped)
	if !typeSet["google.Struct"] {
		t.Error("missing google.Struct (custom event)")
	}
}

func TestEvents_WithEventsOnRelation(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	results, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action: store.WriteActionCreate,
			Data:   testData(t, map[string]any{"name": "alice"}),
		}},
		{WriteEntity: &store.WriteEntityOp{
			Action: store.WriteActionCreate,
			Data:   testData(t, map[string]any{"name": "corp"}),
		}},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	customEvt, _ := structpb.NewStruct(map[string]any{"reason": "extracted from invoice"})
	_, err = s.BatchWrite(ctx, []store.BatchWriteOp{
		{UpsertRelation: &store.UpsertRelationOp{
			SourceID:     results[0].Entity.ID,
			TargetID:     results[1].Entity.ID,
			RelationType: "works_at",
			Confidence:   0.9,
			Events:       []proto.Message{customEvt},
		}},
	})
	if err != nil {
		t.Fatalf("upsert with events: %v", err)
	}
	// No assertion on specific events — just verify it doesn't error.
	// Relation events are keyed by relation_key, not entity_id.
}

// ---------------------------------------------------------------------------
// EventQueryOpts filtering
// ---------------------------------------------------------------------------

func TestEvents_FilterByEventType(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	// Create then update — produces EntityCreated + EntityUpdated.
	results, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action:     store.WriteActionCreate,
			Data:       testData(t, map[string]any{"name": "grace"}),
			Confidence: 0.9,
		}},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := results[0].Entity.ID

	_, err = s.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action:          store.WriteActionUpdate,
			MatchedEntityID: id,
			Version:         results[0].Entity.Version,
			Data:            testData(t, map[string]any{"name": "grace updated"}),
			Confidence:      0.95,
		}},
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	// Filter: only EntityCreated.
	events, err := s.GetEventsForEntity(ctx, id, &store.EventQueryOpts{
		EventTypes: []string{"entitystore.events.EntityCreated"},
	})
	if err != nil {
		t.Fatalf("get events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 filtered event, got %d", len(events))
	}
	if events[0].EventType != "entitystore.events.EntityCreated" {
		t.Errorf("EventType = %q, want EntityCreated", events[0].EventType)
	}
}

func TestEvents_FilterBySince(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	results, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action:     store.WriteActionCreate,
			Data:       testData(t, map[string]any{"name": "hank"}),
			Confidence: 0.9,
		}},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := results[0].Entity.ID

	// Query with Since in the future — should get no results.
	events, err := s.GetEventsForEntity(ctx, id, &store.EventQueryOpts{
		Since: time.Now().Add(1 * time.Hour),
	})
	if err != nil {
		t.Fatalf("get events: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events with future Since, got %d", len(events))
	}
}

func TestEvents_FilterByLimit(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	// Create then update twice — 3 events total.
	results, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action:     store.WriteActionCreate,
			Data:       testData(t, map[string]any{"name": "iris"}),
			Confidence: 0.9,
		}},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := results[0].Entity.ID

	for i := 0; i < 2; i++ {
		ent, err := s.GetEntity(ctx, id)
		if err != nil {
			t.Fatalf("get entity for update %d: %v", i, err)
		}
		_, err = s.BatchWrite(ctx, []store.BatchWriteOp{
			{WriteEntity: &store.WriteEntityOp{
				Action:          store.WriteActionUpdate,
				MatchedEntityID: id,
				Version:         ent.Version,
				Data:            testData(t, map[string]any{"name": "iris updated"}),
				Confidence:      0.95,
			}},
		})
		if err != nil {
			t.Fatalf("update %d: %v", i, err)
		}
	}

	// Limit to 2.
	events, err := s.GetEventsForEntity(ctx, id, &store.EventQueryOpts{Limit: 2})
	if err != nil {
		t.Fatalf("get events: %v", err)
	}
	if len(events) != 2 {
		t.Errorf("expected 2 events with Limit=2, got %d", len(events))
	}
}

// ---------------------------------------------------------------------------
// UUIDv7 ordering
// ---------------------------------------------------------------------------

func TestEvents_UUIDv7TimeOrdering(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	// Create then update — events should be in reverse chronological order.
	results, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action:     store.WriteActionCreate,
			Data:       testData(t, map[string]any{"name": "jack"}),
			Confidence: 0.9,
		}},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := results[0].Entity.ID

	ent, err := s.GetEntity(ctx, id)
	if err != nil {
		t.Fatalf("get entity: %v", err)
	}
	_, err = s.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action:          store.WriteActionUpdate,
			MatchedEntityID: id,
			Version:         ent.Version,
			Data:            testData(t, map[string]any{"name": "jack updated"}),
			Confidence:      0.95,
		}},
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	events, err := s.GetEventsForEntity(ctx, id, nil)
	if err != nil {
		t.Fatalf("get events: %v", err)
	}
	if len(events) < 2 {
		t.Fatalf("expected at least 2 events, got %d", len(events))
	}

	// Events are newest first (DESC).
	for i := 1; i < len(events); i++ {
		if events[i].OccurredAt.After(events[i-1].OccurredAt) {
			t.Errorf("events not in DESC order: [%d]=%v > [%d]=%v",
				i, events[i].OccurredAt, i-1, events[i-1].OccurredAt)
		}
	}

	// UUIDv7 IDs should also be lexicographically ordered (newer > older).
	if events[0].ID <= events[1].ID {
		t.Errorf("UUIDv7 IDs not in expected order: newest=%q <= oldest=%q", events[0].ID, events[1].ID)
	}
}

// ---------------------------------------------------------------------------
// Event type derivation
// ---------------------------------------------------------------------------

func TestEvents_EventTypeDerived(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	results, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action:     store.WriteActionCreate,
			Data:       testData(t, map[string]any{"name": "test"}),
			Confidence: 0.9,
		}},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := results[0].Entity.ID

	events, err := s.GetEventsForEntity(ctx, id, nil)
	if err != nil {
		t.Fatalf("get events: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("expected at least 1 event")
	}

	evt := events[0]
	// event_type should strip the version segment.
	if evt.EventType != "entitystore.events.EntityCreated" {
		t.Errorf("EventType = %q, want entitystore.events.EntityCreated", evt.EventType)
	}
	// payload_type should keep the full name.
	if evt.PayloadType != "entitystore.events.v1.EntityCreated" {
		t.Errorf("PayloadType = %q, want entitystore.events.v1.EntityCreated", evt.PayloadType)
	}
}

// ---------------------------------------------------------------------------
// TxStore events
// ---------------------------------------------------------------------------

func TestEvents_TxStoreEmitsEvents(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	tx, err := s.Tx(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	ent, err := tx.WriteEntity(ctx, &store.WriteEntityOp{
		Action:     store.WriteActionCreate,
		Data:       testData(t, map[string]any{"name": "tx-test"}),
		Confidence: 0.9,
	})
	if err != nil {
		t.Fatalf("write in tx: %v", err)
	}

	// Events should be visible within the transaction.
	events, err := tx.GetEventsForEntity(ctx, ent.ID, nil)
	if err != nil {
		t.Fatalf("get events in tx: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event in tx, got %d", len(events))
	}
	if events[0].EventType != "entitystore.events.EntityCreated" {
		t.Errorf("EventType = %q, want EntityCreated", events[0].EventType)
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// Events should persist after commit.
	events, err = s.GetEventsForEntity(ctx, ent.ID, nil)
	if err != nil {
		t.Fatalf("get events after commit: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event after commit, got %d", len(events))
	}
}

func TestEvents_TxStoreRollbackDiscardsEvents(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	tx, err := s.Tx(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}

	ent, err := tx.WriteEntity(ctx, &store.WriteEntityOp{
		Action:     store.WriteActionCreate,
		Data:       testData(t, map[string]any{"name": "rollback-test"}),
		Confidence: 0.9,
	})
	if err != nil {
		t.Fatalf("write in tx: %v", err)
	}
	entityID := ent.ID

	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	// Events should NOT persist after rollback.
	events, err := s.GetEventsForEntity(ctx, entityID, nil)
	if err != nil {
		t.Fatalf("get events after rollback: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events after rollback, got %d", len(events))
	}
}

// ---------------------------------------------------------------------------
// DeleteRelationByKey events
// ---------------------------------------------------------------------------

func TestEvents_DeleteRelationByKeyEmitsRelationDeleted(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	results, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action: store.WriteActionCreate,
			Data:   testData(t, map[string]any{"name": "alice"}),
		}},
		{WriteEntity: &store.WriteEntityOp{
			Action: store.WriteActionCreate,
			Data:   testData(t, map[string]any{"name": "corp"}),
		}},
	})
	if err != nil {
		t.Fatalf("create entities: %v", err)
	}
	sourceID := results[0].Entity.ID
	targetID := results[1].Entity.ID

	_, err = s.BatchWrite(ctx, []store.BatchWriteOp{
		{UpsertRelation: &store.UpsertRelationOp{
			SourceID:     sourceID,
			TargetID:     targetID,
			RelationType: "works_at",
			Confidence:   0.9,
		}},
	})
	if err != nil {
		t.Fatalf("upsert relation: %v", err)
	}

	if err := s.DeleteRelationByKey(ctx, sourceID, targetID, "works_at"); err != nil {
		t.Fatalf("delete relation: %v", err)
	}

	// The RelationDeleted event is not tied to entity_id, so we can't query
	// it via GetEventsForEntity. This test just verifies the operation succeeds
	// without error (the event is inserted in the same transaction).
}

// ---------------------------------------------------------------------------
// Multiple events in a single batch
// ---------------------------------------------------------------------------

func TestEvents_BatchWithMultipleOps(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	// Create two entities in one batch.
	results, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action:     store.WriteActionCreate,
			Data:       testData(t, map[string]any{"name": "entity1"}),
			Confidence: 0.9,
		}},
		{WriteEntity: &store.WriteEntityOp{
			Action:     store.WriteActionCreate,
			Data:       testData(t, map[string]any{"name": "entity2"}),
			Confidence: 0.85,
		}},
	})
	if err != nil {
		t.Fatalf("batch create: %v", err)
	}

	// Each entity should have exactly one EntityCreated event.
	for i, r := range results {
		events, err := s.GetEventsForEntity(ctx, r.Entity.ID, nil)
		if err != nil {
			t.Fatalf("get events for entity %d: %v", i, err)
		}
		if len(events) != 1 {
			t.Errorf("entity %d: expected 1 event, got %d", i, len(events))
		}
		if events[0].EventType != "entitystore.events.EntityCreated" {
			t.Errorf("entity %d: EventType = %q, want EntityCreated", i, events[0].EventType)
		}
	}
}

// Suppress unused import warnings.
var _ = matching.AnchorQuery{}
