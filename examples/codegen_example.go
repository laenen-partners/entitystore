// Package examples demonstrates entitystore features through runnable examples.
//
// This file shows how to use the protoc-gen-entitystore code generator output:
// registering match configs, building anchors/tokens from extracted data,
// and using the extraction schema for LLM-based entity extraction.
//
// The generated functions (PersonMatchConfig, PersonExtractionSchema, etc.)
// are produced by running `buf generate` on annotated proto messages.
// See the proto/ directory for the annotated proto definitions.
package examples

import (
	"encoding/json"
	"fmt"

	"github.com/laenen-partners/entitystore/extraction"
	"github.com/laenen-partners/entitystore/matching"
)

// ---------------------------------------------------------------------------
// 1. Match Config Registry — registering generated configs
// ---------------------------------------------------------------------------

// SetupMatchConfigRegistry shows how to register generated match configs
// from multiple entity types into a single registry.
func SetupMatchConfigRegistry() *matching.MatchConfigRegistry {
	mcr := matching.NewMatchConfigRegistry()

	// In a real project, these come from generated code:
	//   mcr.Register(examplesv1.PersonMatchConfig())
	//   mcr.Register(examplesv1.InvoiceMatchConfig())
	//   mcr.Register(examplesv1.JobPostingMatchConfig())

	// For this example, we register a manually-constructed config
	// that mirrors what the generator produces.
	mcr.Register(matching.EntityMatchConfig{
		EntityType: "examples.v1.Person",
		Anchors: matching.AnchorConfig{
			SingleAnchors: []matching.AnchorField{
				{ProtoFieldName: "email", Normalizer: matching.NormalizeLowercaseTrim},
				{ProtoFieldName: "crm_id", Normalizer: nil},
			},
			CompositeAnchors: [][]matching.AnchorField{
				{
					{ProtoFieldName: "full_name", Normalizer: matching.NormalizeLowercaseTrim},
					{ProtoFieldName: "date_of_birth", Normalizer: nil},
				},
			},
		},
		FieldWeights: []matching.FieldWeight{
			{ProtoFieldName: "email", Weight: 0.30, Similarity: matching.SimilarityExact},
			{ProtoFieldName: "full_name", Weight: 0.35, Similarity: matching.SimilarityJaroWinkler},
			{ProtoFieldName: "phone", Weight: 0.15, Similarity: matching.SimilarityExact},
			{ProtoFieldName: "date_of_birth", Weight: 0.20, Similarity: matching.SimilarityExact},
		},
		ConflictStrategies: map[string]matching.ConflictStrategy{
			"email":         matching.ConflictFlagForReview,
			"full_name":     matching.ConflictLatestWins,
			"phone":         matching.ConflictLatestWins,
			"date_of_birth": matching.ConflictHighestConf,
		},
		Thresholds: matching.MatchThresholds{
			AutoMatch:  0.85,
			ReviewZone: 0.60,
		},
		EmbedFields: []string{"email", "full_name", "job_title"},
		TokenFields: []string{"full_name", "job_title"},
		AllowedRelations: []string{"works_at", "knows", "same_as"},
		Normalizers: map[string]func(string) string{
			"email":     matching.NormalizeLowercaseTrim,
			"full_name": matching.NormalizeLowercaseTrim,
			"phone":     matching.NormalizePhone,
		},
	})

	return mcr
}

// ---------------------------------------------------------------------------
// 2. Building anchors and tokens from extracted data
// ---------------------------------------------------------------------------

// BuildAnchorsAndTokensExample shows how to use the matching package to
// prepare entity data for storage after extraction.
func BuildAnchorsAndTokensExample() {
	mcr := SetupMatchConfigRegistry()
	cfg, _ := mcr.Get("examples.v1.Person")

	// Simulated extracted entity data (e.g., from an LLM or form submission).
	data := json.RawMessage(`{
		"email": "  John.Doe@Example.COM  ",
		"full_name": "John Michael Doe",
		"phone": "(555) 867-5309",
		"date_of_birth": "1990-05-15",
		"job_title": "Senior Software Engineer"
	}`)

	// BuildAnchors applies normalizers and returns anchor queries for dedup lookup.
	anchors := matching.BuildAnchors(data, cfg)
	fmt.Println("Anchors for dedup lookup:")
	for _, a := range anchors {
		fmt.Printf("  %s = %q\n", a.Field, a.Value)
	}
	// Output:
	//   email = "john.doe@example.com"      (lowercased + trimmed)
	//   full_name+date_of_birth = composite  (composite anchor)

	// BuildTokens tokenizes fields for blocking/candidate retrieval.
	tokens := matching.BuildTokens(data, cfg)
	fmt.Println("Tokens for blocking:")
	for field, toks := range tokens {
		fmt.Printf("  %s: %v\n", field, toks)
	}
	// Output:
	//   full_name: [john michael doe]
	//   job_title: [senior software engineer]

	// ExtractEmbedText concatenates embed fields for embedding input.
	embedText := matching.TextToEmbed(data, cfg.EmbedFields)
	fmt.Printf("Embed input text: %q\n", embedText)
	// Output: "john.doe@example.com John Michael Doe Senior Software Engineer"

	// NormalizeField applies the configured normalizer for a specific field.
	normalizedPhone := matching.NormalizeField("(555) 867-5309", "phone", cfg)
	fmt.Printf("Normalized phone: %q\n", normalizedPhone)
	// Output: "5558675309"
}

// ---------------------------------------------------------------------------
// 3. Extraction Schema Registry — registering generated schemas
// ---------------------------------------------------------------------------

// SetupExtractionSchemaRegistry shows how to register generated extraction
// schemas for use with an LLM extraction framework (e.g., Genkit).
func SetupExtractionSchemaRegistry() *extraction.ExtractionSchemaRegistry {
	esr := extraction.NewExtractionSchemaRegistry()

	// In a real project, these come from generated code:
	//   esr.Register(examplesv1.PersonExtractionSchema())
	//   esr.Register(examplesv1.InvoiceExtractionSchema())
	//   esr.Register(examplesv1.JobPostingExtractionSchema())

	// For this example, we register a manually-constructed schema
	// that mirrors what the generator produces.
	esr.Register(extraction.ExtractionSchema{
		EntityType:   "examples.v1.Person",
		DisplayName:  "Person",
		Prompt:       "Extract person details from the provided text.",
		Instructions: "If multiple people are mentioned, extract only the primary subject. Ignore quoted or referenced individuals.",
		Fields: []extraction.ExtractionField{
			{
				Name:        "email",
				Description: "Primary email address",
				Hint:        "Extract the primary email, not CC or forwarded addresses",
				Type:        extraction.ExtractionFieldTypeString,
				Required:    true, // anchor field
				Examples:    []string{"john.doe@example.com", "jane@company.org"},
			},
			{
				Name:        "full_name",
				Description: "Full legal name of the person",
				Hint:        "Use the full name as written, including middle names if present",
				Type:        extraction.ExtractionFieldTypeString,
				Examples:    []string{"John Michael Doe", "Jane Smith-Williams"},
			},
			{
				Name:        "phone",
				Description: "Phone number in E.164 format",
				Hint:        "Include country code if available, e.g. +1 for US numbers",
				Type:        extraction.ExtractionFieldTypeString,
				Examples:    []string{"+1-555-867-5309", "+44 20 7946 0958"},
			},
			{
				Name:        "date_of_birth",
				Description: "Date of birth in ISO 8601 format (YYYY-MM-DD)",
				Hint:        "Format as YYYY-MM-DD",
				Type:        extraction.ExtractionFieldTypeString,
				Examples:    []string{"1990-05-15", "1985-12-01"},
			},
			{
				Name:        "job_title",
				Description: "Current job title or role",
				Type:        extraction.ExtractionFieldTypeString,
			},
			// Note: crm_id has extract: false, so it's excluded.
			{
				Name:        "notes",
				Description: "notes", // humanized from field name (no comment, no annotation)
				Type:        extraction.ExtractionFieldTypeString,
			},
		},
	})

	return esr
}

// UseExtractionSchemaExample shows how to use the extraction schema
// to build a prompt or structured schema for an LLM extraction call.
func UseExtractionSchemaExample() {
	esr := SetupExtractionSchemaRegistry()
	schema, ok := esr.Get("examples.v1.Person")
	if !ok {
		return
	}

	// The schema contains everything needed for an LLM extraction call:
	fmt.Printf("Entity: %s (%s)\n", schema.DisplayName, schema.EntityType)
	fmt.Printf("Prompt: %s\n", schema.Prompt)
	fmt.Printf("Instructions: %s\n", schema.Instructions)
	fmt.Printf("Fields (%d):\n", len(schema.Fields))
	for _, f := range schema.Fields {
		required := ""
		if f.Required {
			required = " [REQUIRED]"
		}
		fmt.Printf("  - %s (%s)%s: %s\n", f.Name, f.Type, required, f.Description)
		if f.Hint != "" {
			fmt.Printf("    Hint: %s\n", f.Hint)
		}
		if len(f.Examples) > 0 {
			fmt.Printf("    Examples: %v\n", f.Examples)
		}
	}

	// In a real project, you'd convert this to your LLM framework's format.
	// For example, with Genkit (out of scope for this library):
	//
	//   req := toGenkitSchema(schema)
	//   resp, _ := genkit.Generate(ctx, req, document)
}
