// This file demonstrates the event store, named consumers, search, and
// display names — features added after the initial store examples.
package examples

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/laenen-partners/entitystore"
	"github.com/laenen-partners/entitystore/store"
)

// ---------------------------------------------------------------------------
// 1. Events — automatic lifecycle events
// ---------------------------------------------------------------------------

// AutomaticEventsExample shows that every write automatically emits a
// lifecycle event. No explicit event creation needed.
func AutomaticEventsExample(ctx context.Context, es *entitystore.EntityStore) {
	data, _ := structpb.NewStruct(map[string]any{
		"full_name": "Alice Dupont",
		"email":     "alice@dupont.be",
	})

	// Create an entity — automatically emits EntityCreated event.
	results, err := es.BatchWrite(ctx, []entitystore.BatchWriteOp{
		{WriteEntity: &entitystore.WriteEntityOp{
			Action:      entitystore.WriteActionCreate,
			Data:        data,
			Confidence:  0.95,
			DisplayName: "Alice Dupont",
			Tags:        []string{"ws:acme"},
		}},
	})
	if err != nil {
		log.Fatal(err)
	}
	entityID := results[0].Entity.ID

	// Query events for this entity — EntityCreated is already there.
	events, _ := es.GetEventsForEntity(ctx, entityID, nil)
	fmt.Printf("Events after create: %d\n", len(events))
	for _, evt := range events {
		fmt.Printf("  %s at %s\n", evt.EventType, evt.OccurredAt.Format(time.RFC3339))
	}
	// Output:
	//   Events after create: 1
	//   entitystore.events.EntityCreated at 2026-03-31T...

	// Update the entity — automatically emits EntityUpdated event.
	updatedData, _ := structpb.NewStruct(map[string]any{
		"full_name": "Alice M. Dupont",
		"email":     "alice@dupont.be",
		"job_title": "CEO",
	})
	es.BatchWrite(ctx, []entitystore.BatchWriteOp{
		{WriteEntity: &entitystore.WriteEntityOp{
			Action:          entitystore.WriteActionUpdate,
			MatchedEntityID: entityID,
			Data:            updatedData,
			Confidence:      0.98,
			DisplayName:     "Alice M. Dupont",
		}},
	})

	events, _ = es.GetEventsForEntity(ctx, entityID, nil)
	fmt.Printf("Events after update: %d\n", len(events))
	// Output: Events after update: 2
}

// ---------------------------------------------------------------------------
// 2. Custom domain events
// ---------------------------------------------------------------------------

// CustomEventsExample shows how to attach custom domain events to writes.
// These are stored alongside the automatic lifecycle events.
func CustomEventsExample(ctx context.Context, es *entitystore.EntityStore) {
	data, _ := structpb.NewStruct(map[string]any{
		"full_name": "Bob Martin",
		"email":     "bob@techcorp.io",
	})

	// Custom event as a structpb (in practice, use your own proto message).
	customEvent, _ := structpb.NewStruct(map[string]any{
		"source":    "linkedin",
		"recruiter": "carol@acme.com",
	})

	// Create with custom event attached.
	results, _ := es.BatchWrite(ctx, []entitystore.BatchWriteOp{
		{WriteEntity: &entitystore.WriteEntityOp{
			Action:      entitystore.WriteActionCreate,
			Data:        data,
			Confidence:  0.90,
			DisplayName: "Bob Martin",
			Events:      []proto.Message{customEvent}, // custom event
		}},
	})
	entityID := results[0].Entity.ID

	// Both lifecycle + custom events are stored.
	events, _ := es.GetEventsForEntity(ctx, entityID, nil)
	fmt.Printf("Events: %d\n", len(events))
	for _, evt := range events {
		fmt.Printf("  %s\n", evt.EventType)
	}
	// Output:
	//   Events: 2
	//   entitystore.events.EntityCreated
	//   google.protobuf.Struct

	// Or use WithEvents option with generated WriteOp:
	//   op := personv1.PersonWriteOp(person, store.WriteActionCreate,
	//       store.WithEvents(customEvent),
	//   )
}

// ---------------------------------------------------------------------------
// 3. Querying events with filters
// ---------------------------------------------------------------------------

// EventQueryExample shows how to filter events by type and time range.
func EventQueryExample(ctx context.Context, es *entitystore.EntityStore, entityID string) {
	// All events for an entity, newest first.
	events, _ := es.GetEventsForEntity(ctx, entityID, nil)
	fmt.Printf("All events: %d\n", len(events))

	// Filter by event type.
	events, _ = es.GetEventsForEntity(ctx, entityID, &entitystore.EventQueryOpts{
		EventTypes: []string{"entitystore.events.EntityCreated"},
	})
	fmt.Printf("Create events only: %d\n", len(events))

	// Filter by time range.
	events, _ = es.GetEventsForEntity(ctx, entityID, &entitystore.EventQueryOpts{
		Since: time.Now().Add(-1 * time.Hour),
		Limit: 10,
	})
	fmt.Printf("Last hour, max 10: %d\n", len(events))

	// All events across all entities (for the explorer UI).
	allEvents, _ := es.GetAllEvents(ctx, &entitystore.EventQueryOpts{Limit: 50}, nil)
	fmt.Printf("Global events: %d\n", len(allEvents))

	// Paginate with cursor.
	if len(allEvents) == 50 {
		lastTime := allEvents[len(allEvents)-1].OccurredAt
		nextPage, _ := es.GetAllEvents(ctx, &entitystore.EventQueryOpts{Limit: 50}, &lastTime)
		fmt.Printf("Next page: %d\n", len(nextPage))
	}
}

// ---------------------------------------------------------------------------
// 4. Named consumers
// ---------------------------------------------------------------------------

// ConsumerExample shows how to create named event consumers that
// independently process the event stream.
func ConsumerExample(ctx context.Context, es *entitystore.EntityStore) {
	// Realtime consumer — wakes instantly on new events via LISTEN/NOTIFY.
	// Use for lightweight, fire-and-forget work (e.g. SSE notifications).
	notifier := es.NewConsumer(
		func(ctx context.Context, events []store.Event) error {
			for _, evt := range events {
				fmt.Printf("notify: %s for entity %s\n", evt.EventType, evt.EntityID)
			}
			return nil // return error to retry the batch
		},
		entitystore.ConsumerConfig{
			Name:         "notifier",
			PollInterval: 5 * time.Second, // fallback if NOTIFY missed
			Logger:       slog.Default(),
		},
	)

	// Polling consumer — for heavyweight processing (embeddings, projections).
	projector := es.NewConsumer(
		func(ctx context.Context, events []store.Event) error {
			for _, evt := range events {
				fmt.Printf("project: %s\n", evt.EventType)
				// Compute embeddings, update projections, etc.
			}
			return nil
		},
		entitystore.ConsumerConfig{
			Name:         "projector",
			BatchSize:    50,
			PollInterval: 5 * time.Second,
		},
	)

	// Both run independently — each tracks its own cursor position.
	// In practice, run these in goroutines:
	//   go notifier.RunRealtime(ctx) // LISTEN/NOTIFY + polling
	//   go projector.Run(ctx)        // polling only
	_ = notifier
	_ = projector
}

// ---------------------------------------------------------------------------
// 5. Display names
// ---------------------------------------------------------------------------

// DisplayNameExample shows how display names make entities human-readable.
func DisplayNameExample(ctx context.Context, es *entitystore.EntityStore) {
	data, _ := structpb.NewStruct(map[string]any{
		"full_name": "Diana Janssen",
		"email":     "diana@acme.com",
	})

	// Set display name on create.
	es.BatchWrite(ctx, []entitystore.BatchWriteOp{
		{WriteEntity: &entitystore.WriteEntityOp{
			Action:      entitystore.WriteActionCreate,
			Data:        data,
			Confidence:  0.95,
			DisplayName: "Diana Janssen", // shows in explorer UI, search results, relations
			Tags:        []string{"type:person"},
		}},
	})

	// With generated code, display name is set automatically from proto annotations:
	//   message Person {
	//     option (entitystore.v1.message) = {
	//       display_fields: ["full_name", "email"]
	//     };
	//   }
	//   op := personv1.PersonWriteOp(person, store.WriteActionCreate)
	//   // op.DisplayName = "Diana Janssen" (auto-derived)

	// Or set via option:
	//   store.WithDisplayName("Diana Janssen")
}

// ---------------------------------------------------------------------------
// 6. Search
// ---------------------------------------------------------------------------

// SearchExample shows fuzzy search across entity display names.
func SearchExample(ctx context.Context, es *entitystore.EntityStore) {
	// Fuzzy trigram search on display_name. Falls back to token search.
	results, _ := es.Search(ctx, "diana", 20, nil)
	fmt.Printf("Search 'diana': %d results\n", len(results))
	for _, e := range results {
		fmt.Printf("  %s (%s)\n", e.DisplayName, e.EntityType)
	}

	// Partial matches work — "jan" finds "Diana Janssen".
	results, _ = es.Search(ctx, "jan", 20, nil)
	fmt.Printf("Search 'jan': %d results\n", len(results))

	// Case-insensitive.
	results, _ = es.Search(ctx, "DIANA", 20, nil)
	fmt.Printf("Search 'DIANA': %d results\n", len(results))

	// With tag filter (for scoped search).
	results, _ = es.Search(ctx, "diana", 20, &entitystore.QueryFilter{
		Tags: []string{"type:person"},
	})
	fmt.Printf("Search 'diana' (persons only): %d results\n", len(results))
}

// ---------------------------------------------------------------------------
// 7. Anchors
// ---------------------------------------------------------------------------

// AnchorsExample shows how to query the stored anchors for an entity.
func AnchorsExample(ctx context.Context, es *entitystore.EntityStore, entityID string) {
	anchors, _ := es.GetAnchorsForEntity(ctx, entityID)
	fmt.Printf("Anchors for %s:\n", entityID[:8])
	for _, a := range anchors {
		fmt.Printf("  %s = %s\n", a.Field, a.Value)
	}
	// Output:
	//   Anchors for a3f2e1b0:
	//     email = alice@dupont.be
}

// ---------------------------------------------------------------------------
// 8. Health check
// ---------------------------------------------------------------------------

// HealthExample shows how to check the store's health status.
func HealthExample(ctx context.Context, es *entitystore.EntityStore) {
	status, _ := es.Health(ctx)

	fmt.Printf("DB OK: %v (latency %v)\n", status.DB.OK, status.DB.Latency)
	fmt.Printf("Pool: %d/%d connections\n", status.DB.TotalConns, status.DB.MaxConns)
	fmt.Printf("Last event: %v\n", status.Events.LastEventAt)

	// Consumer health shows lag and lock status.
	for _, c := range status.Consumers {
		fmt.Printf("Consumer %s: lag %s, holder %s\n", c.Name, c.Lag, c.HolderID)
	}
}

// ---------------------------------------------------------------------------
// 9. Explorer UI
// ---------------------------------------------------------------------------

// ExplorerExample shows how to embed the explorer in any service.
func ExplorerExample(ctx context.Context, es *entitystore.EntityStore) {
	// The explorer is a separate package — import it where needed:
	//
	//   import "github.com/laenen-partners/entitystore/ui/explorer"
	//
	//   // Standalone server (blocks):
	//   explorer.Run(explorer.Config{Store: es, Port: 3336})
	//
	//   // Background (non-blocking):
	//   explorer.RunInBackground(ctx, explorer.Config{Store: es})
	//
	//   // Mount on existing router:
	//   explorer.Mount(r, es)
	//
	// Opens at http://localhost:3336 with:
	// - Search (fuzzy trigram, debounced)
	// - Entity detail (drawer with JSON viewer, anchors, relations)
	// - Stats (entity/relation type counts)
	// - Events (paginated, filterable, clickable payloads)

	_ = es // explorer import not shown to avoid dependency in examples package
}
