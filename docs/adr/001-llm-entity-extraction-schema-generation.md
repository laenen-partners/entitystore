# ADR-001: Generate LLM Entity Extraction Schemas from Proto Annotations

**Status:** Proposed
**Date:** 2026-03-17
**Author:** Pascal Laenen

## Context

The `protoc-gen-entitystore` plugin currently generates `EntityMatchConfig` structs from proto annotations — defining how entities are matched, deduplicated, and merged. However, before matching can happen, entities must be **extracted** from unstructured sources (documents, emails, web pages, etc.).

Entity extraction will be performed by an LLM via [Genkit](https://github.com/firebase/genkit) (out of scope for this project). Genkit supports structured output by accepting a schema definition and optional prompt instructions. Today, these schemas must be hand-written and kept in sync with the proto definitions — a maintenance burden and source of drift.

**The opportunity:** since proto messages already declare the full structure of an entity (field names, types, descriptions) and the entitystore annotations already mark which fields matter for matching, we have everything needed to **generate** the extraction schemas and field descriptions directly from the proto source.

## Decision

Extend `protoc-gen-entitystore` to generate, alongside the existing `MatchConfig` function, a second function per annotated message that returns an **`ExtractionSchema`** — a structured description of the entity suitable for passing to an LLM extraction framework like Genkit.

Additionally, extend the proto annotation options to allow authors to provide **extraction-specific metadata**: extraction hints, examples, and message-level prompt instructions. Field descriptions are derived from **proto leading comments** by default, with an explicit annotation override when the LLM needs different wording than the developer documentation.

## Approach

### 1. Proto Annotation Extensions

Extend the existing `FieldOptions` and `MessageOptions` in `proto/entitystore/v1/options.proto`:

```protobuf
// --- Field-level additions ---
message FieldOptions {
  // ... existing fields (anchor, similarity, weight, etc.) ...

  // Optional explicit description override for the LLM extraction prompt.
  // If omitted, the generator uses the proto leading comment on the field.
  // If neither is present, the field name is humanized (e.g., "full_name" → "full name").
  string description = 10;

  // Additional extraction hint for the LLM (e.g., "Format as ISO 8601 date",
  // "Extract the full legal name, not abbreviations").
  string extraction_hint = 11;

  // Whether this field should be included in extraction output.
  // Defaults to true for all annotated fields. Set to false to exclude
  // fields that are only relevant for matching but not extraction.
  optional bool extract = 12;

  // Examples of valid values for this field, included in the schema
  // to improve LLM extraction accuracy.
  repeated string examples = 13;
}

// --- Enum for extraction output format ---
enum ExtractionFieldType {
  EXTRACTION_FIELD_TYPE_UNSPECIFIED = 0;
  EXTRACTION_FIELD_TYPE_STRING = 1;
  EXTRACTION_FIELD_TYPE_NUMBER = 2;
  EXTRACTION_FIELD_TYPE_BOOLEAN = 3;
  EXTRACTION_FIELD_TYPE_DATE = 4;
  EXTRACTION_FIELD_TYPE_ARRAY = 5;
}

// --- Message-level additions ---
message MessageOptions {
  // ... existing fields (match_thresholds, composite_anchors, etc.) ...

  // System-level prompt instructions prepended to extraction requests.
  // Use this for entity-specific extraction guidance, e.g.,
  // "This is a job posting. Extract structured fields from the raw text."
  string extraction_prompt = 10;

  // Additional context or instructions appended after the schema description.
  // Use for edge-case handling, e.g.,
  // "If multiple people are mentioned, extract only the primary contact."
  string extraction_instructions = 11;

  // Display name for the entity type in prompts (e.g., "Job Posting" instead of "JobPosting").
  string extraction_display_name = 12;
}
```

**Description resolution order** (field-level):
1. Explicit `description` annotation — highest priority, used when the LLM needs different wording than the developer docs.
2. Proto leading comment on the field (e.g., `// Email address of the primary contact.`) — the default for most fields.
3. Humanized field name (e.g., `full_name` → `"full name"`) — fallback when neither is present.

This means **most fields need no `description` annotation at all** — well-commented protos get extraction descriptions for free.

**Additional design rationale:**
- `description` and `extraction_hint` serve different purposes: description is a stable label ("Email address of the primary contact") while hints are directive instructions for the LLM ("Extract the primary email, not CC or forwarded addresses"). Comments naturally fill the description role; hints have no comment equivalent.
- `extract` defaults to true — the common case is that annotated fields should be extracted. Opting out is the exception.
- `examples` improve LLM accuracy through few-shot grounding directly in the schema.
- `ExtractionFieldType` is intentionally simple; the proto scalar type provides the default, and this override exists for semantic types (e.g., `string` proto field that is actually a `DATE`).

### 2. New Domain Type: `ExtractionSchema`

Add to the `matching` package (or a new `extraction` package — see decision below):

```go
// Package: matching (or extraction)

// ExtractionSchema describes an entity type for LLM-based extraction.
type ExtractionSchema struct {
    // EntityType is the fully qualified proto message name.
    EntityType string

    // DisplayName is the human-friendly name (e.g., "Job Posting").
    DisplayName string

    // Prompt is the system-level extraction instruction.
    Prompt string

    // Instructions are additional instructions appended after the schema.
    Instructions string

    // Fields describes each extractable field.
    Fields []ExtractionField
}

// ExtractionField describes a single field for LLM extraction.
type ExtractionField struct {
    // Name is the proto field name (snake_case).
    Name string

    // Description is the human-readable field description.
    Description string

    // Hint is an additional extraction directive for the LLM.
    Hint string

    // Type is the expected output type (string, number, boolean, date, array).
    Type ExtractionFieldType

    // Required indicates the field must be present in extraction output.
    // Derived from: anchor fields are required; others are optional.
    Required bool

    // Repeated indicates the proto field is repeated (list).
    Repeated bool

    // Examples are sample values for few-shot grounding.
    Examples []string
}

type ExtractionFieldType string

const (
    ExtractionFieldTypeString  ExtractionFieldType = "string"
    ExtractionFieldTypeNumber  ExtractionFieldType = "number"
    ExtractionFieldTypeBoolean ExtractionFieldType = "boolean"
    ExtractionFieldTypeDate    ExtractionFieldType = "date"
    ExtractionFieldTypeArray   ExtractionFieldType = "array"
)
```

### 3. Code Generation Output

For each annotated message, the plugin generates a second function:

```go
// Generated example for a JobPosting message:

func JobPostingExtractionSchema() matching.ExtractionSchema {
    return matching.ExtractionSchema{
        EntityType:   "jobs.v1.JobPosting",
        DisplayName:  "Job Posting",
        Prompt:       "Extract structured job posting fields from the provided text.",
        Instructions: "If salary is mentioned as a range, extract the midpoint.",
        Fields: []matching.ExtractionField{
            {
                Name:        "reference",
                Description: "Unique job reference identifier",
                Type:        matching.ExtractionFieldTypeString,
                Required:    true, // anchor field
                Examples:    []string{"JOB-2024-001", "REF-ABC-123"},
            },
            {
                Name:        "title",
                Description: "Job title or position name",
                Hint:        "Extract the exact title as stated, do not normalize",
                Type:        matching.ExtractionFieldTypeString,
                Required:    false,
            },
            {
                Name:        "salary",
                Description: "Annual salary amount",
                Hint:        "Convert to annual if stated as monthly or hourly",
                Type:        matching.ExtractionFieldTypeNumber,
                Required:    false,
            },
        },
    }
}
```

### 4. Genkit Integration (Downstream — Out of Scope)

The generated `ExtractionSchema` is designed to be consumed by Genkit in a downstream project. A thin adapter (not part of entitystore) would convert the schema to Genkit's format:

```go
// In a downstream project — NOT in entitystore
import (
    "github.com/firebase/genkit/go/ai"
    "github.com/laenen-partners/entitystore/matching"
)

func toGenkitSchema(es matching.ExtractionSchema) *ai.GenerateRequest {
    // Convert ExtractionSchema fields → Genkit's JSON schema format
    // Apply es.Prompt as system instruction
    // Apply es.Instructions as additional context
}
```

This keeps entitystore framework-agnostic while providing everything Genkit needs.

### 5. Package Placement Decision

**Option A: Add to `matching` package** (recommended)
- Keeps all proto-derived config types together.
- `ExtractionSchema` is conceptually a sibling of `EntityMatchConfig` — both are generated from the same annotations.
- Avoids a new import path for downstream users.

**Option B: New `extraction` package**
- Cleaner separation of concerns.
- Would require a new import in generated code.
- Justified only if extraction grows significantly in complexity.

**Decision:** Start with Option A. If the extraction types grow beyond ~200 lines or gain dependencies the matching package shouldn't have, extract to a separate package at that point.

## Implementation Plan

### Phase 1: Proto Annotation Extensions
1. Add new fields to `FieldOptions` and `MessageOptions` in `options.proto`.
2. Run `task generate` to regenerate Go proto code in `gen/`.
3. Push updated proto to BSR (`task proto:push`).

### Phase 2: Domain Types
1. Add `ExtractionSchema`, `ExtractionField`, and `ExtractionFieldType` to `matching/extraction.go`.
2. Add `ExtractionSchemaRegistry` (mirrors `MatchConfigRegistry` pattern).
3. Write unit tests for the new types and registry.

### Phase 3: Code Generator Extension
1. Add `generateExtractionSchema()` function in `main.go`.
2. Resolve field descriptions using the fallback chain: explicit `description` annotation → `field.Comments.Leading` (proto comment) → humanized field name.
3. Map proto scalar types to `ExtractionFieldType` (with override from annotation).
4. Derive `Required` from existing `anchor` annotation.
5. Derive `Repeated` from proto field descriptor.
6. Call from `generateFile()` alongside existing `generateMatchConfig()`.
7. Write generator tests using proto test fixtures (including comment-based description tests).

### Phase 4: Documentation & Examples
1. Update `CLAUDE.md` with extraction schema usage.
2. Add example annotated proto in docs.

## Proto Annotation Example (Full)

```protobuf
syntax = "proto3";
package entities.v1;

import "entitystore/v1/options.proto";

message Person {
  option (entitystore.v1.message) = {
    match_thresholds: {auto_match: 0.85, review_zone: 0.60}
    extraction_prompt: "Extract person details from the provided text."
    extraction_instructions: "If multiple people are mentioned, extract only the primary subject. Ignore quoted or referenced individuals."
    extraction_display_name: "Person"
  };

  // Email address of the primary contact.
  string email = 1 [(entitystore.v1.field) = {
    anchor: true
    similarity: SIMILARITY_FUNCTION_EXACT
    weight: 0.40
    normalizer: NORMALIZER_LOWERCASE_TRIM
    // description not set → generator uses leading comment: "Email address of the primary contact."
    extraction_hint: "Extract the primary email address, not CC or forwarded addresses"
    examples: ["john.doe@example.com"]
  }];

  // Full legal name of the person.
  string full_name = 2 [(entitystore.v1.field) = {
    similarity: SIMILARITY_FUNCTION_JARO_WINKLER
    weight: 0.35
    // description not set → generator uses leading comment: "Full legal name of the person."
    extraction_hint: "Use the full name as written, including middle names if present"
    examples: ["John Michael Doe", "Jane Smith-Williams"]
  }];

  // Phone number in E.164 format.
  string phone = 3 [(entitystore.v1.field) = {
    similarity: SIMILARITY_FUNCTION_EXACT
    weight: 0.25
    normalizer: NORMALIZER_PHONE_NORMALIZE
    description: "Phone number including country code"
    // explicit description overrides the comment (different wording needed for LLM)
    extraction_hint: "Include country code if available"
    extract: false  // used for matching only, not extracted
  }];

  // Internal tracking identifier.
  string tracking_id = 4;
  // no entitystore annotation → not included in matching or extraction
}
```

**Description resolution in this example:**
- `email`: uses leading comment → `"Email address of the primary contact."`
- `full_name`: uses leading comment → `"Full legal name of the person."`
- `phone`: uses explicit `description` annotation → `"Phone number including country code"` (overrides the comment)
- `tracking_id`: not annotated, excluded entirely

## Consequences

### Positive
- **Single source of truth:** Entity structure, matching config, and extraction schema all derive from the same proto definition. No drift.
- **Framework-agnostic:** `ExtractionSchema` is a plain Go struct. Works with Genkit, LangChain, direct OpenAI calls, or any other framework.
- **Incremental adoption:** Existing annotated messages continue to work. Extraction metadata is purely additive — no breaking changes.
- **LLM quality improvement:** Field descriptions, hints, and examples in the schema lead to better extraction accuracy vs. bare field names.
- **Compile-time safety:** Generated code is type-checked. Missing fields or typos in annotations are caught at `buf generate` time.

### Negative
- **Proto annotation surface area grows:** More fields to learn and maintain. Mitigated by sensible defaults (description defaults to field name, extract defaults to true).
- **Generated file size increases:** Each message now produces two functions. Minimal impact in practice.
- **Coupling between matching and extraction concerns in annotations:** A field's matching config and extraction config live in the same `FieldOptions` message. This is intentional (single source of truth) but could feel crowded for messages with many fields.

### Risks
- **LLM schema format evolution:** If Genkit or other frameworks change their schema expectations, the adapter layer (downstream, not in entitystore) absorbs the change. The `ExtractionSchema` type itself is stable.
- **Proto field number conflicts:** New fields use numbers 10+ to leave room for future matching-related fields (currently 1–7). This is safe.

## Alternatives Considered

### A. Generate Genkit-specific code directly
Rejected. Ties entitystore to a specific framework. The adapter pattern is more flexible and keeps the library dependency-free.

### B. Use only explicit `description` annotations (no comment fallback)
Considered. Would require every field to have a `description` annotation for extraction. Rejected because:
- Duplicates information already present in well-written proto comments.
- Adds annotation noise for the majority of fields where the comment is perfectly adequate.
- Proto leading comments *are* reliably accessible in `protogen` (via `field.Comments.Leading`), which is the framework this plugin uses.

The chosen hybrid approach (comment default + annotation override) gives the best ergonomics while preserving an escape hatch.

### E. Use only proto comments (no annotation override)
Considered. Would be the simplest approach. Rejected because:
- Comments serve developers; LLM extraction may need different wording (e.g., a terse developer comment vs. a more explicit LLM instruction).
- No way to distinguish between "this comment is a good description" and "this comment is developer-facing only".
- The override annotation is optional and rarely needed, so the cost is minimal.

### C. Separate proto extension for extraction (new extension number)
Considered. Would use a different extension (e.g., `(entitystore.v1.extraction_field)`) to keep matching and extraction annotations separate. Rejected because:
- Forces users to annotate the same field twice.
- Two annotations on one field are harder to read than one annotation with more fields.
- The fields are logically related (an anchor in matching is required in extraction).

### D. JSON Schema output instead of Go struct
Considered. Would generate a `.json` file per message instead of Go code. Rejected because:
- Loses compile-time type safety.
- Harder to integrate programmatically in Go.
- Can always be derived from `ExtractionSchema` at runtime if needed (add a `ToJSONSchema()` method).
