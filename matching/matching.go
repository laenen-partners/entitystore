// Package matching defines the entity store interface and all types used by
// the entity matching pipeline. It has zero external dependencies.
package matching

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"
	"unicode"
)

// ---------------------------------------------------------------------------
// Entity Store interface
// ---------------------------------------------------------------------------

// EntityStore abstracts persistence for the matching pipeline.
type EntityStore interface {
	FindByAnchors(ctx context.Context, entityType string, anchors []AnchorQuery, filter *QueryFilter) ([]StoredEntity, error)
	FindByTokens(ctx context.Context, entityType string, tokens []string, limit int, filter *QueryFilter) ([]StoredEntity, error)
	FindConnectedByType(ctx context.Context, entityID string, entityType string, relationTypes []string, filter *QueryFilter) ([]StoredEntity, error)
	FindEntitiesByRelation(ctx context.Context, entityType string, relationType string, filter *QueryFilter) ([]StoredEntity, error)
}

// AnchorQuery represents a single anchor lookup: a normalized field value that
// should uniquely (or near-uniquely) identify an entity.
type AnchorQuery struct {
	Field string `json:"field"`
	Value string `json:"value"`
}

// QueryFilter allows callers to narrow entity searches by tags.
type QueryFilter struct {
	Tags []string `json:"tags,omitempty"`
}

// ---------------------------------------------------------------------------
// EmbeddingStore interface (optional, extends EntityStore)
// ---------------------------------------------------------------------------

// EmbeddingStore extends EntityStore with vector similarity search.
type EmbeddingStore interface {
	EntityStore
	FindByEmbedding(ctx context.Context, entityType string, vec []float32, topK int, filter *QueryFilter) ([]StoredEntity, error)
	UpdateEmbedding(ctx context.Context, entityID string, vec []float32) error
}

// ---------------------------------------------------------------------------
// Stored entity types
// ---------------------------------------------------------------------------

// StoredEntity is a persisted entity record returned by the store.
type StoredEntity struct {
	ID         string          `json:"id"`
	EntityType string          `json:"entity_type"`
	Data       json.RawMessage `json:"data"`
	Confidence float64         `json:"confidence"`
	Tags       []string        `json:"tags"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

// ProvenanceEntry records the origin of an entity.
type ProvenanceEntry struct {
	ID              string    `json:"id"`
	EntityID        string    `json:"entity_id"`
	SourceURN      string    `json:"source_urn"`
	ExtractedAt     time.Time `json:"extracted_at"`
	ModelID         string    `json:"model_id"`
	Confidence      float64   `json:"confidence"`
	Fields          []string  `json:"fields"`
	MatchMethod     string    `json:"match_method"`
	MatchConfidence float64   `json:"match_confidence"`
}

// StoredRelation is a directed edge between two stored entities.
type StoredRelation struct {
	ID           string         `json:"id"`
	SourceID     string         `json:"source_id"`
	TargetID     string         `json:"target_id"`
	RelationType string         `json:"relation_type"`
	Confidence   float64        `json:"confidence"`
	Evidence     string         `json:"evidence"`
	Implied      bool           `json:"implied"`
	SourceURN   string         `json:"source_urn,omitempty"`
	Data         map[string]any `json:"data,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
}

// ---------------------------------------------------------------------------
// Match decision types
// ---------------------------------------------------------------------------

// MatchAction describes what the matcher decided to do with an extracted entity.
type MatchAction string

const (
	ActionCreate        MatchAction = "create"
	ActionUpdate        MatchAction = "update"
	ActionPartialUpdate MatchAction = "partial_update"
	ActionConflict      MatchAction = "conflict"
	ActionReview        MatchAction = "review"
)

// MatchDecision is the matcher's output for a single extracted entity.
type MatchDecision struct {
	EntityType      string          `json:"entity_type"`
	Data            json.RawMessage `json:"data"`
	Action          MatchAction     `json:"action"`
	MatchedRecordID string          `json:"matched_record_id,omitempty"`
	MatchConfidence float64         `json:"match_confidence"`
	MatchMethod     string          `json:"match_method"`
	MergePlan       []FieldMergeOp  `json:"merge_plan,omitempty"`
	Candidates      []MatchCandidate `json:"candidates,omitempty"`
}

// ---------------------------------------------------------------------------
// Merge types
// ---------------------------------------------------------------------------

// MergeOp describes what to do with a single field during entity merge.
type MergeOp string

const (
	MergeKeep     MergeOp = "keep"
	MergeWrite    MergeOp = "write"
	MergeConflict MergeOp = "conflict"
)

// FieldMergeOp is a per-field merge instruction.
type FieldMergeOp struct {
	Field          string  `json:"field"`
	Op             MergeOp `json:"op"`
	ExistingValue  any     `json:"existing_value"`
	ExtractedValue any     `json:"extracted_value"`
	Reason         string  `json:"reason"`
}

// ---------------------------------------------------------------------------
// Configuration types
// ---------------------------------------------------------------------------

// AnchorConfig defines which fields serve as identity anchors for an entity type.
type AnchorConfig struct {
	SingleAnchors    []AnchorField   `json:"single_anchors"`
	CompositeAnchors [][]AnchorField `json:"composite_anchors,omitempty"`
}

// AnchorField identifies a proto field that acts as an anchor.
type AnchorField struct {
	ProtoFieldName string              `json:"proto_field_name"`
	Normalizer     func(string) string `json:"-"`
}

// MatchThresholds controls the scoring boundaries for match decisions.
type MatchThresholds struct {
	AutoMatch  float64 `json:"auto_match"`
	ReviewZone float64 `json:"review_zone"`
}

// DefaultMatchThresholds returns sensible defaults.
func DefaultMatchThresholds() MatchThresholds {
	return MatchThresholds{
		AutoMatch:  0.85,
		ReviewZone: 0.60,
	}
}

// ConflictStrategy determines how to resolve a field-level conflict.
type ConflictStrategy string

const (
	ConflictFlagForReview ConflictStrategy = "flag_for_review"
	ConflictLatestWins    ConflictStrategy = "latest_wins"
	ConflictHighestConf   ConflictStrategy = "highest_confidence"
)

// FieldWeight assigns a similarity function and weight to a field for scoring.
type FieldWeight struct {
	ProtoFieldName string         `json:"proto_field_name"`
	Weight         float64        `json:"weight"`
	Similarity     SimilarityFunc `json:"similarity"`
}

// SimilarityFunc identifies a string similarity algorithm.
type SimilarityFunc string

const (
	SimilarityExact        SimilarityFunc = "exact"
	SimilarityJaroWinkler  SimilarityFunc = "jaro_winkler"
	SimilarityLevenshtein  SimilarityFunc = "levenshtein"
	SimilarityTokenJaccard SimilarityFunc = "token_jaccard"
)

// ---------------------------------------------------------------------------
// Entity Match Config (proto-annotation-derived)
// ---------------------------------------------------------------------------

// EntityMatchConfig bundles all config for one entity type.
type EntityMatchConfig struct {
	EntityType         string                         `json:"entity_type"`
	Anchors            AnchorConfig                   `json:"anchors"`
	FieldWeights       []FieldWeight                  `json:"field_weights"`
	ConflictStrategies map[string]ConflictStrategy    `json:"conflict_strategies"`
	Thresholds         MatchThresholds                `json:"thresholds"`
	EmbedFields        []string                       `json:"embed_fields"`
	TokenFields        []string                       `json:"token_fields"`
	AllowedRelations   []string                       `json:"allowed_relations,omitempty"`
	Normalizers        map[string]func(string) string `json:"-"`
}

// MatchConfigRegistry maps entity type names to their match configurations.
type MatchConfigRegistry struct {
	mu      sync.RWMutex
	configs map[string]EntityMatchConfig
}

// NewMatchConfigRegistry creates an empty registry.
func NewMatchConfigRegistry() *MatchConfigRegistry {
	return &MatchConfigRegistry{configs: make(map[string]EntityMatchConfig)}
}

// Register adds or replaces a configuration for the given entity type.
func (r *MatchConfigRegistry) Register(cfg EntityMatchConfig) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.configs[cfg.EntityType] = cfg
}

// Get returns the configuration for the given entity type, if registered.
func (r *MatchConfigRegistry) Get(entityType string) (EntityMatchConfig, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	cfg, ok := r.configs[entityType]
	return cfg, ok
}

// All returns a copy of all registered configurations.
func (r *MatchConfigRegistry) All() map[string]EntityMatchConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]EntityMatchConfig, len(r.configs))
	for k, v := range r.configs {
		out[k] = v
	}
	return out
}

// ---------------------------------------------------------------------------
// Built-in normalizers
// ---------------------------------------------------------------------------

// NormalizeLowercaseTrim lowercases and trims whitespace.
func NormalizeLowercaseTrim(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// NormalizePhone strips non-digit characters except a leading '+'.
func NormalizePhone(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	var b strings.Builder
	for i, r := range s {
		if r == '+' && i == 0 {
			b.WriteRune(r)
		} else if unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// Scoring types
// ---------------------------------------------------------------------------

// MatchCandidate is a stored entity that was considered as a potential match.
type MatchCandidate struct {
	Entity StoredEntity `json:"entity"`
	Score  float64      `json:"score"`
	Method string       `json:"method"`
}
