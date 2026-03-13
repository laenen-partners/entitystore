# EntityStore Tutorial: Family Butler

This tutorial walks through designing entity types for a "Family Butler" application -- a personal assistant that manages people, families, documents, assets, events, and their relationships. Each section introduces a different matching pattern.

## Overview

The Family Butler manages:

| Entity | Description | Matching pattern |
|---|---|---|
| Person | Family members, contacts, professionals | Multi-anchor + fuzzy name matching |
| Family | Family units (households) | Composite anchor (scoped uniqueness) |
| Document | Contracts, IDs, certificates, receipts | Single anchor (document number) |
| Asset | Properties, vehicles, accounts, valuables | Multi-type anchors with conflict strategies |
| Event | Birthdays, appointments, deadlines, milestones | Temporal scoping with composite anchors |
| Note | Free-text notes, reminders, action items | Embedding-only (semantic search) |

## Prerequisites

Add the entitystore BSR dependency to your project:

```yaml
# buf.yaml
version: v2
modules:
  - path: proto
deps:
  - buf.build/laenen-partners/entitystore
```

```sh
buf dep update
```

Add the code generator to your `buf.gen.yaml`:

```yaml
# buf.gen.yaml
version: v2
plugins:
  - remote: buf.build/protocolbuffers/go
    out: gen
    opt: paths=source_relative
  - local: ["go", "run", "github.com/laenen-partners/entitystore/cmd/protoc-gen-entitystore@latest"]
    out: gen
    opt: paths=source_relative
```

---

## Pattern 1: Multi-anchor with fuzzy matching (Person)

Persons are the most common entity and demonstrate multiple matching strategies working together.

**Design decisions:**
- Email and phone are single anchors -- each uniquely identifies a person
- Full name + date of birth is a composite anchor -- for when no email/phone is available
- Name uses Jaro-Winkler similarity for typo tolerance ("Jon" vs "John")
- Phone uses a dedicated normalizer to canonicalize formats

```protobuf
// proto/butler/v1/person.proto
syntax = "proto3";
package butler.v1;

import "entitystore/v1/options.proto";

message Person {
  option (entitystore.v1.message) = {
    match_thresholds: {auto_match: 0.85, review_zone: 0.60}
    composite_anchors: [{fields: ["full_name", "date_of_birth"]}]
    allowed_relations: [
      "member_of",       // Person → Family
      "spouse_of",       // Person → Person
      "parent_of",       // Person → Person
      "child_of",        // Person → Person
      "emergency_contact_for", // Person → Person
      "owner_of",        // Person → Asset
      "signatory_of",    // Person → Document
      "attendee_of"      // Person → Event
    ]
  };

  // Anchor: email uniquely identifies a person globally.
  string email = 1 [(entitystore.v1.field) = {
    anchor: true
    similarity: SIMILARITY_FUNCTION_EXACT
    weight: 0.25
    normalizer: NORMALIZER_LOWERCASE_TRIM
    conflict_strategy: CONFLICT_STRATEGY_FLAG_FOR_REVIEW
    embed: true
  }];

  // Anchor: phone uniquely identifies a person globally.
  string phone = 2 [(entitystore.v1.field) = {
    anchor: true
    similarity: SIMILARITY_FUNCTION_EXACT
    weight: 0.15
    normalizer: NORMALIZER_PHONE_NORMALIZE
    conflict_strategy: CONFLICT_STRATEGY_LATEST_WINS
  }];

  // Fuzzy match: Jaro-Winkler catches typos in names.
  string full_name = 3 [(entitystore.v1.field) = {
    similarity: SIMILARITY_FUNCTION_JARO_WINKLER
    weight: 0.30
    normalizer: NORMALIZER_LOWERCASE_TRIM
    embed: true
    token_field: true
  }];

  // Part of composite anchor (full_name + date_of_birth).
  string date_of_birth = 4 [(entitystore.v1.field) = {
    similarity: SIMILARITY_FUNCTION_EXACT
    weight: 0.15
  }];

  // No annotations: extracted but does not participate in matching.
  string role = 5;
  string notes = 6;

  // Evolving field: latest extraction wins.
  string address = 7 [(entitystore.v1.field) = {
    similarity: SIMILARITY_FUNCTION_LEVENSHTEIN
    weight: 0.15
    normalizer: NORMALIZER_LOWERCASE_TRIM
    conflict_strategy: CONFLICT_STRATEGY_LATEST_WINS
    embed: true
    token_field: true
  }];
}
```

**How matching works for Person:**

1. **Anchor lookup** (fast path): If the incoming person has an email or phone, `FindByAnchors` does an O(1) lookup. Match found → update existing entity.
2. **Composite anchor** (fallback): If no email/phone but name + DOB are present, the composite anchor fires. Useful for minors or elderly without email.
3. **Token blocking** (fuzzy): If no anchors match, `FindByTokens` retrieves candidates whose name/address tokens overlap.
4. **Embedding search** (semantic): `FindByEmbedding` catches near-matches that token overlap misses.
5. **Scoring**: Candidates are scored using field weights and similarity functions. Score >= 0.85 → auto-merge. 0.60-0.85 → human review. < 0.60 → new entity.

---

## Pattern 2: Scoped uniqueness (Family)

Families demonstrate composite anchors as scoped uniqueness constraints. A family name is not globally unique ("The Smiths" exists many times), but a family name scoped to a primary contact is.

```protobuf
// proto/butler/v1/family.proto
syntax = "proto3";
package butler.v1;

import "entitystore/v1/options.proto";

message Family {
  option (entitystore.v1.message) = {
    match_thresholds: {auto_match: 0.90, review_zone: 0.65}
    composite_anchors: [
      {fields: ["family_name", "primary_contact_email"]}
    ]
    allowed_relations: [
      "has_member",      // Family → Person
      "owns",            // Family → Asset
      "subscribes_to"    // Family → service/subscription
    ]
  };

  // Not a single anchor -- "Smith" is not globally unique.
  string family_name = 1 [(entitystore.v1.field) = {
    similarity: SIMILARITY_FUNCTION_JARO_WINKLER
    weight: 0.40
    normalizer: NORMALIZER_LOWERCASE_TRIM
    embed: true
    token_field: true
  }];

  // Scopes the composite anchor.
  string primary_contact_email = 2 [(entitystore.v1.field) = {
    similarity: SIMILARITY_FUNCTION_EXACT
    weight: 0.40
    normalizer: NORMALIZER_LOWERCASE_TRIM
  }];

  // No matching annotations.
  string address = 3;
  string phone = 4;
  int32 member_count = 5;
}
```

**Stored anchors:**

| `anchor_field` | `normalized_value` | Meaning |
|---|---|---|
| `family_name\|primary_contact_email` | `smith\|john@smith.com` | Unique per contact |

"The Smiths" with `john@smith.com` and "The Smiths" with `jane@other-smith.com` are treated as different families. Same family name + same contact email → same family.

---

## Pattern 3: Single anchor with strict thresholds (Document)

Documents have a natural unique identifier (document number, reference code). Strict thresholds prevent accidental merges -- documents should almost never auto-merge without a very high confidence.

```protobuf
// proto/butler/v1/document.proto
syntax = "proto3";
package butler.v1;

import "entitystore/v1/options.proto";

message Document {
  option (entitystore.v1.message) = {
    match_thresholds: {auto_match: 0.95, review_zone: 0.80}
    allowed_relations: [
      "belongs_to",      // Document → Person
      "concerns",        // Document → Asset
      "supersedes",      // Document → Document (newer version)
      "attachment_of"    // Document → Event
    ]
  };

  // Single anchor: document numbers are globally unique.
  string document_number = 1 [(entitystore.v1.field) = {
    anchor: true
    similarity: SIMILARITY_FUNCTION_EXACT
    weight: 0.50
    normalizer: NORMALIZER_LOWERCASE_TRIM
    conflict_strategy: CONFLICT_STRATEGY_FLAG_FOR_REVIEW
  }];

  // Matching fields for when document_number is missing.
  string title = 2 [(entitystore.v1.field) = {
    similarity: SIMILARITY_FUNCTION_TOKEN_JACCARD
    weight: 0.25
    embed: true
    token_field: true
  }];

  string document_type = 3 [(entitystore.v1.field) = {
    similarity: SIMILARITY_FUNCTION_EXACT
    weight: 0.15
    normalizer: NORMALIZER_LOWERCASE_TRIM
  }];

  string issued_date = 4 [(entitystore.v1.field) = {
    similarity: SIMILARITY_FUNCTION_EXACT
    weight: 0.10
  }];

  // Extraction-only fields.
  string issuer = 5;
  string expiry_date = 6;
  string storage_location = 7; // objectstore bucket/key
  string summary = 8;
}
```

**Why 0.95 auto_match?** Documents are high-stakes. A passport and a driver's license might share similar metadata but are different documents. Only near-perfect matches should auto-merge.

---

## Pattern 4: Multi-type anchors with conflict strategies (Asset)

Assets come in many types (property, vehicle, account) each with different natural identifiers. Multiple single anchors cover different asset types. Conflict strategies handle evolving values like market price.

```protobuf
// proto/butler/v1/asset.proto
syntax = "proto3";
package butler.v1;

import "entitystore/v1/options.proto";

message Asset {
  option (entitystore.v1.message) = {
    match_thresholds: {auto_match: 0.90, review_zone: 0.70}
    composite_anchors: [
      {fields: ["asset_type", "name"]}
    ]
    allowed_relations: [
      "owned_by",        // Asset → Person
      "owned_by_family", // Asset → Family
      "insured_by",      // Asset → Document (insurance policy)
      "located_at",      // Asset → address/location
      "valued_at"        // Asset → valuation event
    ]
  };

  // Registration number (vehicles), account number (financial),
  // cadastral reference (property). Globally unique when present.
  string reference_number = 1 [(entitystore.v1.field) = {
    anchor: true
    similarity: SIMILARITY_FUNCTION_EXACT
    weight: 0.40
    normalizer: NORMALIZER_LOWERCASE_TRIM
    conflict_strategy: CONFLICT_STRATEGY_FLAG_FOR_REVIEW
  }];

  // "property", "vehicle", "bank_account", "investment", "valuable"
  string asset_type = 2 [(entitystore.v1.field) = {
    similarity: SIMILARITY_FUNCTION_EXACT
    weight: 0.15
    normalizer: NORMALIZER_LOWERCASE_TRIM
  }];

  string name = 3 [(entitystore.v1.field) = {
    similarity: SIMILARITY_FUNCTION_JARO_WINKLER
    weight: 0.25
    normalizer: NORMALIZER_LOWERCASE_TRIM
    embed: true
    token_field: true
  }];

  string description = 4 [(entitystore.v1.field) = {
    similarity: SIMILARITY_FUNCTION_TOKEN_JACCARD
    weight: 0.10
    embed: true
  }];

  // Evolving: always take the latest valuation.
  string estimated_value = 5 [(entitystore.v1.field) = {
    weight: 0.10
    conflict_strategy: CONFLICT_STRATEGY_LATEST_WINS
  }];

  // Extraction-only.
  string currency = 6;
  string acquisition_date = 7;
  string location = 8;
}
```

**Composite anchor pattern:** `asset_type + name` creates scoped uniqueness. "Main Residence" is unique per asset type -- you can have a property called "Main Residence" and a vehicle called "Main Residence" (unlikely, but safe).

---

## Pattern 5: Temporal scoping (Event)

Events use composite anchors that include a date to prevent duplicates within the same time scope. "Dentist appointment" is not unique, but "Dentist appointment on 2026-03-15 for john@smith.com" is.

```protobuf
// proto/butler/v1/event.proto
syntax = "proto3";
package butler.v1;

import "entitystore/v1/options.proto";

message Event {
  option (entitystore.v1.message) = {
    match_thresholds: {auto_match: 0.90, review_zone: 0.65}
    composite_anchors: [
      {fields: ["event_type", "event_date", "organizer"]}
    ]
    allowed_relations: [
      "attended_by",     // Event → Person
      "concerns_asset",  // Event → Asset (e.g. property inspection)
      "documented_by",   // Event → Document (minutes, receipts)
      "follow_up_to",    // Event → Event
      "organized_by"     // Event → Person
    ]
  };

  // "birthday", "appointment", "deadline", "milestone", "meeting"
  string event_type = 1 [(entitystore.v1.field) = {
    similarity: SIMILARITY_FUNCTION_EXACT
    weight: 0.20
    normalizer: NORMALIZER_LOWERCASE_TRIM
  }];

  string title = 2 [(entitystore.v1.field) = {
    similarity: SIMILARITY_FUNCTION_TOKEN_JACCARD
    weight: 0.30
    embed: true
    token_field: true
  }];

  // Part of composite anchor -- scopes by date.
  string event_date = 3 [(entitystore.v1.field) = {
    similarity: SIMILARITY_FUNCTION_EXACT
    weight: 0.20
  }];

  // Part of composite anchor -- scopes by who organized it.
  string organizer = 4 [(entitystore.v1.field) = {
    similarity: SIMILARITY_FUNCTION_EXACT
    weight: 0.15
    normalizer: NORMALIZER_LOWERCASE_TRIM
  }];

  string location = 5 [(entitystore.v1.field) = {
    similarity: SIMILARITY_FUNCTION_LEVENSHTEIN
    weight: 0.15
    embed: true
  }];

  // Extraction-only.
  string description = 6;
  string end_date = 7;
  bool recurring = 8;
  string recurrence_rule = 9;
}
```

**Why temporal scoping?** Without the date in the composite anchor, re-extracting "Dentist appointment" from a new email would match the old one. With the date, each occurrence is a separate entity linked by `follow_up_to` relations.

---

## Pattern 6: Embedding-only matching (Note)

Notes have no natural identifiers. They are matched purely by semantic similarity via vector embeddings. This is useful for deduplicating free-text content that may be phrased differently but mean the same thing.

```protobuf
// proto/butler/v1/note.proto
syntax = "proto3";
package butler.v1;

import "entitystore/v1/options.proto";

message Note {
  option (entitystore.v1.message) = {
    match_thresholds: {auto_match: 0.92, review_zone: 0.75}
    allowed_relations: [
      "about_person",    // Note → Person
      "about_asset",     // Note → Asset
      "about_event",     // Note → Event
      "about_document",  // Note → Document
      "follow_up"        // Note → Note
    ]
  };

  // No anchors, no token fields -- purely semantic matching.
  string content = 1 [(entitystore.v1.field) = {
    weight: 0.60
    embed: true
  }];

  string subject = 2 [(entitystore.v1.field) = {
    similarity: SIMILARITY_FUNCTION_TOKEN_JACCARD
    weight: 0.25
    embed: true
    token_field: true
  }];

  string category = 3 [(entitystore.v1.field) = {
    similarity: SIMILARITY_FUNCTION_EXACT
    weight: 0.15
    normalizer: NORMALIZER_LOWERCASE_TRIM
  }];

  // Extraction-only.
  string source = 4;       // "email", "voice", "scan", "manual"
  string created_by = 5;
  bool action_required = 6;
  string due_date = 7;
}
```

**High auto_match threshold (0.92):** Since there are no anchors, matching relies entirely on embeddings and token overlap. A high threshold prevents false merges between notes that are topically similar but distinct ("Call dentist to reschedule" vs "Call dentist to confirm").

---

## Relationship graph

The `allowed_relations` annotations define the full relationship graph:

```
                    member_of
              Person --------→ Family
             ↗ ↕ ↘              ↓ owns
  spouse_of ↔  parent_of      Asset
  child_of     ↕              ↑ owned_by
            attendee_of    insured_by
               ↓              ↓
             Event ←----→ Document
          follow_up_to    supersedes
               ↑              ↑
            about_event   about_document
               ↓              ↓
              Note ←--→ Note
              follow_up
```

Relations are directed edges stored with confidence, evidence, and optional structured data:

```go
store.BatchWrite(ctx, []store.BatchWriteOp{{
    UpsertRelation: &store.UpsertRelationOp{
        SourceID:     personID,
        TargetID:     familyID,
        RelationType: "member_of",
        Confidence:   0.95,
        Evidence:     "Extracted from family registration document",
        SourceURN:    documentID,
        Data: map[string]any{
            "role":  "head_of_household",
            "since": "2020-01-15",
        },
    },
}})
```

---

## Registration in Go

After running `buf generate`, register all entity configs:

```go
package main

import (
    "github.com/laenen-partners/entitystore/matching"

    butlerv1 "example.com/butler/gen/butler/v1"
)

func newRegistry() *matching.MatchConfigRegistry {
    mcr := matching.NewMatchConfigRegistry()
    mcr.Register(butlerv1.PersonMatchConfig())
    mcr.Register(butlerv1.FamilyMatchConfig())
    mcr.Register(butlerv1.DocumentMatchConfig())
    mcr.Register(butlerv1.AssetMatchConfig())
    mcr.Register(butlerv1.EventMatchConfig())
    mcr.Register(butlerv1.NoteMatchConfig())
    return mcr
}
```

## Entity resolution flow

When the extraction pipeline processes a new document:

```go
cfg, _ := registry.Get(entity.EntityType)

// 1. Build anchors from extracted data.
anchors := matching.BuildAnchors(entity.Data, cfg)

// 2. Fast path: exact anchor lookup.
if len(anchors) > 0 {
    matches, _ := store.FindByAnchors(ctx, entity.EntityType, anchors, nil)
    if len(matches) == 1 {
        // Anchor hit → update existing entity.
        return
    }
}

// 3. Build tokens for fuzzy blocking.
tokens := matching.BuildTokens(entity.Data, cfg)
var candidates []matching.StoredEntity
if tokens != nil {
    allTokens := flattenTokens(tokens)
    candidates, _ = store.FindByTokens(ctx, entity.EntityType, allTokens, 10, nil)
}

// 4. Embedding search for semantic matches.
if len(cfg.EmbedFields) > 0 {
    vec, _ := matching.ComputeEmbedding(ctx, entity.Data, cfg, embedder)
    embMatches, _ := store.FindByEmbedding(ctx, entity.EntityType, vec, 5, nil)
    candidates = append(candidates, embMatches...)
}

// 5. Score candidates against thresholds.
// score >= cfg.Thresholds.AutoMatch   → auto-merge
// score >= cfg.Thresholds.ReviewZone  → flag for review
// score < cfg.Thresholds.ReviewZone   → create new entity
```

## Tag-based filtering

Tags enable multi-tenant and workflow filtering across all search operations:

```go
// Create with tags via BatchWrite.
results, _ := store.BatchWrite(ctx, []store.BatchWriteOp{{
    WriteEntity: &store.WriteEntityOp{
        Action:     store.ActionCreate,
        EntityType: "butler.v1.Person",
        Data:       data,
        Confidence: 0.95,
        Tags:       []string{"family:smith", "source:email", "status:verified"},
    },
}})

// Search within a family.
filter := &matching.QueryFilter{Tags: []string{"family:smith"}}
results, _ := store.FindByAnchors(ctx, "butler.v1.Person", anchors, filter)

// Search across a workflow stage.
pending, _ := store.GetEntitiesByType(ctx, "butler.v1.Document")
// Then filter client-side by tag, or use FindByTokens with tag filter.
```

## Summary of patterns

| Pattern | When to use | Example |
|---|---|---|
| **Single anchor** | Field is globally unique | Email, document number, registration plate |
| **Composite anchor** | Uniqueness is scoped | Family name + contact, event type + date + organizer |
| **Multi-anchor** | Entity has multiple unique identifiers | Person with both email and phone |
| **Fuzzy matching** | Typos and variations expected | Names (Jaro-Winkler), addresses (Levenshtein) |
| **Token blocking** | Multi-word fields, order doesn't matter | Job titles, descriptions (Token Jaccard) |
| **Embedding-only** | No natural identifiers, semantic meaning | Free-text notes, summaries |
| **Strict thresholds** | High-stakes entities, false merges are costly | Documents (0.95), financial assets |
| **Relaxed thresholds** | Approximate matches are acceptable | Addresses (0.80), notes |
| **Conflict strategies** | Fields evolve over time | Phone (latest wins), email (flag for review) |
| **Temporal scoping** | Same label recurs at different times | Appointments, deadlines, milestones |
| **Allowed relations** | Constrain the relationship graph | Prevent nonsensical edges |
