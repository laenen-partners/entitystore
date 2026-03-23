# ADR-005: Entitystore Batch Write Preconditions

**Date**: 2026-03-23
**Status**: Accepted
**Authors**: Pascal Laenen

---

## Context

When prodcat links a ruleset to a product (`AddRuleset`), it must verify the ruleset exists and is not disabled before persisting the link. Today this requires two separate operations:

1. `FindByAnchors` — read the ruleset (outside any transaction)
2. `BatchWrite` — update the product with the new ruleset ID (inside a transaction)

This creates a **TOCTOU (time-of-check-time-of-use) gap**: between step 1 and step 2, another request could disable or delete the referenced entity. The same pattern applies whenever a write depends on the current state of another entity — referential integrity, status guards, uniqueness checks.

### Current workarounds

| Approach | Limitation |
|----------|-----------|
| Check-then-write in application code | TOCTOU gap — not transactionally safe |
| Use `TxStore` manually | Works, but forces every consumer to reimplement transactional reads + writes. The pattern is common enough to warrant first-class support. |
| Database foreign keys | Entitystore's anchor/entity model doesn't map to traditional FK constraints. Entities are type-agnostic rows. |

### Goals

- Eliminate TOCTOU gaps for common "entity X must exist" and "entity X must not exist" checks
- Keep the `BatchWrite` API ergonomic — preconditions declared alongside operations, not spread across manual transaction code
- Fail the entire batch atomically if any precondition is violated
- Return clear, structured errors so callers can distinguish precondition failures from store errors

---

## Decision

### Add `PreConditions` to `BatchWriteOp`

Extend `BatchWriteOp` with an optional list of preconditions that are checked **inside** the `BatchWrite` transaction, before the write is applied.

```go
type BatchWriteOp struct {
    WriteEntity    *WriteEntityOp
    UpsertRelation *UpsertRelationOp
    PreConditions  []PreCondition  // new — checked before applying this op
}
```

### PreCondition type

```go
type PreCondition struct {
    // What to look up.
    EntityType string
    Anchors    []matching.AnchorQuery

    // What to assert.
    MustExist    bool   // true → fail if no entity matches
    MustNotExist bool   // true → fail if any entity matches (for uniqueness)
    TagRequired  string // optional — if set, matched entity must carry this tag
    TagForbidden string // optional — if set, matched entity must NOT carry this tag
}
```

Only one of `MustExist` / `MustNotExist` may be true. Setting both is a validation error.

`TagRequired` and `TagForbidden` extend the check beyond existence. They enable status-like guards without needing the caller to deserialize entity data. For example, prodcat could tag disabled rulesets with `"disabled:true"` and use `TagForbidden: "disabled:true"` as a precondition.

### Evaluation semantics

1. For each op in the batch, preconditions are evaluated **first**, inside the same database transaction.
2. Each precondition runs a `FindByAnchors` query scoped to the transaction (`queries.WithTx(tx)`).
3. If any precondition fails, the entire batch is rolled back.
4. Preconditions are evaluated in order. On first failure, remaining preconditions and operations are skipped.

### Error type

```go
type PreConditionError struct {
    OpIndex    int           // which BatchWriteOp failed
    Condition  PreCondition  // the failing precondition
    Violation  string        // "not_found", "already_exists", "tag_required", "tag_forbidden"
}

func (e *PreConditionError) Error() string {
    return fmt.Sprintf("precondition failed on op %d: %s for %s",
        e.OpIndex, e.Violation, e.Condition.EntityType)
}
```

Callers use `errors.As(err, &PreConditionError{})` to inspect failures.

### Implementation sketch

Inside `BatchWrite`, before dispatching to `applyEntityWrite`:

```go
for i, op := range ops {
    for _, pc := range op.PreConditions {
        entities, err := findByAnchorsTx(ctx, q, pc.EntityType, pc.Anchors)
        if err != nil {
            return nil, fmt.Errorf("precondition query op %d: %w", i, err)
        }
        if pc.MustExist && len(entities) == 0 {
            return nil, &PreConditionError{OpIndex: i, Condition: pc, Violation: "not_found"}
        }
        if pc.MustNotExist && len(entities) > 0 {
            return nil, &PreConditionError{OpIndex: i, Condition: pc, Violation: "already_exists"}
        }
        if pc.TagRequired != "" && !entityHasTag(entities, pc.TagRequired) {
            return nil, &PreConditionError{OpIndex: i, Condition: pc, Violation: "tag_required"}
        }
        if pc.TagForbidden != "" && entityHasTag(entities, pc.TagForbidden) {
            return nil, &PreConditionError{OpIndex: i, Condition: pc, Violation: "tag_forbidden"}
        }
    }
    // ... apply write
}
```

`findByAnchorsTx` is a new internal function — the same query as `FindByAnchors` but using the transaction-scoped `*dbgen.Queries`.

### Usage example — prodcat `AddRuleset`

```go
func (s *Store) AddRulesetToProduct(ctx context.Context, product *prodcatv1.Product, rulesetID string, prov prodcat.Provenance) error {
    rulesetCfg := prodcatv1.RulesetMatchConfig()

    _, err := s.es.BatchWrite(ctx, []entitystore.BatchWriteOp{
        {
            WriteEntity: &entitystore.WriteEntityOp{
                Action:          entitystore.WriteActionUpdate,
                MatchedEntityID: product.ID,
                Data:            product,
                // ...
            },
            PreConditions: []entitystore.PreCondition{
                {
                    EntityType:   rulesetCfg.EntityType,
                    Anchors:      []matching.AnchorQuery{{Field: "ruleset_id", Value: rulesetID}},
                    MustExist:    true,
                    TagForbidden: "disabled:true",
                },
            },
        },
    })
    return err
}
```

One call. Atomic. No TOCTOU.

---

## Consequences

### Benefits

- **Transactional safety** — read + write in one transaction, no TOCTOU gaps
- **Declarative** — preconditions are data, not imperative code; easy to audit and test
- **Backward compatible** — `PreConditions` is a new optional field; existing callers are unaffected
- **Reusable** — any entitystore consumer (prodcat, onboarding, future services) gets the same guarantees

### Trade-offs

- **Adds complexity to BatchWrite** — precondition evaluation adds query overhead inside the transaction. For high-throughput paths this may matter; for admin-facing catalogue operations it's negligible.
- **Tag-based guards are coarse** — `TagRequired`/`TagForbidden` only checks tag presence, not arbitrary field values. This is intentional: entitystore is type-agnostic and shouldn't deserialize proto data for field-level checks. For richer guards, consumers should use `TxStore` directly.
- **Transaction hold time increases** — precondition queries extend the time the transaction is open. Keep precondition count small per batch.

### Migration

No database migration required. This is purely a Go API addition to the `store` package. The existing `findByAnchors` SQL query is reused inside the transaction.

### What this does NOT solve

- Cross-batch consistency (e.g. "these two batches must both succeed or both fail") — use `TxStore` for multi-batch workflows
- Field-level assertions on entity data (e.g. "status must be active") — out of scope; use tags or application-level `TxStore` reads
- Cascade operations (e.g. "disabling a ruleset auto-updates all referencing products") — application logic, not store-level

---

## References

- entitystore `BatchWrite` implementation: `store/store_write.go:68-108`
- entitystore `TxStore` API: `store/tx.go`
- prodcat business rules discussion: this conversation
