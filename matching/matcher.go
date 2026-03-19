package matching

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
)

// ---------------------------------------------------------------------------
// Matcher options
// ---------------------------------------------------------------------------

// MatcherOption configures a Matcher.
type MatcherOption func(*matcherOptions)

type matcherOptions struct {
	embedder      EmbedderFunc
	tokenLimit    int
	embeddingTopK int
}

// WithEmbedder sets the embedding function for vector similarity retrieval.
func WithEmbedder(fn EmbedderFunc) MatcherOption {
	return func(o *matcherOptions) { o.embedder = fn }
}

// WithTokenLimit sets the maximum number of candidates from token search (default 20).
func WithTokenLimit(n int) MatcherOption {
	return func(o *matcherOptions) { o.tokenLimit = n }
}

// WithEmbeddingTopK sets the maximum candidates from embedding search (default 10).
func WithEmbeddingTopK(n int) MatcherOption {
	return func(o *matcherOptions) { o.embeddingTopK = n }
}

// ---------------------------------------------------------------------------
// Matcher
// ---------------------------------------------------------------------------

// Matcher orchestrates entity matching using an EntityMatchConfig.
// It retrieves candidates, scores them field-by-field, applies thresholds,
// and returns a MatchDecision.
type Matcher struct {
	config        EntityMatchConfig
	store         EntityStore
	embedder      EmbedderFunc
	tokenLimit    int
	embeddingTopK int
}

// NewMatcher creates a Matcher for the given entity type config.
func NewMatcher(config EntityMatchConfig, store EntityStore, opts ...MatcherOption) *Matcher {
	o := matcherOptions{
		tokenLimit:    20,
		embeddingTopK: 10,
	}
	for _, opt := range opts {
		opt(&o)
	}
	return &Matcher{
		config:        config,
		store:         store,
		embedder:      o.embedder,
		tokenLimit:    o.tokenLimit,
		embeddingTopK: o.embeddingTopK,
	}
}

// Match takes extracted entity data and returns a match decision.
func (m *Matcher) Match(ctx context.Context, data json.RawMessage) (*MatchDecision, error) {
	entityType := m.config.EntityType

	// --- Anchor short-circuit ---
	anchors := BuildAnchors(data, m.config)
	if len(anchors) > 0 {
		anchorMatches, err := m.store.FindByAnchors(ctx, entityType, anchors, nil)
		if err != nil {
			return nil, fmt.Errorf("anchor lookup: %w", err)
		}
		if len(anchorMatches) == 1 {
			mergePlan := m.buildMergePlan(data, anchorMatches[0].Data)
			action := ActionUpdate
			if hasConflictOps(mergePlan) {
				action = ActionConflict
			}
			return &MatchDecision{
				EntityType:      entityType,
				Data:            data,
				Action:          action,
				MatchedRecordID: anchorMatches[0].ID,
				MatchConfidence: 1.0,
				MatchMethod:     "anchor",
				MergePlan:       mergePlan,
				Candidates:      toMatchCandidates(anchorMatches, 1.0, "anchor"),
			}, nil
		}
		if len(anchorMatches) > 1 {
			return &MatchDecision{
				EntityType:  entityType,
				Data:        data,
				Action:      ActionConflict,
				MatchMethod: "anchor",
				Candidates:  toMatchCandidates(anchorMatches, 1.0, "anchor"),
			}, nil
		}
	}

	// --- Fuzzy candidate retrieval ---
	candidates, err := m.retrieveFuzzyCandidates(ctx, entityType, data)
	if err != nil {
		return nil, fmt.Errorf("candidate retrieval: %w", err)
	}

	if len(candidates) == 0 {
		return &MatchDecision{
			EntityType:  entityType,
			Data:        data,
			Action:      ActionCreate,
			MatchMethod: "none",
		}, nil
	}

	// --- Score candidates ---
	type scored struct {
		entity StoredEntity
		score  float64
	}
	var scoredCandidates []scored
	for _, c := range candidates {
		score := m.scoreCandidate(data, c.Data)
		scoredCandidates = append(scoredCandidates, scored{entity: c, score: score})
	}

	// Sort by score descending.
	sort.Slice(scoredCandidates, func(i, j int) bool {
		return scoredCandidates[i].score > scoredCandidates[j].score
	})

	best := scoredCandidates[0]

	// Build candidate list for the decision.
	matchCandidates := make([]MatchCandidate, len(scoredCandidates))
	for i, sc := range scoredCandidates {
		matchCandidates[i] = MatchCandidate{
			Entity: sc.entity,
			Score:  sc.score,
			Method: "composite",
		}
	}

	// --- Apply thresholds ---
	thresholds := m.config.Thresholds

	if best.score < thresholds.ReviewZone {
		return &MatchDecision{
			EntityType:  entityType,
			Data:        data,
			Action:      ActionCreate,
			MatchMethod: "composite",
			Candidates:  matchCandidates,
		}, nil
	}

	if best.score >= thresholds.AutoMatch {
		mergePlan := m.buildMergePlan(data, best.entity.Data)
		action := ActionUpdate
		if hasConflictOps(mergePlan) {
			action = ActionConflict
		}
		return &MatchDecision{
			EntityType:      entityType,
			Data:            data,
			Action:          action,
			MatchedRecordID: best.entity.ID,
			MatchConfidence: best.score,
			MatchMethod:     "composite",
			MergePlan:       mergePlan,
			Candidates:      matchCandidates,
		}, nil
	}

	// Review zone.
	return &MatchDecision{
		EntityType:      entityType,
		Data:            data,
		Action:          ActionReview,
		MatchedRecordID: best.entity.ID,
		MatchConfidence: best.score,
		MatchMethod:     "composite",
		Candidates:      matchCandidates,
	}, nil
}

// ---------------------------------------------------------------------------
// Candidate retrieval (fuzzy)
// ---------------------------------------------------------------------------

func (m *Matcher) retrieveFuzzyCandidates(ctx context.Context, entityType string, data json.RawMessage) ([]StoredEntity, error) {
	seen := make(map[string]struct{})
	var result []StoredEntity

	addUnique := func(entities []StoredEntity) {
		for _, e := range entities {
			if _, ok := seen[e.ID]; ok {
				continue
			}
			seen[e.ID] = struct{}{}
			result = append(result, e)
		}
	}

	// Token-based retrieval.
	tokens := BuildTokens(data, m.config)
	if tokens != nil {
		flat := flattenTokens(tokens)
		if len(flat) > 0 {
			found, err := m.store.FindByTokens(ctx, entityType, flat, m.tokenLimit, nil)
			if err != nil {
				return nil, fmt.Errorf("token lookup: %w", err)
			}
			addUnique(found)
		}
	}

	// Embedding-based retrieval.
	if embStore, ok := m.store.(EmbeddingStore); ok && m.embedder != nil {
		vec, err := ComputeEmbedding(ctx, data, m.config, m.embedder)
		if err != nil {
			return nil, fmt.Errorf("compute embedding: %w", err)
		}
		if vec != nil {
			found, err := embStore.FindByEmbedding(ctx, entityType, vec, m.embeddingTopK, nil)
			if err != nil {
				return nil, fmt.Errorf("embedding lookup: %w", err)
			}
			addUnique(found)
		}
	}

	return result, nil
}

// flattenTokens merges all token field values into a single slice.
func flattenTokens(tokens map[string][]string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, toks := range tokens {
		for _, t := range toks {
			if _, ok := seen[t]; ok {
				continue
			}
			seen[t] = struct{}{}
			out = append(out, t)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Scoring
// ---------------------------------------------------------------------------

// scoreCandidate computes a composite similarity score between extracted data
// and a candidate entity, using the configured field weights and similarity functions.
func (m *Matcher) scoreCandidate(extracted, candidate json.RawMessage) float64 {
	extractedFields := extractFields(extracted)
	candidateFields := extractFields(candidate)

	var totalScore float64
	for _, fw := range m.config.FieldWeights {
		if fw.Weight <= 0 {
			continue
		}

		ev, _ := fieldValue(extractedFields, fw.ProtoFieldName)
		cv, _ := fieldValue(candidateFields, fw.ProtoFieldName)

		// Apply normalizers before comparison.
		ev = NormalizeField(ev, fw.ProtoFieldName, m.config)
		cv = NormalizeField(cv, fw.ProtoFieldName, m.config)

		score := computeSimilarity(fw.Similarity, ev, cv)
		totalScore += score * fw.Weight
	}

	return totalScore
}

// ---------------------------------------------------------------------------
// Merge plan
// ---------------------------------------------------------------------------

// buildMergePlan produces a per-field merge plan for updating an existing entity.
func (m *Matcher) buildMergePlan(extracted, existing json.RawMessage) []FieldMergeOp {
	extractedFields := extractFields(extracted)
	existingFields := extractFields(existing)

	var plan []FieldMergeOp

	// Consider all fields that have weights or conflict strategies.
	fieldSet := make(map[string]struct{})
	for _, fw := range m.config.FieldWeights {
		fieldSet[fw.ProtoFieldName] = struct{}{}
	}
	for f := range m.config.ConflictStrategies {
		fieldSet[f] = struct{}{}
	}

	for field := range fieldSet {
		ev, _ := fieldValue(extractedFields, field)
		xv, _ := fieldValue(existingFields, field)

		if ev == "" {
			plan = append(plan, FieldMergeOp{
				Field: field, Op: MergeKeep,
				ExistingValue: xv, ExtractedValue: ev,
				Reason: "extracted value is empty",
			})
			continue
		}

		// Normalize for comparison.
		evNorm := NormalizeField(ev, field, m.config)
		xvNorm := NormalizeField(xv, field, m.config)

		if evNorm == xvNorm {
			plan = append(plan, FieldMergeOp{
				Field: field, Op: MergeKeep,
				ExistingValue: xv, ExtractedValue: ev,
				Reason: "values match after normalization",
			})
			continue
		}

		if xv == "" {
			plan = append(plan, FieldMergeOp{
				Field: field, Op: MergeWrite,
				ExistingValue: xv, ExtractedValue: ev,
				Reason: "new value fills empty field",
			})
			continue
		}

		// Values differ — apply conflict strategy.
		strategy := m.config.ConflictStrategies[field]
		switch strategy {
		case ConflictLatestWins:
			plan = append(plan, FieldMergeOp{
				Field: field, Op: MergeWrite,
				ExistingValue: xv, ExtractedValue: ev,
				Reason: "latest_wins strategy",
			})
		case ConflictHighestConf:
			plan = append(plan, FieldMergeOp{
				Field: field, Op: MergeConflict,
				ExistingValue: xv, ExtractedValue: ev,
				Reason: "highest_confidence — requires confidence comparison",
			})
		case ConflictFlagForReview:
			plan = append(plan, FieldMergeOp{
				Field: field, Op: MergeConflict,
				ExistingValue: xv, ExtractedValue: ev,
				Reason: "flag_for_review — human decision needed",
			})
		default:
			plan = append(plan, FieldMergeOp{
				Field: field, Op: MergeWrite,
				ExistingValue: xv, ExtractedValue: ev,
				Reason: "no conflict strategy — defaulting to write",
			})
		}
	}

	// Sort for deterministic output.
	sort.Slice(plan, func(i, j int) bool {
		return plan[i].Field < plan[j].Field
	})

	return plan
}

// hasConflictOps returns true if any merge op is a conflict.
func hasConflictOps(plan []FieldMergeOp) bool {
	for _, op := range plan {
		if op.Op == MergeConflict {
			return true
		}
	}
	return false
}

// toMatchCandidates wraps stored entities as MatchCandidates.
func toMatchCandidates(entities []StoredEntity, score float64, method string) []MatchCandidate {
	candidates := make([]MatchCandidate, len(entities))
	for i, e := range entities {
		candidates[i] = MatchCandidate{Entity: e, Score: score, Method: method}
	}
	return candidates
}

// ---------------------------------------------------------------------------
// MatcherRegistry
// ---------------------------------------------------------------------------

// MatcherRegistry holds Matchers indexed by entity type.
type MatcherRegistry struct {
	matchers map[string]*Matcher
}

// NewMatcherRegistry creates an empty registry.
func NewMatcherRegistry() *MatcherRegistry {
	return &MatcherRegistry{matchers: make(map[string]*Matcher)}
}

// Register adds a Matcher for the given entity type.
func (r *MatcherRegistry) Register(m *Matcher) {
	r.matchers[m.config.EntityType] = m
}

// Get returns the Matcher for the given entity type, if registered.
func (r *MatcherRegistry) Get(entityType string) (*Matcher, bool) {
	m, ok := r.matchers[entityType]
	return m, ok
}

// Match finds the matcher for the given entity type and runs matching.
func (r *MatcherRegistry) Match(ctx context.Context, entityType string, data json.RawMessage) (*MatchDecision, error) {
	m, ok := r.matchers[entityType]
	if !ok {
		return nil, fmt.Errorf("no matcher registered for %q", entityType)
	}
	return m.Match(ctx, data)
}
