package store

import "google.golang.org/protobuf/proto"

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

// WithVersion sets the expected version for optimistic locking on updates/merges.
func WithVersion(v int) WriteOpOption {
	return func(op *WriteEntityOp) { op.Version = v }
}

// WithDisplayName sets the display name for the entity.
func WithDisplayName(name string) WriteOpOption {
	return func(op *WriteEntityOp) { op.DisplayName = name }
}

// WithEvents appends caller-defined events to the operation.
// These are inserted alongside the standard lifecycle events in the same transaction.
func WithEvents(events ...proto.Message) WriteOpOption {
	return func(op *WriteEntityOp) { op.Events = append(op.Events, events...) }
}
