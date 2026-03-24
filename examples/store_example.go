// This file demonstrates entity store CRUD operations, relationship management,
// tagging, embedding search, and transaction usage.
//
// NOTE: These examples require a running PostgreSQL 16+ database with pgvector.
// In production, use testcontainers or a real database. The function signatures
// show the API usage patterns — they are not meant to be run standalone.
package examples

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/laenen-partners/entitystore"
	"github.com/laenen-partners/entitystore/matching"
)

// ---------------------------------------------------------------------------
// 1. Setup — creating the entity store
// ---------------------------------------------------------------------------

// SetupEntityStore shows how to create an EntityStore with PostgreSQL backend.
func SetupEntityStore(ctx context.Context, connString string) (*entitystore.EntityStore, error) {
	// Create connection pool.
	pool, err := pgxpool.New(ctx, connString)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	// Apply migrations (creates tables, indexes, pgvector extension).
	if err := entitystore.Migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	// Create entity store.
	es, err := entitystore.New(entitystore.WithPgStore(pool))
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("create store: %w", err)
	}

	return es, nil
}

// ---------------------------------------------------------------------------
// 2. Creating entities with BatchWrite
// ---------------------------------------------------------------------------

// CreateEntityExample shows how to create a new entity with anchors,
// tokens, tags, and provenance tracking.
func CreateEntityExample(ctx context.Context, es *entitystore.EntityStore) {
	data, _ := structpb.NewStruct(map[string]any{
		"email":         "alice@example.com",
		"full_name":     "Alice Johnson",
		"phone":         "+1-555-123-4567",
		"date_of_birth": "1992-03-20",
		"job_title":     "Product Manager",
	})

	results, err := es.BatchWrite(ctx, []entitystore.BatchWriteOp{
		{WriteEntity: &entitystore.WriteEntityOp{
			Action:     entitystore.WriteActionCreate,
			Data:       data,
			Confidence: 0.95,
			Tags:       []string{"source:crm", "team:engineering"},
			Anchors: []entitystore.AnchorQuery{
				{Field: "email", Value: "alice@example.com"},
			},
			Tokens: map[string][]string{
				"full_name": {"alice", "johnson"},
				"job_title": {"product", "manager"},
			},
			Provenance: entitystore.ProvenanceEntry{
				SourceURN:   "crm:contacts/alice-001",
				ExtractedAt: time.Now(),
				ModelID:     "gpt-4o",
				Confidence:  0.95,
				Fields:      []string{"email", "full_name", "phone", "date_of_birth", "job_title"},
				MatchMethod: "create",
			},
		}},
	})
	if err != nil {
		log.Fatal(err)
	}

	entity := results[0].Entity
	fmt.Printf("Created entity: %s (type: %s)\n", entity.ID, entity.EntityType)
}

// CreateWithClientIDExample shows how to create an entity with a
// client-generated UUID (useful for idempotent writes).
func CreateWithClientIDExample(ctx context.Context, es *entitystore.EntityStore) {
	data, _ := structpb.NewStruct(map[string]any{
		"invoice_number": "INV-2024-001",
		"issuer_name":    "Acme Corp",
		"total_amount":   1250.00,
		"invoice_date":   "2024-03-15",
		"currency":       "EUR",
	})

	results, err := es.BatchWrite(ctx, []entitystore.BatchWriteOp{
		{WriteEntity: &entitystore.WriteEntityOp{
			Action:     entitystore.WriteActionCreate,
			ID:         "550e8400-e29b-41d4-a716-446655440000", // client-generated UUID
			Data:       data,
			Confidence: 0.90,
			Anchors: []entitystore.AnchorQuery{
				{Field: "invoice_number", Value: "inv-2024-001"}, // normalized
			},
			Provenance: entitystore.ProvenanceEntry{
				SourceURN:   "email:inbox/msg-42",
				ExtractedAt: time.Now(),
				ModelID:     "claude-sonnet-4-20250514",
				Confidence:  0.90,
				Fields:      []string{"invoice_number", "issuer_name", "total_amount"},
				MatchMethod: "create",
			},
		}},
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Created invoice: %s\n", results[0].Entity.ID)
}

// ---------------------------------------------------------------------------
// 3. Updating and merging entities
// ---------------------------------------------------------------------------

// UpdateEntityExample shows how to fully replace an entity's data.
func UpdateEntityExample(ctx context.Context, es *entitystore.EntityStore, entityID string) {
	updatedData, _ := structpb.NewStruct(map[string]any{
		"email":         "alice.johnson@newcompany.com",
		"full_name":     "Alice M. Johnson",
		"phone":         "+1-555-987-6543",
		"date_of_birth": "1992-03-20",
		"job_title":     "VP of Product",
	})

	_, err := es.BatchWrite(ctx, []entitystore.BatchWriteOp{
		{WriteEntity: &entitystore.WriteEntityOp{
			Action:          entitystore.WriteActionUpdate,
			MatchedEntityID: entityID,
			Data:            updatedData,
			Confidence:      0.98,
			Anchors: []entitystore.AnchorQuery{
				{Field: "email", Value: "alice.johnson@newcompany.com"},
			},
			Provenance: entitystore.ProvenanceEntry{
				SourceURN:       "linkedin:profile/alice-johnson",
				ExtractedAt:     time.Now(),
				ModelID:         "gpt-4o",
				Confidence:      0.98,
				Fields:          []string{"email", "full_name", "phone", "job_title"},
				MatchMethod:     "anchor",
				MatchConfidence: 1.0,
			},
		}},
	})
	if err != nil {
		log.Fatal(err)
	}
}

// MergeEntityExample shows how to merge new data into an existing entity
// using JSON merge semantics (existing fields not in the new data are kept).
func MergeEntityExample(ctx context.Context, es *entitystore.EntityStore, entityID string) {
	// Only the fields present in this data will be updated; others are preserved.
	partialData, _ := structpb.NewStruct(map[string]any{
		"job_title": "Chief Product Officer",
		"phone":     "+1-555-000-1111",
	})

	_, err := es.BatchWrite(ctx, []entitystore.BatchWriteOp{
		{WriteEntity: &entitystore.WriteEntityOp{
			Action:          entitystore.WriteActionMerge,
			MatchedEntityID: entityID,
			Data:            partialData,
			Confidence:      0.92,
			Provenance: entitystore.ProvenanceEntry{
				SourceURN:       "email:inbox/msg-99",
				ExtractedAt:     time.Now(),
				ModelID:         "claude-sonnet-4-20250514",
				Confidence:      0.92,
				Fields:          []string{"job_title", "phone"},
				MatchMethod:     "composite",
				MatchConfidence: 0.87,
			},
		}},
	})
	if err != nil {
		log.Fatal(err)
	}
}

// ---------------------------------------------------------------------------
// 4. Finding entities — anchors, tokens, embeddings
// ---------------------------------------------------------------------------

// FindByAnchorsExample shows O(1) dedup lookup using anchor values.
func FindByAnchorsExample(ctx context.Context, es *entitystore.EntityStore) {
	// Find by single anchor.
	matches, err := es.FindByAnchors(ctx, "examples.v1.Person",
		[]entitystore.AnchorQuery{
			{Field: "email", Value: "alice@example.com"},
		},
		nil, // no filter
	)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Found %d entities by email anchor\n", len(matches))

	// Find with tag filter.
	matches, err = es.FindByAnchors(ctx, "examples.v1.Person",
		[]entitystore.AnchorQuery{
			{Field: "email", Value: "alice@example.com"},
		},
		&entitystore.QueryFilter{Tags: []string{"source:crm"}},
	)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Found %d entities by anchor + tag filter\n", len(matches))
}

// FindByTokensExample shows fuzzy candidate retrieval using token overlap.
func FindByTokensExample(ctx context.Context, es *entitystore.EntityStore) {
	// Tokenize the search query.
	tokens := matching.Tokenize("Alice Johnson Engineering")

	matches, err := es.FindByTokens(ctx, "examples.v1.Person",
		tokens,
		10,  // limit
		nil, // no filter
	)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Found %d candidates by token overlap\n", len(matches))
}

// FindByEmbeddingExample shows semantic vector similarity search.
func FindByEmbeddingExample(ctx context.Context, es *entitystore.EntityStore) {
	// In practice, you'd compute the embedding vector using an embedder:
	//   vec, _ := embedder(ctx, "Alice Johnson product manager")
	vec := make([]float32, 1536) // placeholder

	// Search within a single entity type.
	matches, err := es.FindByEmbedding(ctx, "examples.v1.Person",
		vec,
		5,   // top-K
		nil, // no filter
	)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Found %d entities by embedding similarity\n", len(matches))

	// Cross-type embedding search (pass empty entity type).
	matches, err = es.FindByEmbedding(ctx, "",
		vec,
		10,
		&entitystore.QueryFilter{
			EntityTypes: []string{"examples.v1.Person", "examples.v1.JobPosting"},
		},
	)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Found %d entities across types by embedding\n", len(matches))
}

// ---------------------------------------------------------------------------
// 5. Relationships
// ---------------------------------------------------------------------------

// RelationsExample shows how to create and query entity relationships.
func RelationsExample(ctx context.Context, es *entitystore.EntityStore, personID, companyID string) {
	// Create a relation in a batch alongside entity writes.
	relData, _ := structpb.NewStruct(map[string]any{
		"role":       "VP of Product",
		"start_date": "2023-01-15",
	})

	_, err := es.BatchWrite(ctx, []entitystore.BatchWriteOp{
		{UpsertRelation: &entitystore.UpsertRelationOp{
			SourceID:     personID,
			TargetID:     companyID,
			RelationType: "works_at",
			Confidence:   0.95,
			Evidence:     "Extracted from LinkedIn profile",
			SourceURN:    "linkedin:profile/alice-johnson",
			Data:         relData,
		}},
	})
	if err != nil {
		log.Fatal(err)
	}

	// Query outbound relations.
	outbound, err := es.GetRelationsFromEntity(ctx, personID, 0, nil)
	if err != nil {
		log.Fatal(err)
	}
	for _, rel := range outbound {
		fmt.Printf("  %s -[%s]-> %s (confidence: %.2f)\n",
			rel.SourceID, rel.RelationType, rel.TargetID, rel.Confidence)
	}

	// Find all entities connected to a person.
	connected, err := es.ConnectedEntities(ctx, personID)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Connected entities: %d\n", len(connected))

	// Find connected entities filtered by type and relation.
	companies, err := es.FindConnectedByType(ctx, personID,
		&entitystore.FindConnectedOpts{
			EntityType:    "examples.v1.Company",
			RelationTypes: []string{"works_at"},
		},
	)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Companies person works at: %d\n", len(companies))
}

// ---------------------------------------------------------------------------
// 6. Tags
// ---------------------------------------------------------------------------

// TagsExample shows tag management on entities.
func TagsExample(ctx context.Context, es *entitystore.EntityStore, entityID string) {
	// Set tags (replaces all existing tags).
	_ = es.SetTags(ctx, entityID, []string{"source:crm", "status:active"})

	// Add additional tags.
	_ = es.AddTags(ctx, entityID, []string{"team:engineering", "priority:high"})

	// Remove a single tag.
	_ = es.RemoveTag(ctx, entityID, "priority:high")
}

// ---------------------------------------------------------------------------
// 7. Transactions
// ---------------------------------------------------------------------------

// TransactionExample shows atomic multi-step operations using transactions.
func TransactionExample(ctx context.Context, es *entitystore.EntityStore) {
	tx, err := es.Tx(ctx)
	if err != nil {
		log.Fatal(err)
	}
	// Always rollback on error — commit overrides if reached.
	defer tx.Rollback(ctx) //nolint:errcheck

	// Write entity within transaction.
	personData, _ := structpb.NewStruct(map[string]any{
		"email":     "bob@example.com",
		"full_name": "Bob Smith",
	})
	person, err := tx.WriteEntity(ctx, &entitystore.WriteEntityOp{
		Action:     entitystore.WriteActionCreate,
		Data:       personData,
		Confidence: 0.90,
		Anchors:    []entitystore.AnchorQuery{{Field: "email", Value: "bob@example.com"}},
		Provenance: entitystore.ProvenanceEntry{
			SourceURN: "test:tx-example", ExtractedAt: time.Now(),
			ModelID: "manual", MatchMethod: "create",
		},
	})
	if err != nil {
		log.Fatal(err)
	}

	// Upsert relation using the entity ID from the same transaction.
	_, err = tx.UpsertRelation(ctx, &entitystore.UpsertRelationOp{
		SourceID:     person.ID,
		TargetID:     "some-company-id",
		RelationType: "works_at",
		Confidence:   0.85,
	})
	if err != nil {
		log.Fatal(err)
	}

	// Commit atomically.
	if err := tx.Commit(ctx); err != nil {
		log.Fatal(err)
	}
}

// ---------------------------------------------------------------------------
// 8. Pagination
// ---------------------------------------------------------------------------

// PaginationExample shows cursor-based pagination over entities.
func PaginationExample(ctx context.Context, es *entitystore.EntityStore) {
	var cursor *time.Time
	pageSize := int32(50)

	for {
		entities, err := es.GetEntitiesByType(ctx, "examples.v1.Person", pageSize, cursor, nil)
		if err != nil {
			log.Fatal(err)
		}
		if len(entities) == 0 {
			break
		}

		for _, ent := range entities {
			fmt.Printf("  %s: %s\n", ent.ID, ent.EntityType)
		}

		// Use the last entity's UpdatedAt as cursor for the next page.
		last := entities[len(entities)-1].UpdatedAt
		cursor = &last
	}
}

// ---------------------------------------------------------------------------
// 9. Provenance
// ---------------------------------------------------------------------------

// ProvenanceExample shows how to query the extraction audit trail.
func ProvenanceExample(ctx context.Context, es *entitystore.EntityStore, entityID string) {
	entries, err := es.GetProvenanceForEntity(ctx, entityID)
	if err != nil {
		log.Fatal(err)
	}

	for _, p := range entries {
		fmt.Printf("  Source: %s, Model: %s, Confidence: %.2f, Method: %s\n",
			p.SourceURN, p.ModelID, p.Confidence, p.MatchMethod)
		fmt.Printf("  Fields: %v, Extracted at: %s\n",
			p.Fields, p.ExtractedAt.Format(time.RFC3339))
	}
}

// ---------------------------------------------------------------------------
// 10. Mixed batch — entities and relations in one transaction
// ---------------------------------------------------------------------------

// MixedBatchExample shows how to create multiple entities and their
// relationships in a single atomic batch operation.
func MixedBatchExample(ctx context.Context, es *entitystore.EntityStore) {
	personData, _ := structpb.NewStruct(map[string]any{
		"email": "carol@startup.io", "full_name": "Carol Chen",
	})
	companyData, _ := structpb.NewStruct(map[string]any{
		"name": "Startup Inc", "domain": "startup.io",
	})

	results, err := es.BatchWrite(ctx, []entitystore.BatchWriteOp{
		// Op 0: create person
		{WriteEntity: &entitystore.WriteEntityOp{
			Action:     entitystore.WriteActionCreate,
			Data:       personData,
			Confidence: 0.93,
			Anchors:    []entitystore.AnchorQuery{{Field: "email", Value: "carol@startup.io"}},
			Provenance: entitystore.ProvenanceEntry{
				SourceURN: "email:inbox/msg-100", ExtractedAt: time.Now(),
				ModelID: "gpt-4o", MatchMethod: "create",
			},
		}},
		// Op 1: create company
		{WriteEntity: &entitystore.WriteEntityOp{
			Action:     entitystore.WriteActionCreate,
			Data:       companyData,
			Confidence: 0.88,
			Anchors:    []entitystore.AnchorQuery{{Field: "domain", Value: "startup.io"}},
			Provenance: entitystore.ProvenanceEntry{
				SourceURN: "email:inbox/msg-100", ExtractedAt: time.Now(),
				ModelID: "gpt-4o", MatchMethod: "create",
			},
		}},
	})
	if err != nil {
		log.Fatal(err)
	}

	personID := results[0].Entity.ID
	companyID := results[1].Entity.ID

	// Now create the relation in a second batch (needs entity IDs from above).
	_, err = es.BatchWrite(ctx, []entitystore.BatchWriteOp{
		{UpsertRelation: &entitystore.UpsertRelationOp{
			SourceID:     personID,
			TargetID:     companyID,
			RelationType: "works_at",
			Confidence:   0.93,
			Evidence:     "Email domain matches company domain",
			SourceURN:    "email:inbox/msg-100",
		}},
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Created person %s, company %s, and works_at relation\n", personID, companyID)
}

// ---------------------------------------------------------------------------
// 11. Preconditions — transactionally safe guards on BatchWrite
// ---------------------------------------------------------------------------

// PreConditionReferentialIntegrityExample shows how to ensure a referenced
// entity exists before applying a write — checked atomically inside the
// BatchWrite transaction with no TOCTOU gap.
func PreConditionReferentialIntegrityExample(ctx context.Context, es *entitystore.EntityStore, productID, rulesetID string) {
	updatedProduct, _ := structpb.NewStruct(map[string]any{
		"name":       "Widget Pro",
		"ruleset_id": rulesetID,
	})

	_, err := es.BatchWrite(ctx, []entitystore.BatchWriteOp{
		{
			WriteEntity: &entitystore.WriteEntityOp{
				Action:          entitystore.WriteActionUpdate,
				MatchedEntityID: productID,
				Data:            updatedProduct,
				Confidence:      0.95,
			},
			PreConditions: []entitystore.PreCondition{
				{
					EntityType:   "rulesets.v1.Ruleset",
					Anchors:      []entitystore.AnchorQuery{{Field: "ruleset_id", Value: rulesetID}},
					MustExist:    true,
					TagForbidden: "disabled:true", // also reject disabled rulesets
				},
			},
		},
	})

	var pcErr *entitystore.PreConditionError
	if errors.As(err, &pcErr) {
		fmt.Printf("Precondition failed: %s (op %d, entity type %s)\n",
			pcErr.Violation, pcErr.OpIndex, pcErr.Condition.EntityType)
		return
	}
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Product updated with ruleset link")
}

// PreConditionUniquenessExample shows how to prevent duplicate entity creation
// by asserting that no entity with the same anchor already exists.
func PreConditionUniquenessExample(ctx context.Context, es *entitystore.EntityStore) {
	invoiceData, _ := structpb.NewStruct(map[string]any{
		"invoice_number": "INV-2024-042",
		"issuer_name":    "Acme Corp",
		"total_amount":   3500.00,
	})

	results, err := es.BatchWrite(ctx, []entitystore.BatchWriteOp{
		{
			WriteEntity: &entitystore.WriteEntityOp{
				Action:     entitystore.WriteActionCreate,
				Data:       invoiceData,
				Confidence: 0.90,
				Anchors:    []entitystore.AnchorQuery{{Field: "invoice_number", Value: "inv-2024-042"}},
			},
			PreConditions: []entitystore.PreCondition{
				{
					EntityType:   "google.protobuf.Struct",
					Anchors:      []entitystore.AnchorQuery{{Field: "invoice_number", Value: "inv-2024-042"}},
					MustNotExist: true,
				},
			},
		},
	})

	var pcErr *entitystore.PreConditionError
	if errors.As(err, &pcErr) {
		fmt.Printf("Invoice already exists: %s\n", pcErr.Violation)
		return
	}
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Created invoice: %s\n", results[0].Entity.ID)
}

// PreConditionStatusGuardExample shows how to use tag-based preconditions
// to enforce status checks — e.g., only allow writes when a related entity
// carries (or does not carry) a specific tag.
func PreConditionStatusGuardExample(ctx context.Context, es *entitystore.EntityStore, orderID, warehouseID string) {
	shipmentData, _ := structpb.NewStruct(map[string]any{
		"order_id":     orderID,
		"warehouse_id": warehouseID,
		"status":       "pending",
	})

	_, err := es.BatchWrite(ctx, []entitystore.BatchWriteOp{
		{
			WriteEntity: &entitystore.WriteEntityOp{
				Action:     entitystore.WriteActionCreate,
				Data:       shipmentData,
				Confidence: 0.95,
			},
			PreConditions: []entitystore.PreCondition{
				// Order must exist and be confirmed.
				{
					EntityType:  "orders.v1.Order",
					Anchors:     []entitystore.AnchorQuery{{Field: "order_id", Value: orderID}},
					MustExist:   true,
					TagRequired: "confirmed",
				},
				// Warehouse must exist and not be suspended.
				{
					EntityType:   "warehouses.v1.Warehouse",
					Anchors:      []entitystore.AnchorQuery{{Field: "warehouse_id", Value: warehouseID}},
					MustExist:    true,
					TagForbidden: "suspended",
				},
			},
		},
	})

	var pcErr *entitystore.PreConditionError
	if errors.As(err, &pcErr) {
		fmt.Printf("Cannot create shipment: %s for %s\n",
			pcErr.Violation, pcErr.Condition.EntityType)
		return
	}
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Shipment created")
}

// ---------------------------------------------------------------------------
// 12. Scoped stores — multi-tenant tag-based filtering
// ---------------------------------------------------------------------------

// ScopedStoreWorkspaceExample shows how to create a workspace-scoped store
// that automatically filters reads and tags creates.
func ScopedStoreWorkspaceExample(ctx context.Context, es *entitystore.EntityStore, workspaceID string) {
	// Create a scoped store for a specific workspace.
	scoped := es.Scoped(entitystore.ScopeConfig{
		RequireTags: []string{"ws:" + workspaceID},
		ExcludeTag:  "restricted",
		UnlessTags:  []string{"admin"},
		AutoTags:    []string{"ws:" + workspaceID},
	})

	// Creates are auto-tagged — the entity will have "ws:<workspaceID>" tag.
	personData, _ := structpb.NewStruct(map[string]any{
		"email": "alice@acme.com", "full_name": "Alice Johnson",
	})
	results, err := scoped.BatchWrite(ctx, []entitystore.BatchWriteOp{
		{WriteEntity: &entitystore.WriteEntityOp{
			Action:     entitystore.WriteActionCreate,
			Data:       personData,
			Confidence: 0.95,
			Anchors:    []entitystore.AnchorQuery{{Field: "email", Value: "alice@acme.com"}},
		}},
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Created entity %s (auto-tagged with ws:%s)\n", results[0].Entity.ID, workspaceID)

	// Reads only return entities visible to this workspace.
	entities, err := scoped.FindByAnchors(ctx, "google.protobuf.Struct",
		[]entitystore.AnchorQuery{{Field: "email", Value: "alice@acme.com"}}, nil)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Found %d entities in workspace %s\n", len(entities), workspaceID)

	// GetEntity returns ErrAccessDenied for out-of-scope entities.
	_, err = scoped.GetEntity(ctx, "some-other-workspace-entity-id")
	if errors.Is(err, entitystore.ErrAccessDenied) {
		fmt.Println("Entity not visible in this scope")
	}
}

// ScopedStoreTenantExample shows simple tenant-level isolation.
func ScopedStoreTenantExample(ctx context.Context, es *entitystore.EntityStore, tenantID string) {
	scoped := es.Scoped(entitystore.ScopeConfig{
		RequireTags: []string{"tenant:" + tenantID},
		AutoTags:    []string{"tenant:" + tenantID},
	})

	// All reads and creates are now scoped to this tenant.
	invoiceData, _ := structpb.NewStruct(map[string]any{
		"invoice_number": "INV-001",
		"total_amount":   5000.00,
	})
	results, err := scoped.BatchWrite(ctx, []entitystore.BatchWriteOp{
		{WriteEntity: &entitystore.WriteEntityOp{
			Action:     entitystore.WriteActionCreate,
			Data:       invoiceData,
			Confidence: 0.9,
			Anchors:    []entitystore.AnchorQuery{{Field: "invoice_number", Value: "inv-001"}},
		}},
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Created tenant-scoped invoice: %s\n", results[0].Entity.ID)
}

// ScopedStoreWithTxExample shows scoped stores working with transactions.
func ScopedStoreWithTxExample(ctx context.Context, es *entitystore.EntityStore, pool interface{ Begin(ctx context.Context) (interface{ Rollback(ctx context.Context) error; Commit(ctx context.Context) error }, error) }) {
	scoped := es.Scoped(entitystore.ScopeConfig{
		RequireTags: []string{"ws:acme"},
		AutoTags:    []string{"ws:acme"},
	})

	// Scope config is preserved across transactions.
	_ = scoped // WithTx preserves the scope config:
	// txScoped := scoped.WithTx(tx)
	// txScoped.BatchWrite(ctx, ops) // still auto-tags with ws:acme
	fmt.Println("Scoped store with transaction support")
}

// ---------------------------------------------------------------------------
// 13. Graph traversal — multi-hop entity exploration
// ---------------------------------------------------------------------------

// TraverseBasicExample shows how to explore entities within N hops of a
// starting entity. This is the simplest traversal — follow all edges in
// both directions up to the default depth (2).
func TraverseBasicExample(ctx context.Context, es *entitystore.EntityStore, startEntityID string) {
	results, err := es.Traverse(ctx, startEntityID, nil) // nil opts = defaults
	if err != nil {
		log.Fatal(err)
	}

	for _, r := range results {
		fmt.Printf("depth %d: %s (%s)\n", r.Depth, r.Entity.ID, r.Entity.EntityType)
	}
}

// TraverseWithDirectionExample shows directional traversal — only follow
// outbound (source→target) or inbound (target→source) edges.
func TraverseWithDirectionExample(ctx context.Context, es *entitystore.EntityStore, personID string) {
	// Outbound only: find entities this person points to (e.g., companies they work at).
	outbound, err := es.Traverse(ctx, personID, &entitystore.TraverseOpts{
		Direction: entitystore.DirectionOutbound,
		MaxDepth:  2,
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Outbound from %s: %d entities\n", personID, len(outbound))

	// Inbound only: find entities that point to this person (e.g., documents mentioning them).
	inbound, err := es.Traverse(ctx, personID, &entitystore.TraverseOpts{
		Direction: entitystore.DirectionInbound,
		MaxDepth:  2,
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Inbound to %s: %d entities\n", personID, len(inbound))
}

// TraverseWithFiltersExample shows how to narrow traversal by relation type,
// entity type, and minimum edge confidence.
func TraverseWithFiltersExample(ctx context.Context, es *entitystore.EntityStore, companyID string) {
	// Find all people connected to a company through "works_at" or "founded"
	// relations, skipping low-confidence edges.
	results, err := es.Traverse(ctx, companyID, &entitystore.TraverseOpts{
		MaxDepth:      2,
		RelationTypes: []string{"works_at", "founded"},      // only follow these edge types
		EntityType:    "entities.v1.Person",                  // only return Person entities
		MinConfidence: 0.7,                                   // skip edges below 70% confidence
		MaxResults:    50,                                    // safety cap
	})
	if err != nil {
		log.Fatal(err)
	}

	for _, r := range results {
		fmt.Printf("  %s at depth %d\n", r.Entity.ID, r.Depth)
		// Each result includes the full path of edges from the start entity.
		for _, edge := range r.Path {
			fmt.Printf("    %s -[%s]-> %s (confidence: %.2f)\n",
				edge.FromID, edge.RelationType, edge.ToID, edge.Confidence)
		}
	}
}

// TraverseWithTagFilterExample shows how to combine traversal with
// tag-based filtering. Filtered-out entities block the path — the
// traversal does not pass through them to reach entities beyond.
func TraverseWithTagFilterExample(ctx context.Context, es *entitystore.EntityStore, entityID string) {
	results, err := es.Traverse(ctx, entityID, &entitystore.TraverseOpts{
		MaxDepth: 3,
		Filter: &entitystore.QueryFilter{
			Tags:       []string{"ws:acme"},            // entity must have this tag
			ExcludeTag: "archived",                     // skip archived entities
			UnlessTags: []string{"pinned"},             // unless they're pinned
		},
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Found %d entities within 3 hops (scoped to ws:acme)\n", len(results))
}

// TraverseRAGContextExample shows how to use traversal for RAG context
// enrichment — gather related entities around a matched entity to provide
// richer context to an LLM.
func TraverseRAGContextExample(ctx context.Context, es *entitystore.EntityStore, matchedEntityID string) {
	// Retrieve the matched entity and its neighborhood for LLM context.
	entity, err := es.GetEntity(ctx, matchedEntityID)
	if err != nil {
		log.Fatal(err)
	}

	// Traverse 2 hops in all directions to gather context.
	neighbors, err := es.Traverse(ctx, matchedEntityID, &entitystore.TraverseOpts{
		MaxDepth:   2,
		MaxResults: 20,
	})
	if err != nil {
		log.Fatal(err)
	}

	// Build context for LLM.
	fmt.Printf("Primary entity: %s (%s)\n", entity.ID, entity.EntityType)
	fmt.Printf("Context entities: %d\n", len(neighbors))
	for _, r := range neighbors {
		fmt.Printf("  [depth %d] %s (%s) via:", r.Depth, r.Entity.ID, r.Entity.EntityType)
		for i, edge := range r.Path {
			if i > 0 {
				fmt.Print(" →")
			}
			fmt.Printf(" %s", edge.RelationType)
		}
		fmt.Println()
	}
}

// TraverseScopedExample shows traversal through a ScopedStore — the scope
// filters are applied at the SQL level, so traversal stops at scope
// boundaries. Entities outside the scope are never visited, and paths
// through them are blocked.
func TraverseScopedExample(ctx context.Context, es *entitystore.EntityStore, workspaceID string, startID string) {
	scoped := es.Scoped(entitystore.ScopeConfig{
		RequireTags: []string{"ws:" + workspaceID},
		ExcludeTag:  "deleted",
		UnlessTags:  []string{"pinned"},
	})

	// Traversal respects the scope — entities without the workspace tag are
	// invisible and block further traversal through them.
	results, err := scoped.Traverse(ctx, startID, &entitystore.TraverseOpts{
		MaxDepth: 3,
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Found %d entities within workspace %s (3 hops)\n", len(results), workspaceID)
}
