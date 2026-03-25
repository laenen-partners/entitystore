// This file demonstrates the matching package's pure domain logic:
// normalizers, similarity functions, tokenization, embedding computation,
// and match config inspection. These functions have no database dependency.
package examples

import (
	"context"
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
// 3. Computing embeddings
// ---------------------------------------------------------------------------

// simpleEmbedder is a mock Embedder for examples.
type simpleEmbedder struct{}

func (e *simpleEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	vecs := make([][]float32, len(texts))
	for i, text := range texts {
		fmt.Printf("Embedding text: %q\n", text)
		vecs[i] = make([]float32, 1536)
	}
	return vecs, nil
}

// ComputeEmbeddingExample shows the full embed pipeline using ComputeEmbedding.
// In practice, use the generated {Entity}EmbedText(msg) for the text extraction
// step and call your embedder directly.
func ComputeEmbeddingExample(ctx context.Context) {
	cfg := matching.EntityMatchConfig{
		EntityType:  "examples.v1.Person",
		EmbedFields: []string{"full_name", "job_title"},
	}

	// ComputeEmbedding extracts text from embed fields and calls the embedder.
	// Prefer using the generated PersonEmbedText(msg) + embedder.Embed() directly
	// for typed access without JSON round-trip.
	embedder := &simpleEmbedder{}

	data := []byte(`{"full_name": "Alice Johnson", "job_title": "Product Manager"}`)
	vec, err := matching.ComputeEmbedding(ctx, data, cfg, embedder)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	fmt.Printf("Got %d-dimensional vector\n", len(vec))
}

// ---------------------------------------------------------------------------
// 4. Match config inspection
// ---------------------------------------------------------------------------

// MatchConfigInspectionExample shows how to inspect a match config to
// understand an entity type's matching behaviour.
func MatchConfigInspectionExample() {
	// In a real project: cfg := personv1.PersonMatchConfig()
	cfg := matching.EntityMatchConfig{
		EntityType: "examples.v1.Person",
		Anchors: matching.AnchorConfig{
			SingleAnchors: []matching.AnchorField{
				{ProtoFieldName: "email", Normalizer: matching.NormalizeLowercaseTrim},
			},
			CompositeAnchors: [][]matching.AnchorField{
				{
					{ProtoFieldName: "full_name", Normalizer: matching.NormalizeLowercaseTrim},
					{ProtoFieldName: "date_of_birth"},
				},
			},
		},
		FieldWeights: []matching.FieldWeight{
			{ProtoFieldName: "email", Weight: 0.30, Similarity: matching.SimilarityExact},
			{ProtoFieldName: "full_name", Weight: 0.35, Similarity: matching.SimilarityJaroWinkler},
		},
		Thresholds:  matching.MatchThresholds{AutoMatch: 0.85, ReviewZone: 0.60},
		EmbedFields: []string{"email", "full_name"},
		TokenFields: []string{"full_name"},
	}

	fmt.Printf("Entity type: %s\n", cfg.EntityType)
	fmt.Printf("Thresholds: auto=%.2f, review=%.2f\n", cfg.Thresholds.AutoMatch, cfg.Thresholds.ReviewZone)
	fmt.Printf("Embed fields: %v\n", cfg.EmbedFields)
	fmt.Printf("Token fields: %v\n", cfg.TokenFields)

	for _, a := range cfg.Anchors.SingleAnchors {
		fmt.Printf("Anchor: %s\n", a.ProtoFieldName)
	}
	for _, fw := range cfg.FieldWeights {
		fmt.Printf("Weight: %s = %.2f (%s)\n", fw.ProtoFieldName, fw.Weight, fw.Similarity)
	}
}
