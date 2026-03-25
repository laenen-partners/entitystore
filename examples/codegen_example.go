// This file demonstrates how to use protoc-gen-entitystore generated code.
//
// The generated functions shown in comments below are produced by running
// `buf generate` on annotated proto messages. See proto/ for examples.
//
// In a real project you would import the generated package directly:
//
//	import personv1 "example.com/gen/persons/v1"
//
//	cfg := personv1.PersonMatchConfig()
//	tokens := personv1.PersonTokens(person)
//	text := personv1.PersonEmbedText(person)
//	op := personv1.PersonWriteOp(person, store.WriteActionCreate, ...)
package examples

import (
	"fmt"

	"github.com/laenen-partners/entitystore/extraction"
	"github.com/laenen-partners/entitystore/matching"
)

// ---------------------------------------------------------------------------
// 1. Registering generated configs
// ---------------------------------------------------------------------------

// RegisterConfigsExample shows how generated match configs and extraction
// schemas are registered into their respective registries.
func RegisterConfigsExample() {
	// Match configs — used by the Matcher for entity resolution.
	mcr := matching.NewMatchConfigRegistry()

	// In a real project, these are one-liners from generated code:
	//   mcr.Register(personv1.PersonMatchConfig())
	//   mcr.Register(invoicev1.InvoiceMatchConfig())
	//   mcr.Register(jobsv1.JobPostingMatchConfig())
	_ = mcr

	// Extraction schemas — used for LLM entity extraction prompts.
	esr := extraction.NewExtractionSchemaRegistry()

	// Same pattern:
	//   esr.Register(personv1.PersonExtractionSchema())
	//   esr.Register(invoicev1.InvoiceExtractionSchema())
	_ = esr
}

// ---------------------------------------------------------------------------
// 2. Using generated token and embed extractors
// ---------------------------------------------------------------------------

// TokensAndEmbedExample shows the generated typed extractors that replace
// the old reflection-based matching.BuildAnchors/BuildTokens/TextToEmbed.
func TokensAndEmbedExample() {
	// In a real project with generated code:
	//
	//   person := &personv1.Person{
	//       Email:    "alice@example.com",
	//       FullName: "Alice Johnson",
	//       JobTitle: "VP of Product",
	//   }
	//
	//   // Typed token extraction — no reflection, no config lookup.
	//   tokens := personv1.PersonTokens(person)
	//   // tokens = map[string][]string{
	//   //   "full_name": ["alice", "johnson"],
	//   //   "job_title": ["vp", "of", "product"],
	//   // }
	//
	//   // Typed embed text — deterministic field ordering from proto numbers.
	//   text := personv1.PersonEmbedText(person)
	//   // text = "alice@example.com Alice Johnson VP of Product"
	//
	//   // Pass to embedder:
	//   vecs, _ := embedder.Embed(ctx, []string{text})

	fmt.Println("See generated code for PersonTokens, PersonEmbedText usage")
}

// ---------------------------------------------------------------------------
// 3. Using generated WriteOp
// ---------------------------------------------------------------------------

// WriteOpExample shows how the generated WriteOp function replaces manual
// WriteEntityOp construction.
func WriteOpExample() {
	// In a real project with generated code:
	//
	//   person := &personv1.Person{
	//       Email:    "alice@example.com",
	//       FullName: "Alice Johnson",
	//   }
	//
	//   // Generated WriteOp — anchors, tokens wired automatically.
	//   op := personv1.PersonWriteOp(person, store.WriteActionCreate,
	//       store.WithTags("ws:acme", "active"),
	//   )
	//
	//   // Use in BatchWrite:
	//   results, _ := es.BatchWrite(ctx, []store.BatchWriteOp{
	//       {WriteEntity: op},
	//   })
	//
	//   // For updates, add the matched entity ID:
	//   op := personv1.PersonWriteOp(updated, store.WriteActionUpdate,
	//       store.WithMatchedEntityID(existingID),
	//       store.WithTags("active"),
	//   )

	fmt.Println("See generated code for PersonWriteOp usage")
}

// ---------------------------------------------------------------------------
// 4. Using extraction schemas for LLM prompts
// ---------------------------------------------------------------------------

// ExtractionSchemaExample shows how to use the generated extraction schema
// to build structured prompts for LLM entity extraction.
func ExtractionSchemaExample() {
	esr := extraction.NewExtractionSchemaRegistry()

	// In a real project:
	//   esr.Register(personv1.PersonExtractionSchema())
	//
	// For this example, register a manual schema:
	esr.Register(extraction.ExtractionSchema{
		EntityType:  "examples.v1.Person",
		DisplayName: "Person",
		Prompt:      "Extract person details from the provided text.",
		Fields: []extraction.ExtractionField{
			{Name: "email", Description: "Primary email address", Type: extraction.ExtractionFieldTypeString, Required: true},
			{Name: "full_name", Description: "Full legal name", Type: extraction.ExtractionFieldTypeString},
			{Name: "phone", Description: "Phone number", Type: extraction.ExtractionFieldTypeString},
		},
	})

	schema, ok := esr.Get("examples.v1.Person")
	if !ok {
		return
	}

	fmt.Printf("Entity: %s (%s)\n", schema.DisplayName, schema.EntityType)
	fmt.Printf("Prompt: %s\n", schema.Prompt)
	for _, f := range schema.Fields {
		fmt.Printf("  - %s (%s): %s\n", f.Name, f.Type, f.Description)
	}
}
