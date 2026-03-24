# ADR-002: Matching Engine — Scoring and Match Decision Pipeline

**Status:** Implemented (v0.9.0)
**Date:** 2026-03-19
**Author:** Pascal Laenen

## Context

EntityStore currently provides the building blocks for entity resolution:

- **Configuration** — `EntityMatchConfig` defines anchors, field weights, similarity functions, thresholds, and conflict strategies per entity type.
- **Candidate retrieval** — `FindByAnchors`, `FindByTokens`, and `FindByEmbedding` retrieve potential matches from the store.
- **Data preparation** — `BuildAnchors`, `BuildTokens`, `ExtractEmbedText`, and `NormalizeField` prepare extracted data for storage and lookup.
- **Storage** — `BatchWrite` persists entities with anchors, tokens, embeddings, and provenance.
- **Decision types** — `MatchDecision`, `MatchAction`, `MatchCandidate`, `FieldMergeOp` are defined but **nothing produces them**.

The critical missing piece is the **scoring engine**: the component that takes an extracted entity + a list of candidates and produces a `MatchDecision` (create, update, merge, review, or conflict). This is the core of entity resolution and where systems like Senzing, Zingg, and Splink differentiate.

Without this, every consumer of EntityStore must implement their own scoring loop, which is error-prone and defeats the purpose of having declarative match configs.

## Decision

Implement a `Matcher` in the `matching` package that:

1. Orchestrates the full candidate retrieval → scoring → decision pipeline
2. Uses the declarative `EntityMatchConfig` to drive all behaviour
3. Returns `MatchDecision` with full transparency (scores, candidates, merge plan)
4. Remains a pure library with no database dependency (receives candidates via the `EntityStore` interface)

## Design

### Core type: `Matcher`

```go
// Package: matching

// Matcher orchestrates entity matching using an EntityMatchConfig.
type Matcher struct {
    config   EntityMatchConfig
    store    EntityStore       // interface — no DB dependency
    embedder EmbedderFunc      // optional — for embedding-based retrieval
}

// NewMatcher creates a Matcher for the given entity type config.
func NewMatcher(config EntityMatchConfig, store EntityStore, opts ...MatcherOption) *Matcher

// Match takes extracted entity data and returns a match decision.
func (m *Matcher) Match(ctx context.Context, data json.RawMessage) (*MatchDecision, error)
```

### Match pipeline (inside `Matcher.Match`)

```
                    ┌─────────────────┐
                    │ Extracted Entity │
                    │ (json.RawMessage)│
                    └────────┬────────┘
                             │
              ┌──────────────┼──────────────┐
              ▼              ▼              ▼
        ┌──────────┐  ┌───────────┐  ┌──────────────┐
        │ Anchor   │  │ Token     │  │ Embedding    │
        │ Lookup   │  │ Overlap   │  │ Similarity   │
        └────┬─────┘  └─────┬─────┘  └──────┬───────┘
             │              │               │
             └──────────────┼───────────────┘
                            ▼
                   ┌─────────────────┐
                   │ Deduplicate     │
                   │ Candidates      │
                   └────────┬────────┘
                            ▼
                   ┌─────────────────┐
                   │ Score Each      │
                   │ Candidate       │
                   │ (field-by-field)│
                   └────────┬────────┘
                            ▼
                   ┌─────────────────┐
                   │ Apply           │
                   │ Thresholds      │
                   └────────┬────────┘
                            ▼
                   ┌─────────────────┐
                   │ Build           │
                   │ Merge Plan      │
                   │ (if updating)   │
                   └────────┬────────┘
                            ▼
                   ┌─────────────────┐
                   │ MatchDecision   │
                   └─────────────────┘
```

### Step 1: Candidate retrieval

Retrieve candidates from all three channels in parallel:

```go
func (m *Matcher) retrieveCandidates(ctx context.Context, data json.RawMessage) ([]StoredEntity, error) {
    anchors := BuildAnchors(data, m.config)
    tokens := BuildTokens(data, m.config)

    // Parallel retrieval from all channels.
    anchorResults, _ := m.store.FindByAnchors(ctx, m.config.EntityType, anchors, nil)
    tokenResults, _ := m.store.FindByTokens(ctx, m.config.EntityType, flattenTokens(tokens), tokenLimit, nil)

    // Embedding retrieval (optional).
    var embeddingResults []StoredEntity
    if embStore, ok := m.store.(EmbeddingStore); ok && m.embedder != nil {
        vec, _ := ComputeEmbedding(ctx, data, m.config, m.embedder)
        if vec != nil {
            embeddingResults, _ = embStore.FindByEmbedding(ctx, m.config.EntityType, vec, embeddingTopK, nil)
        }
    }

    // Deduplicate by entity ID.
    return dedup(anchorResults, tokenResults, embeddingResults), nil
}
```

### Step 2: Field-level scoring

For each candidate, compute a composite score using the configured similarity functions and weights:

```go
type fieldScore struct {
    Field      string
    Similarity SimilarityFunc
    Weight     float64
    Score      float64  // 0.0–1.0 similarity for this field
}

func (m *Matcher) scoreCandidate(extracted, candidate json.RawMessage) (float64, []fieldScore) {
    var totalScore float64
    var scores []fieldScore

    for _, fw := range m.config.FieldWeights {
        extractedVal := extractField(extracted, fw.ProtoFieldName)
        candidateVal := extractField(candidate, fw.ProtoFieldName)

        // Apply normalizer before comparison.
        extractedVal = NormalizeField(extractedVal, fw.ProtoFieldName, m.config)
        candidateVal = NormalizeField(candidateVal, fw.ProtoFieldName, m.config)

        score := computeSimilarity(fw.Similarity, extractedVal, candidateVal)
        totalScore += score * fw.Weight

        scores = append(scores, fieldScore{
            Field: fw.ProtoFieldName, Similarity: fw.Similarity,
            Weight: fw.Weight, Score: score,
        })
    }

    return totalScore, scores
}
```

### Step 3: Similarity functions

Implement the four declared similarity functions:

```go
func computeSimilarity(fn SimilarityFunc, a, b string) float64 {
    if a == "" || b == "" {
        return 0.0
    }
    switch fn {
    case SimilarityExact:
        if a == b { return 1.0 }
        return 0.0
    case SimilarityJaroWinkler:
        return jaroWinkler(a, b)
    case SimilarityLevenshtein:
        return normalizedLevenshtein(a, b)
    case SimilarityTokenJaccard:
        return tokenJaccard(a, b)
    default:
        return 0.0
    }
}
```

**Implementation options for string similarity:**

| Algorithm | Option A: Implement in-house | Option B: Use existing library |
|---|---|---|
| Jaro-Winkler | ~80 lines of Go | `github.com/xrash/smetrics` or `github.com/adrg/strutil` |
| Levenshtein (normalized) | ~40 lines of Go | Same libraries |
| Token Jaccard | ~20 lines (set intersection) | Trivial to implement |
| Exact | 1 line | N/A |

**Recommendation:** Implement all four in-house. They're well-defined algorithms, each under 100 lines. This keeps the `matching` package dependency-light and avoids pulling in libraries that may have different edge-case behaviour.

### Step 4: Threshold-based decision

Apply the configured thresholds to the best candidate:

```go
func (m *Matcher) decide(bestScore float64, bestCandidate *StoredEntity, extracted json.RawMessage, fieldScores []fieldScore) *MatchDecision {
    thresholds := m.config.Thresholds

    if bestCandidate == nil || bestScore < thresholds.ReviewZone {
        // No match — create new entity.
        return &MatchDecision{
            Action: ActionCreate,
            Data:   extracted,
        }
    }

    if bestScore >= thresholds.AutoMatch {
        // High-confidence match — auto-update.
        mergePlan := m.buildMergePlan(extracted, bestCandidate.Data, fieldScores)
        hasConflicts := hasConflictOps(mergePlan)

        action := ActionUpdate
        if hasConflicts {
            action = ActionConflict
        }

        return &MatchDecision{
            Action:          action,
            MatchedRecordID: bestCandidate.ID,
            MatchConfidence: bestScore,
            MatchMethod:     "composite",
            MergePlan:       mergePlan,
            Data:            extracted,
        }
    }

    // Review zone — flag for human review.
    return &MatchDecision{
        Action:          ActionReview,
        MatchedRecordID: bestCandidate.ID,
        MatchConfidence: bestScore,
        MatchMethod:     "composite",
        Data:            extracted,
    }
}
```

### Step 5: Merge plan

When updating an existing entity, build a per-field merge plan using conflict strategies:

```go
func (m *Matcher) buildMergePlan(extracted, existing json.RawMessage, scores []fieldScore) []FieldMergeOp {
    var plan []FieldMergeOp

    for _, fs := range scores {
        extractedVal := extractField(extracted, fs.Field)
        existingVal := extractField(existing, fs.Field)

        if extractedVal == existingVal || extractedVal == "" {
            plan = append(plan, FieldMergeOp{
                Field: fs.Field, Op: MergeKeep,
                ExistingValue: existingVal, ExtractedValue: extractedVal,
                Reason: "values match or extracted is empty",
            })
            continue
        }

        if existingVal == "" {
            plan = append(plan, FieldMergeOp{
                Field: fs.Field, Op: MergeWrite,
                ExistingValue: existingVal, ExtractedValue: extractedVal,
                Reason: "new value fills empty field",
            })
            continue
        }

        // Values differ — apply conflict strategy.
        strategy := m.config.ConflictStrategies[fs.Field]
        switch strategy {
        case ConflictLatestWins:
            plan = append(plan, FieldMergeOp{
                Field: fs.Field, Op: MergeWrite,
                ExistingValue: existingVal, ExtractedValue: extractedVal,
                Reason: "latest_wins strategy",
            })
        case ConflictHighestConf:
            // Caller must resolve based on confidence scores.
            plan = append(plan, FieldMergeOp{
                Field: fs.Field, Op: MergeConflict,
                ExistingValue: existingVal, ExtractedValue: extractedVal,
                Reason: "highest_confidence — requires confidence comparison",
            })
        case ConflictFlagForReview:
            plan = append(plan, FieldMergeOp{
                Field: fs.Field, Op: MergeConflict,
                ExistingValue: existingVal, ExtractedValue: extractedVal,
                Reason: "flag_for_review — human decision needed",
            })
        default:
            plan = append(plan, FieldMergeOp{
                Field: fs.Field, Op: MergeWrite,
                ExistingValue: existingVal, ExtractedValue: extractedVal,
                Reason: "no conflict strategy — defaulting to write",
            })
        }
    }

    return plan
}
```

### Anchor short-circuit

If an anchor lookup returns an exact match, skip scoring and go straight to update/merge:

```go
func (m *Matcher) Match(ctx context.Context, data json.RawMessage) (*MatchDecision, error) {
    anchors := BuildAnchors(data, m.config)

    // Anchor short-circuit: exact anchor match = definite match.
    if len(anchors) > 0 {
        anchorMatches, err := m.store.FindByAnchors(ctx, m.config.EntityType, anchors, nil)
        if err != nil {
            return nil, fmt.Errorf("anchor lookup: %w", err)
        }
        if len(anchorMatches) == 1 {
            mergePlan := m.buildMergePlan(data, anchorMatches[0].Data, nil)
            return &MatchDecision{
                Action:          ActionUpdate,
                MatchedRecordID: anchorMatches[0].ID,
                MatchConfidence: 1.0,
                MatchMethod:     "anchor",
                MergePlan:       mergePlan,
                Data:            data,
            }, nil
        }
        if len(anchorMatches) > 1 {
            // Multiple anchor matches = conflict (shouldn't happen with unique anchors).
            return &MatchDecision{
                Action:      ActionConflict,
                MatchMethod: "anchor",
                Candidates:  toMatchCandidates(anchorMatches, 1.0, "anchor"),
                Data:        data,
            }, nil
        }
    }

    // No anchor match — fall through to fuzzy matching.
    candidates, err := m.retrieveFuzzyCandidates(ctx, data)
    // ... scoring, thresholds, decision ...
}
```

### MatcherOption configuration

```go
type MatcherOption func(*matcherOptions)

type matcherOptions struct {
    embedder       EmbedderFunc
    tokenLimit     int     // max candidates from token search (default: 20)
    embeddingTopK  int     // max candidates from embedding search (default: 10)
}

func WithEmbedder(fn EmbedderFunc) MatcherOption
func WithTokenLimit(n int) MatcherOption
func WithEmbeddingTopK(n int) MatcherOption
```

## Implementation Plan

### Phase 1: Similarity functions
1. Implement `jaroWinkler(a, b string) float64` in `matching/similarity.go`.
2. Implement `normalizedLevenshtein(a, b string) float64`.
3. Implement `tokenJaccard(a, b string) float64`.
4. `exact` is trivial — inline.
5. Comprehensive tests with known test vectors.

### Phase 2: Scoring engine
1. Implement `scoreCandidate` — field-level scoring with weights.
2. Implement `buildMergePlan` — per-field merge using conflict strategies.
3. Unit tests with mock data (no database).

### Phase 3: Matcher pipeline
1. Implement `Matcher` struct with `NewMatcher` and `Match`.
2. Implement candidate retrieval with anchor short-circuit.
3. Implement threshold-based decision logic.
4. Integration tests using the `EntityStore` interface with a mock or in-memory implementation.

### Phase 4: MatcherRegistry
1. Mirror `MatchConfigRegistry` → `MatcherRegistry` that holds a `Matcher` per entity type.
2. Convenience `MatchAll` function that tries matching against all registered types.

## Consequences

### Positive
- **Closes the biggest gap** in EntityStore — consumers no longer need to implement their own scoring.
- **Fully declarative** — the same proto annotations that define extraction schemas also drive matching. Zero additional configuration.
- **Transparent decisions** — `MatchDecision` includes scores, candidate list, and merge plan. No black box.
- **No new dependencies** — similarity algorithms implemented in-house keeps `matching` dependency-light.
- **Testable** — the `Matcher` takes an `EntityStore` interface, making it easy to test with mocks.

### Negative
- **Not ML-based** — unlike Zingg or Splink, this is deterministic weighted scoring. It won't learn from user feedback. This is a deliberate trade-off: predictability and explainability over adaptive accuracy.
- **String-only scoring** — the current `FieldWeight` config assumes string fields. Numeric/date similarity (e.g., "is this salary within 10%?") would require extending the similarity function set.

### Risks
- **Weight tuning** — users must set sensible weights. Bad weights produce bad decisions. Mitigated by documentation and sensible defaults in proto annotations.
- **Performance** — scoring N candidates × M fields is O(N×M). For typical entity resolution (N < 100 candidates, M < 10 fields) this is sub-millisecond. At scale, the bottleneck is candidate retrieval, not scoring.

## Alternatives Considered

### A. Use an ML model for scoring
Rejected for now. ML-based matching (Zingg, Splink) offers adaptive accuracy but requires training data, is harder to debug, and adds significant complexity. The deterministic approach is right for v1 — ML-based scoring can be added as an alternative `SimilarityFunc` later.

### B. Delegate scoring to the caller
This is the current state. Rejected because it forces every consumer to reimplement the same logic, leading to inconsistencies and bugs.

### C. Use PostgreSQL's built-in similarity functions
Considered. PostgreSQL has `pg_trgm` (trigram similarity) and `fuzzystrmatch` (Levenshtein). However, doing scoring in SQL means the match config can't drive field-level weights and conflict strategies. The scoring needs to be application-layer to use the full `EntityMatchConfig`.
