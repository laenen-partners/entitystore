# Migration Guide: v0.17 → v0.18

v0.18 adds generated `Tokens`, `EmbedText`, `Anchors`, and `WriteOp` functions to `protoc-gen-entitystore`, plus `WriteOpOption` helpers. These are **non-breaking, additive changes** — existing code continues to work.

## What changed

| Area | Before (v0.17) | After (v0.18) |
|---|---|---|
| Token extraction | `tokens.Extract(msg, cfg)` via reflection or manual `matching.BuildTokens(json, cfg)` | `{Entity}Tokens(msg)` — generated, typed, no reflection |
| Embed text | `matching.TextToEmbed(json, cfg.EmbedFields)` | `{Entity}EmbedText(msg)` — generated, typed |
| Anchor extraction | Hand-written `buildAnchors(cfg, msg)` per entity type | `{entity}Anchors(msg)` — generated, with normalizers applied |
| WriteEntityOp construction | 15-20 lines of manual wiring | `{Entity}WriteOp(msg, action, opts...)` — one call |
| Write options | Set struct fields directly | `store.WithTags(...)`, `store.WithConfidence(...)`, etc. |

## No database migration needed

No schema changes. No new tables. Just regenerate your code with the updated plugin.

## Step 1: Update the entitystore dependency

```sh
go get github.com/laenen-partners/entitystore@v0.18.0
```

## Step 2: Regenerate proto code

```sh
buf generate
```

Each annotated message now generates up to four additional functions alongside the existing `MatchConfig` and `ExtractionSchema`:

```go
// Existing (unchanged):
PersonMatchConfig()        // matching.EntityMatchConfig
PersonExtractionSchema()   // extraction.ExtractionSchema

// New:
PersonTokens(msg)          // map[string][]string
PersonEmbedText(msg)       // string
personAnchors(msg)         // []matching.AnchorQuery (unexported, used by WriteOp)
PersonWriteOp(msg, action) // *store.WriteEntityOp
```

Functions are only generated when relevant annotations exist:
- `Tokens` — only if at least one field has `token_field: true`
- `EmbedText` — only if at least one field has `embed: true`
- `Anchors` + `WriteOp` — only if at least one field has `anchor: true` or message has `composite_anchors`

## Step 3: Replace manual token extraction

### Before

Using reflection-based utility:

```go
cfg := partyv1.IndividualMatchConfig()
tokens := tokens.Extract(ind, cfg)  // reflection, error-prone
```

Or using `matching.BuildTokens` with raw JSON:

```go
cfg := partyv1.IndividualMatchConfig()
data, _ := protojson.Marshal(ind)
tokens := matching.BuildTokens(data, cfg)
```

### After

```go
tokens := partyv1.IndividualTokens(ind)
```

That's it. The generated function:
- Uses typed getters (`msg.GetFirstName()`) — no reflection
- Calls `matching.Tokenize()` — same tokenization logic (lowercase, strip punctuation, deduplicate)
- Skips empty fields automatically
- Returns `map[string][]string` keyed by proto field name

### What `matching.Tokenize` does

The tokenizer lowercases the input, replaces all non-letter/non-digit characters with spaces, splits on whitespace, and deduplicates. For example:

```
"Alice M. Johnson-Smith" → ["alice", "m", "johnson", "smith"]
"Senior Software Engineer" → ["senior", "software", "engineer"]
```

This is the same logic that `matching.BuildTokens` uses internally — the generated code just skips the JSON round-trip and reflection.

## Step 4: Replace manual embed text construction

### Before

```go
cfg := partyv1.IndividualMatchConfig()
data, _ := protojson.Marshal(ind)
text := matching.TextToEmbed(data, cfg.EmbedFields)
```

### After

```go
text := partyv1.IndividualEmbedText(ind)
```

The generated function concatenates all `embed: true` fields with a space separator. Field order follows proto field numbers for deterministic output. Empty fields are skipped.

For a Person with `email` (embed), `full_name` (embed), `job_title` (embed):

```go
// msg = &Person{Email: "alice@example.com", FullName: "Alice Johnson", JobTitle: "CTO"}
text := PersonEmbedText(msg)
// → "alice@example.com Alice Johnson CTO"
```

Pass this text to your embedding model:

```go
vecs, _ := embedder.Embed(ctx, []string{partyv1.IndividualEmbedText(ind)})
```

## Step 5: Replace manual anchor extraction

### Before

Every domain service has a hand-written `buildAnchors` function:

```go
func buildAnchors(cfg matching.EntityMatchConfig, ind *partyv1.Individual) []matching.AnchorQuery {
    var anchors []matching.AnchorQuery
    if v := ind.GetNationalId(); v != "" {
        anchors = append(anchors, matching.AnchorQuery{
            Field: "national_id",
            Value: matching.NormalizeLowercaseTrim(v),
        })
    }
    // ... repeat for each anchor field ...
    // ... composite anchors need even more code ...
    return anchors
}
```

### After

The generated `{entity}Anchors` function handles this automatically — including normalizers and composite anchors. It's unexported (used internally by `WriteOp`), but you can access it if needed via the generated `MatchConfig`:

```go
// Usually you don't call anchors directly — WriteOp does it for you.
// But if you need anchors separately (e.g. for FindByAnchors):
op := partyv1.IndividualWriteOp(ind, store.WriteActionCreate)
anchors := op.Anchors  // already normalized, composites included
```

What the generated anchor function does:
- Calls the typed getter for each `anchor: true` field
- Applies the configured normalizer (e.g. `NormalizeLowercaseTrim`, `NormalizePhone`)
- Skips empty fields
- Builds composite anchor values by joining field values with `|` separator

## Step 6: Replace manual WriteEntityOp construction

This is the biggest win. Every domain service's Create and Update methods have 15-20 lines of boilerplate to wire up a `WriteEntityOp`. The generated `WriteOp` function replaces all of it.

### Before

```go
func (s *Service) CreateIndividual(ctx context.Context, ind *partyv1.Individual) error {
    cfg := partyv1.IndividualMatchConfig()
    anchors := buildAnchors(cfg, ind)
    tokens := tokens.Extract(ind, cfg)

    _, err := s.es.BatchWrite(ctx, []store.BatchWriteOp{
        {WriteEntity: &store.WriteEntityOp{
            Action:     store.WriteActionCreate,
            Data:       ind,
            Confidence: 1.0,
            Anchors:    anchors,
            Tags:       []string{tags.Active()},
            Tokens:     tokens,
            Provenance: matching.ProvenanceEntry{
                SourceURN:   urn,
                ExtractedAt: time.Now(),
                ModelID:     modelID,
                Confidence:  0.95,
                Fields:      extractedFields,
                MatchMethod: "create",
            },
        }},
    })
    return err
}
```

### After

```go
func (s *Service) CreateIndividual(ctx context.Context, ind *partyv1.Individual) error {
    op := partyv1.IndividualWriteOp(ind, store.WriteActionCreate,
        store.WithTags("active"),
        store.WithProvenance(matching.ProvenanceEntry{
            SourceURN: urn, ModelID: modelID,
            MatchMethod: "create",
        }),
    )
    _, err := s.es.BatchWrite(ctx, []store.BatchWriteOp{{WriteEntity: op}})
    return err
}
```

### What `WriteOp` sets automatically

| Field | Value | Source |
|---|---|---|
| `Action` | from parameter | caller provides |
| `Data` | the proto message | first parameter |
| `Confidence` | `1.0` (override with `WithConfidence`) | default |
| `Anchors` | from `{entity}Anchors(msg)` | generated from `anchor: true` fields |
| `Tokens` | from `{Entity}Tokens(msg)` | generated from `token_field: true` fields |

### What you still set via options

| Option | When to use |
|---|---|
| `store.WithTags("active", "ws:acme")` | Always — tags are domain-specific |
| `store.WithProvenance(entry)` | Always — provenance tracks extraction origin |
| `store.WithMatchedEntityID(id)` | Update and merge operations |
| `store.WithConfidence(0.92)` | Override the default 1.0 |
| `store.WithEmbedding(vec)` | When you've pre-computed the embedding |
| `store.WithID("uuid")` | Idempotent creates with client-generated IDs |

### Update operations

```go
op := partyv1.IndividualWriteOp(ind, store.WriteActionUpdate,
    store.WithMatchedEntityID(existingID),
    store.WithTags("active"),
    store.WithProvenance(matching.ProvenanceEntry{
        SourceURN: urn, ModelID: modelID,
        MatchMethod: "anchor", MatchConfidence: 1.0,
    }),
)
```

### Merge operations

```go
op := partyv1.IndividualWriteOp(partialInd, store.WriteActionMerge,
    store.WithMatchedEntityID(existingID),
    store.WithConfidence(0.87),
    store.WithProvenance(matching.ProvenanceEntry{
        SourceURN: urn, ModelID: modelID,
        MatchMethod: "composite", MatchConfidence: 0.87,
    }),
)
```

## Complete migration checklist

- [ ] Update entitystore dependency to v0.18
- [ ] Run `buf generate` to regenerate proto code
- [ ] Replace `tokens.Extract(msg, cfg)` calls with `{Entity}Tokens(msg)`
- [ ] Replace `matching.TextToEmbed(data, cfg.EmbedFields)` with `{Entity}EmbedText(msg)`
- [ ] Replace hand-written `buildAnchors` functions with generated `WriteOp` (or use `op.Anchors`)
- [ ] Replace manual `WriteEntityOp` construction with `{Entity}WriteOp(msg, action, opts...)`
- [ ] Remove unused `buildAnchors` functions from each domain package
- [ ] Remove `internal/tokens/` package if all callers migrated
- [ ] Run tests — all existing behaviour is preserved
