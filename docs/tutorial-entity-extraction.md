# Entity Extraction with Genkit and EntityStore Code Generation

This tutorial shows how to use `protoc-gen-entitystore` generated extraction schemas with [Firebase Genkit](https://github.com/firebase/genkit) to extract structured entities from unstructured text using an LLM.

## Overview

The pipeline works in three stages:

1. **Define** — annotate proto messages with entitystore field and message options
2. **Generate** — `buf generate` produces `{Message}ExtractionSchema()` and `{Message}MatchConfig()` functions
3. **Extract** — use the generated `ExtractionSchema` to drive Genkit structured output calls

EntityStore generates the schema and descriptions. Genkit handles the LLM call. Your application code is the glue.

## Prerequisites

- Go 1.25+
- [Buf CLI](https://buf.build/docs/installation)
- A Google AI API key (`GEMINI_API_KEY` env var) or Vertex AI project
- Docker (for entitystore integration tests)

## Step 1: Annotate your proto messages

```protobuf
// proto/entities/v1/person.proto
syntax = "proto3";
package entities.v1;

option go_package = "example.com/myapp/gen/entities/v1;entitiesv1";

import "entitystore/v1/options.proto";

message Person {
  option (entitystore.v1.message) = {
    match_thresholds: {auto_match: 0.85, review_zone: 0.60}
    extraction_prompt: "Extract person details from the provided text."
    extraction_instructions: "If multiple people are mentioned, extract only the primary subject. Ignore quoted or referenced individuals."
    extraction_display_name: "Person"
  };

  // Primary email address.
  string email = 1 [(entitystore.v1.field) = {
    anchor: true
    similarity: SIMILARITY_FUNCTION_EXACT
    weight: 0.30
    normalizer: NORMALIZER_LOWERCASE_TRIM
    extraction_hint: "Extract the primary email, not CC or forwarded addresses"
    examples: ["john.doe@example.com", "jane@company.org"]
  }];

  // Full legal name of the person.
  string full_name = 2 [(entitystore.v1.field) = {
    similarity: SIMILARITY_FUNCTION_JARO_WINKLER
    weight: 0.35
    embed: true
    token_field: true
    extraction_hint: "Use the full name as written, including middle names if present"
    examples: ["John Michael Doe", "Jane Smith-Williams"]
  }];

  // Phone number.
  string phone = 3 [(entitystore.v1.field) = {
    similarity: SIMILARITY_FUNCTION_EXACT
    weight: 0.15
    normalizer: NORMALIZER_PHONE_NORMALIZE
    extraction_hint: "Include country code if available"
    examples: ["+1-555-867-5309"]
  }];

  // Current job title or role.
  string job_title = 4 [(entitystore.v1.field) = {
    similarity: SIMILARITY_FUNCTION_TOKEN_JACCARD
    embed: true
    token_field: true
  }];

  // Company the person works at.
  string company = 5;

  // Free-form notes or context.
  string notes = 6;
}
```

**Key annotation concepts:**

- **Proto leading comments** (e.g., `// Primary email address.`) become the field description automatically — no need for an explicit `description` annotation in most cases.
- **`extraction_hint`** adds directive instructions for the LLM beyond the description.
- **`examples`** provide few-shot grounding to improve extraction accuracy.
- **`extraction_prompt`** and **`extraction_instructions`** on the message configure the system-level context.
- Fields without `(entitystore.v1.field)` annotations (like `company` and `notes`) are still included in the extraction schema — they just don't participate in matching.
- Set `extract: false` on fields that should be used for matching only (e.g., internal IDs).

## Step 2: Configure buf and generate

**`buf.yaml`:**

```yaml
version: v2
modules:
  - path: proto
deps:
  - buf.build/laenen-partners/entitystore
```

**`buf.gen.yaml`:**

```yaml
version: v2
plugins:
  - remote: buf.build/protocolbuffers/go
    out: gen
    opt: paths=source_relative
  - local: ["go", "run", "github.com/laenen-partners/entitystore/cmd/protoc-gen-entitystore@latest"]
    out: gen
    opt: paths=source_relative
```

```sh
buf dep update
buf generate
```

This produces `gen/entities/v1/person_entitystore.go` containing:

- `PersonMatchConfig()` — for entity deduplication and matching
- `PersonExtractionSchema()` — for LLM entity extraction

## Step 3: Build the extraction pipeline

### 3.1 Install dependencies

```sh
go get github.com/firebase/genkit/go@latest
go get github.com/firebase/genkit/go/plugins/googlegenai@latest
go get github.com/laenen-partners/entitystore@latest
```

### 3.2 Convert ExtractionSchema to a Genkit-compatible Go struct

The generated `ExtractionSchema` is framework-agnostic. We need a thin adapter to convert it into a Go struct that Genkit can use for structured output, and a prompt builder that uses the schema's descriptions and hints.

```go
// extract/adapter.go
package extract

import (
	"fmt"
	"strings"

	"github.com/laenen-partners/entitystore/matching"
)

// BuildExtractionPrompt constructs a detailed system prompt from an ExtractionSchema.
// This prompt tells the LLM exactly what fields to extract and how.
func BuildExtractionPrompt(schema matching.ExtractionSchema) string {
	var b strings.Builder

	// System-level prompt.
	if schema.Prompt != "" {
		b.WriteString(schema.Prompt)
		b.WriteString("\n\n")
	}

	// Field descriptions.
	b.WriteString(fmt.Sprintf("Extract the following %s fields:\n\n", schema.DisplayName))

	for _, f := range schema.Fields {
		required := ""
		if f.Required {
			required = " (REQUIRED)"
		}
		repeated := ""
		if f.Repeated {
			repeated = " (can be multiple values)"
		}

		b.WriteString(fmt.Sprintf("- **%s** (%s)%s%s: %s",
			f.Name, f.Type, required, repeated, f.Description))

		if f.Hint != "" {
			b.WriteString(fmt.Sprintf(". Hint: %s", f.Hint))
		}

		if len(f.Examples) > 0 {
			b.WriteString(fmt.Sprintf(". Examples: %s", strings.Join(f.Examples, ", ")))
		}

		b.WriteString("\n")
	}

	// Additional instructions.
	if schema.Instructions != "" {
		b.WriteString("\nAdditional instructions:\n")
		b.WriteString(schema.Instructions)
		b.WriteString("\n")
	}

	b.WriteString("\nReturn the extracted fields as a JSON object. ")
	b.WriteString("Use null for fields that cannot be determined from the text.")

	return b.String()
}

// ExtractionResult is a generic map for extracted entity data.
// Each key is a field name from the ExtractionSchema.
type ExtractionResult map[string]any
```

### 3.3 Extract entities with Genkit

```go
// extract/extract.go
package extract

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/genkit"

	"github.com/laenen-partners/entitystore/matching"
)

// Extractor uses Genkit to extract structured entities from text.
type Extractor struct {
	g       *genkit.Genkit
	schemas *matching.ExtractionSchemaRegistry
}

// NewExtractor creates an Extractor with a Genkit instance and schema registry.
func NewExtractor(g *genkit.Genkit, schemas *matching.ExtractionSchemaRegistry) *Extractor {
	return &Extractor{g: g, schemas: schemas}
}

// Extract extracts an entity of the given type from unstructured text.
func (e *Extractor) Extract(ctx context.Context, entityType string, text string) (ExtractionResult, error) {
	schema, ok := e.schemas.Get(entityType)
	if !ok {
		return nil, fmt.Errorf("no extraction schema for %q", entityType)
	}

	systemPrompt := BuildExtractionPrompt(schema)

	resp, err := genkit.Generate(ctx, e.g,
		ai.WithSystem(systemPrompt),
		ai.WithPrompt(text),
		ai.WithOutputType(ExtractionResult{}),
	)
	if err != nil {
		return nil, fmt.Errorf("genkit generate: %w", err)
	}

	var result ExtractionResult
	if err := resp.Output(&result); err != nil {
		return nil, fmt.Errorf("parse output: %w", err)
	}

	return result, nil
}

// ExtractToJSON extracts an entity and returns it as json.RawMessage,
// ready for entitystore BatchWrite.
func (e *Extractor) ExtractToJSON(ctx context.Context, entityType string, text string) (json.RawMessage, error) {
	result, err := e.Extract(ctx, entityType, text)
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}
```

### 3.4 Wire it all together

```go
// main.go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/firebase/genkit/go/genkit"
	"github.com/firebase/genkit/go/plugins/googlegenai"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/laenen-partners/entitystore"
	"github.com/laenen-partners/entitystore/matching"

	entitiesv1 "example.com/myapp/gen/entities/v1"
	"example.com/myapp/extract"
)

func main() {
	ctx := context.Background()

	// --- 1. Initialize Genkit ---
	g := genkit.Init(ctx,
		genkit.WithPlugins(&googlegenai.GoogleAI{}),
		genkit.WithDefaultModel("googleai/gemini-2.5-flash"),
	)

	// --- 2. Register extraction schemas ---
	schemas := matching.NewExtractionSchemaRegistry()
	schemas.Register(entitiesv1.PersonExtractionSchema())
	// schemas.Register(entitiesv1.InvoiceExtractionSchema())
	// schemas.Register(entitiesv1.JobPostingExtractionSchema())

	// --- 3. Register match configs ---
	configs := matching.NewMatchConfigRegistry()
	configs.Register(entitiesv1.PersonMatchConfig())

	// --- 4. Create extractor ---
	extractor := extract.NewExtractor(g, schemas)

	// --- 5. Extract from unstructured text ---
	email := `
		Hi team,

		I wanted to introduce John Michael Doe, our new VP of Engineering.
		He'll be starting next Monday. You can reach him at john.doe@acme.com
		or +1-555-867-5309.

		Best,
		HR Team
	`

	result, err := extractor.Extract(ctx, "entities.v1.Person", email)
	if err != nil {
		log.Fatalf("extraction failed: %v", err)
	}

	fmt.Printf("Extracted Person:\n")
	data, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(data))
	// Output:
	// {
	//   "email": "john.doe@acme.com",
	//   "full_name": "John Michael Doe",
	//   "phone": "+1-555-867-5309",
	//   "job_title": "VP of Engineering",
	//   "company": "Acme",
	//   "notes": null
	// }

	// --- 6. Match against existing entities using the Matcher ---
	cfg := entitiesv1.PersonMatchConfig()
	entityData, _ := json.Marshal(result)

	// The Matcher handles the full pipeline: anchor lookup → fuzzy candidates → scoring → decision.
	matcher := matching.NewMatcher(cfg, es)
	decision, err := matcher.Match(ctx, entityData)
	if err != nil {
		log.Fatalf("match failed: %v", err)
	}

	fmt.Printf("Match decision: %s (confidence: %.2f, method: %s)\n",
		decision.Action, decision.MatchConfidence, decision.MatchMethod)

	// --- 7. Act on the match decision ---
	pool, err := pgxpool.New(ctx, "postgres://user:pass@localhost:5432/mydb")
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	entitystore.Migrate(ctx, pool)
	es, _ := entitystore.New(entitystore.WithPgStore(pool))
	defer es.Close()

	// Build anchors and tokens for storage.
	anchors := matching.BuildAnchors(entityData, cfg)
	tokens := matching.BuildTokens(entityData, cfg)

	// Convert extracted data to a proto message for storage.
	protoData, _ := structpb.NewStruct(result)

	switch decision.Action {
	case matching.ActionCreate:
		fmt.Println("Creating new entity...")
		_, err = es.BatchWrite(ctx, []entitystore.BatchWriteOp{
			{WriteEntity: &entitystore.WriteEntityOp{
				Action:     entitystore.WriteActionCreate,
				Data:       protoData,
				Confidence: 0.92,
				Anchors:    anchors,
				Tokens:     tokens,
				Provenance: entitystore.ProvenanceEntry{
					SourceURN:   "email:inbox/intro-email",
					ExtractedAt: time.Now(),
					ModelID:     "gemini-2.5-flash",
					Confidence:  0.92,
					MatchMethod: "create",
				},
			}},
		})

	case matching.ActionUpdate:
		fmt.Printf("Updating existing entity %s...\n", decision.MatchedRecordID)
		_, err = es.BatchWrite(ctx, []entitystore.BatchWriteOp{
			{WriteEntity: &entitystore.WriteEntityOp{
				Action:          entitystore.WriteActionMerge,
				MatchedEntityID: decision.MatchedRecordID,
				Data:            protoData,
				Confidence:      0.92,
				Anchors:         anchors,
				Tokens:          tokens,
				Provenance: entitystore.ProvenanceEntry{
					SourceURN:       "email:inbox/intro-email",
					ExtractedAt:     time.Now(),
					ModelID:         "gemini-2.5-flash",
					Confidence:      0.92,
					MatchMethod:     decision.MatchMethod,
					MatchConfidence: decision.MatchConfidence,
				},
			}},
		})

	case matching.ActionReview:
		fmt.Printf("Flagged for review — best candidate: %s (%.2f)\n",
			decision.MatchedRecordID, decision.MatchConfidence)
		// Send to review queue...

	case matching.ActionConflict:
		fmt.Printf("Conflict detected — %d candidates\n", len(decision.Candidates))
		if decision.MergePlan != nil {
			for _, op := range decision.MergePlan {
				if op.Op == matching.MergeConflict {
					fmt.Printf("  Field %s: existing=%v, extracted=%v — %s\n",
						op.Field, op.ExistingValue, op.ExtractedValue, op.Reason)
				}
			}
		}
	}

	if err != nil {
		log.Fatalf("store: %v", err)
	}

	fmt.Println("Done!")
}
```

## Step 4: Multi-entity extraction

For documents that contain multiple entity types, iterate over your registered schemas:

```go
func extractAll(ctx context.Context, extractor *extract.Extractor, entityTypes []string, text string) (map[string]extract.ExtractionResult, error) {
	results := make(map[string]extract.ExtractionResult)
	for _, entityType := range entityTypes {
		result, err := extractor.Extract(ctx, entityType, text)
		if err != nil {
			return nil, fmt.Errorf("extract %s: %w", entityType, err)
		}
		results[entityType] = result
	}
	return results, nil
}

// Usage:
results, err := extractAll(ctx, extractor,
	[]string{"entities.v1.Person", "entities.v1.Company"},
	documentText,
)
```

## Step 5: Typed extraction with GenerateData

For compile-time type safety, define a Go struct matching your proto and use `genkit.GenerateData[T]`:

```go
// Matches the proto Person message fields.
type PersonData struct {
	Email    string `json:"email"`
	FullName string `json:"full_name"`
	Phone    string `json:"phone"`
	JobTitle string `json:"job_title"`
	Company  string `json:"company"`
	Notes    string `json:"notes"`
}

func extractTypedPerson(ctx context.Context, g *genkit.Genkit, schema matching.ExtractionSchema, text string) (*PersonData, error) {
	systemPrompt := extract.BuildExtractionPrompt(schema)

	person, _, err := genkit.GenerateData[PersonData](ctx, g,
		ai.WithSystem(systemPrompt),
		ai.WithPrompt(text),
	)
	return person, err
}
```

This approach gives you:
- Compile-time type checking on the extracted data
- No need to unmarshal from `map[string]any`
- IDE autocompletion on extracted fields

The trade-off is maintaining a Go struct alongside the proto definition. For entity types with many fields or frequent changes, the dynamic `ExtractionResult` approach (Step 3) avoids this duplication.

## How the pieces fit together

```
┌─────────────────────────────────────────────────────────┐
│                    Proto Definition                      │
│  message Person {                                        │
│    option (entitystore.v1.message) = { ... };            │
│    // Primary email address.                             │
│    string email = 1 [(entitystore.v1.field) = { ... }];  │
│  }                                                       │
└──────────────────────┬──────────────────────────────────┘
                       │ buf generate
                       ▼
┌──────────────────────────────────────────────────────────┐
│              Generated Code                               │
│  PersonMatchConfig()      → matching.EntityMatchConfig    │
│  PersonExtractionSchema() → matching.ExtractionSchema     │
└──────────┬───────────────────────────┬───────────────────┘
           │                           │
           ▼                           ▼
┌────────────────────┐    ┌───────────────────────────────┐
│  Matcher            │    │   Extraction Pipeline          │
│  Anchor lookup      │    │   BuildExtractionPrompt()      │
│  Fuzzy candidates   │    │   genkit.Generate() / GenData  │
│  Field scoring      │    │                                │
│  Threshold decision │    │                                │
│  Merge plan         │    │                                │
└────────┬───────────┘    └──────────────┬────────────────┘
         │                               │
         │  MatchDecision                │  extracted data
         │  (create/update/              │
         │   review/conflict)            │
         ▼                               │
┌────────────────────┐                   │
│  EntityStore        │◄─────────────────┘
│  BatchWrite()       │
│  GetData()          │
└─────────────────────┘
```

## Tips

- **Start with `extraction_prompt`** — a clear system prompt dramatically improves extraction quality. Be specific about the document type ("This is a recruitment email", "This is an invoice PDF").

- **Use `extraction_hint` for edge cases** — hints like "Format as ISO 8601 date" or "Extract the full legal name, not abbreviations" prevent common LLM mistakes.

- **Provide `examples`** — even 1-2 examples per field significantly improve extraction accuracy, especially for formatted values like phone numbers, dates, and IDs.

- **Use `extract: false`** for internal fields — fields like `crm_id` that are used for matching but shouldn't be extracted from documents.

- **Proto comments are descriptions** — well-commented protos get extraction descriptions for free. Only use the explicit `description` annotation when the LLM needs different wording.

- **Validate required fields** — anchor fields are marked `Required: true` in the schema. Check that these are present in the extraction result before storing.

- **Track provenance** — always record the source document, model ID, and confidence in the `ProvenanceEntry`. This creates an audit trail for debugging extraction quality.
