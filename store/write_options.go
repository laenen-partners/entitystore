package store

import "github.com/laenen-partners/entitystore/matching"

// WriteOpOption configures a WriteEntityOp built by generated code.
type WriteOpOption func(*WriteEntityOp)

// WithMatchedEntityID sets the entity ID for update and merge operations.
func WithMatchedEntityID(id string) WriteOpOption {
	return func(op *WriteEntityOp) { op.MatchedEntityID = id }
}

// WithConfidence sets the extraction confidence score.
func WithConfidence(c float64) WriteOpOption {
	return func(op *WriteEntityOp) { op.Confidence = c }
}

// WithTags sets the tags on the entity.
func WithTags(tags ...string) WriteOpOption {
	return func(op *WriteEntityOp) { op.Tags = tags }
}

// WithEmbedding sets the embedding vector.
func WithEmbedding(vec []float32) WriteOpOption {
	return func(op *WriteEntityOp) { op.Embedding = vec }
}

// WithID sets a client-generated UUID for idempotent creates.
func WithID(id string) WriteOpOption {
	return func(op *WriteEntityOp) { op.ID = id }
}

// WithProvenance sets the full provenance entry.
func WithProvenance(p matching.ProvenanceEntry) WriteOpOption {
	return func(op *WriteEntityOp) { op.Provenance = p }
}

// Provenance builds a ProvenanceEntry with sensible defaults.
// ExtractedAt is set to the current time. MatchMethod is derived from the
// write action: "create" for create, "update" for update, "merge" for merge.
func Provenance(sourceURN, modelID string) matching.ProvenanceEntry {
	return matching.ProvenanceEntry{
		SourceURN: sourceURN,
		ModelID:   modelID,
	}
}
