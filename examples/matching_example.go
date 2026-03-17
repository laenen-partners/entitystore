// This file demonstrates the matching package's pure domain logic:
// normalizers, similarity functions, embedding helpers, and match
// config inspection. These functions have no database dependency.
package examples

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/laenen-partners/entitystore/matching"
)

// ---------------------------------------------------------------------------
// 1. Normalizers
// ---------------------------------------------------------------------------

// NormalizersExample shows the built-in normalizer functions.
func NormalizersExample() {
	// LowercaseTrim — suitable for names, emails, addresses.
	fmt.Println(matching.NormalizeLowercaseTrim("  John.Doe@Example.COM  "))
	// Output: "john.doe@example.com"

	fmt.Println(matching.NormalizeLowercaseTrim("  Alice M. JOHNSON  "))
	// Output: "alice m. johnson"

	// PhoneNormalize — strips non-digit characters except leading '+'.
	fmt.Println(matching.NormalizePhone("+1 (555) 867-5309"))
	// Output: "+15558675309"

	fmt.Println(matching.NormalizePhone("555.867.5309"))
	// Output: "5558675309"
}

// ---------------------------------------------------------------------------
// 2. Tokenization
// ---------------------------------------------------------------------------

// TokenizationExample shows how text is split into tokens for blocking.
func TokenizationExample() {
	tokens := matching.Tokenize("Senior Software Engineer at Acme Corp")
	fmt.Println(tokens)
	// Output: [senior software engineer at acme corp]

	tokens = matching.Tokenize("John Michael Doe")
	fmt.Println(tokens)
	// Output: [john michael doe]
}

// ---------------------------------------------------------------------------
// 3. Embedding text extraction
// ---------------------------------------------------------------------------

// EmbeddingExtractionExample shows how embed fields are concatenated.
func EmbeddingExtractionExample() {
	data := json.RawMessage(`{
		"email": "alice@example.com",
		"full_name": "Alice Johnson",
		"job_title": "Product Manager",
		"phone": "+1-555-123-4567"
	}`)

	// Only fields listed as embed fields are included.
	text := matching.ExtractEmbedText(data, []string{"full_name", "job_title"})
	fmt.Println(text)
	// Output: "Alice Johnson Product Manager"

	// All embed fields.
	text = matching.ExtractEmbedText(data, []string{"email", "full_name", "job_title"})
	fmt.Println(text)
	// Output: "alice@example.com Alice Johnson Product Manager"
}

// ---------------------------------------------------------------------------
// 4. Computing embeddings
// ---------------------------------------------------------------------------

// ComputeEmbeddingExample shows the full embed pipeline: extract text,
// call embedder, get vector.
func ComputeEmbeddingExample(ctx context.Context) {
	cfg := matching.EntityMatchConfig{
		EntityType:  "examples.v1.Person",
		EmbedFields: []string{"full_name", "job_title"},
	}

	data := json.RawMessage(`{
		"full_name": "Alice Johnson",
		"job_title": "Product Manager"
	}`)

	// Define an embedder function (in practice, calls an embedding API).
	embedder := func(_ context.Context, text string) ([]float32, error) {
		fmt.Printf("Embedding text: %q\n", text)
		// Return a dummy vector.
		return make([]float32, 1536), nil
	}

	vec, err := matching.ComputeEmbedding(ctx, data, cfg, embedder)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	fmt.Printf("Got %d-dimensional vector\n", len(vec))
	// Output:
	//   Embedding text: "Alice Johnson Product Manager"
	//   Got 1536-dimensional vector
}

// ---------------------------------------------------------------------------
// 5. Building anchors from entity data
// ---------------------------------------------------------------------------

// BuildAnchorsExample shows how anchors are extracted and normalized
// from raw entity data using the match config.
func BuildAnchorsExample() {
	cfg := matching.EntityMatchConfig{
		EntityType: "examples.v1.Person",
		Anchors: matching.AnchorConfig{
			SingleAnchors: []matching.AnchorField{
				{ProtoFieldName: "email", Normalizer: matching.NormalizeLowercaseTrim},
			},
		},
		Normalizers: map[string]func(string) string{
			"email": matching.NormalizeLowercaseTrim,
		},
	}

	data := json.RawMessage(`{"email": "  ALICE@Example.COM  ", "full_name": "Alice"}`)

	anchors := matching.BuildAnchors(data, cfg)
	for _, a := range anchors {
		fmt.Printf("Anchor: %s = %q\n", a.Field, a.Value)
	}
	// Output: Anchor: email = "alice@example.com"
}

// ---------------------------------------------------------------------------
// 6. Building tokens from entity data
// ---------------------------------------------------------------------------

// BuildTokensExample shows how token fields are extracted and tokenized.
func BuildTokensExample() {
	cfg := matching.EntityMatchConfig{
		EntityType:  "examples.v1.Person",
		TokenFields: []string{"full_name", "job_title"},
	}

	data := json.RawMessage(`{
		"full_name": "Alice Marie Johnson",
		"job_title": "Senior Product Manager",
		"email": "alice@example.com"
	}`)

	tokens := matching.BuildTokens(data, cfg)
	for field, toks := range tokens {
		fmt.Printf("  %s: %v\n", field, toks)
	}
	// Output:
	//   full_name: [alice marie johnson]
	//   job_title: [senior product manager]
}

// ---------------------------------------------------------------------------
// 7. Match config inspection
// ---------------------------------------------------------------------------

// MatchConfigInspectionExample shows how to inspect a registered config
// to understand an entity type's matching behaviour.
func MatchConfigInspectionExample() {
	mcr := SetupMatchConfigRegistry() // from codegen_example.go

	cfg, ok := mcr.Get("examples.v1.Person")
	if !ok {
		fmt.Println("Config not found")
		return
	}

	fmt.Printf("Entity type: %s\n", cfg.EntityType)

	fmt.Println("Single anchors:")
	for _, a := range cfg.Anchors.SingleAnchors {
		fmt.Printf("  - %s\n", a.ProtoFieldName)
	}

	fmt.Println("Composite anchors:")
	for _, ca := range cfg.Anchors.CompositeAnchors {
		names := make([]string, len(ca))
		for i, a := range ca {
			names[i] = a.ProtoFieldName
		}
		fmt.Printf("  - %v\n", names)
	}

	fmt.Println("Field weights:")
	for _, fw := range cfg.FieldWeights {
		fmt.Printf("  - %s: weight=%.2f, similarity=%s\n",
			fw.ProtoFieldName, fw.Weight, fw.Similarity)
	}

	fmt.Printf("Thresholds: auto_match=%.2f, review_zone=%.2f\n",
		cfg.Thresholds.AutoMatch, cfg.Thresholds.ReviewZone)

	fmt.Printf("Embed fields: %v\n", cfg.EmbedFields)
	fmt.Printf("Token fields: %v\n", cfg.TokenFields)
	fmt.Printf("Allowed relations: %v\n", cfg.AllowedRelations)
}
