# Migration Guide: v0.7 → v0.8

v0.8 introduces proto-native write APIs and typed relation data. These are **breaking changes**.

## Summary of changes

| What | v0.7 | v0.8 |
|---|---|---|
| `WriteEntityOp.Data` | `json.RawMessage` | `proto.Message` |
| `WriteEntityOp.EntityType` | explicit `string` field | removed — derived from `proto.MessageName(Data)` |
| `UpsertRelationOp.Data` | `map[string]any` | `proto.Message` |
| `StoredRelation.Data` | `map[string]any` | `json.RawMessage` |
| `StoredRelation.DataType` | *(didn't exist)* | `string` — auto-populated from proto message name |
| `TxStore.UpsertRelation` | accepts `matching.StoredRelation` | accepts `*store.UpsertRelationOp` |
| `MarshalEntityData()` | convenience func | removed |
| `StoredEntity.GetData()` | *(didn't exist)* | `GetData(msg proto.Message) error` |
| `StoredRelation.GetData()` | *(didn't exist)* | `GetData(msg proto.Message) error` |

## Database migration

A new migration adds the `data_type` column to `entity_relations`. This runs automatically if you use `WithAutoMigrate()` or call `entitystore.Migrate()`.

If you manage migrations manually, apply:

```sql
ALTER TABLE entity_relations ADD COLUMN data_type TEXT NOT NULL DEFAULT '';
```

Existing relation rows get an empty `data_type`, which is safe — the field is informational.

## Step-by-step migration

### 1. Entity writes — `WriteEntityOp`

**Before (v0.7):**
```go
data, _ := json.Marshal(map[string]any{
    "email":     "alice@example.com",
    "full_name": "Alice Johnson",
})

es.BatchWrite(ctx, []entitystore.BatchWriteOp{
    {WriteEntity: &entitystore.WriteEntityOp{
        Action:     entitystore.WriteActionCreate,
        EntityType: "entities.v1.Person",
        Data:       data,
        Confidence: 0.95,
        // ...
    }},
})
```

**After (v0.8):**
```go
// Option A: Use your generated proto message directly.
person := &entitiesv1.Person{
    Email:    "alice@example.com",
    FullName: "Alice Johnson",
}

es.BatchWrite(ctx, []entitystore.BatchWriteOp{
    {WriteEntity: &entitystore.WriteEntityOp{
        Action:     entitystore.WriteActionCreate,
        // EntityType is derived automatically: "entities.v1.Person"
        Data:       person,
        Confidence: 0.95,
        // ...
    }},
})

// Option B: Use structpb.Struct for dynamic data (no proto definition needed).
data, _ := structpb.NewStruct(map[string]any{
    "email":     "alice@example.com",
    "full_name": "Alice Johnson",
})

es.BatchWrite(ctx, []entitystore.BatchWriteOp{
    {WriteEntity: &entitystore.WriteEntityOp{
        Action:     entitystore.WriteActionCreate,
        // EntityType will be "google.protobuf.Struct"
        Data:       data,
        Confidence: 0.95,
        // ...
    }},
})
```

**Key changes:**
- Remove `EntityType` from all `WriteEntityOp` literals — the compiler will flag this.
- Replace `json.RawMessage` / `json.Marshal(...)` with a `proto.Message` value.
- For generated proto types, the entity type is the fully qualified proto name (e.g., `"entities.v1.Person"`).
- For `structpb.Struct`, the entity type is `"google.protobuf.Struct"`. Update any queries that filter by entity type accordingly.

### 2. Reading entity data — `StoredEntity.GetData()`

**Before (v0.7):**
```go
entity, _ := es.GetEntity(ctx, id)
var data map[string]any
json.Unmarshal(entity.Data, &data)
name := data["full_name"].(string)
```

**After (v0.8):**
```go
entity, _ := es.GetEntity(ctx, id)

// Option A: Unmarshal into your proto type.
var person entitiesv1.Person
entity.GetData(&person)
name := person.FullName

// Option B: Unmarshal into structpb.Struct for dynamic access.
var s structpb.Struct
entity.GetData(&s)
name := s.Fields["full_name"].GetStringValue()

// Option C: entity.Data is still json.RawMessage — raw JSON access still works.
var data map[string]any
json.Unmarshal(entity.Data, &data)
```

### 3. Relation writes — `UpsertRelationOp`

**Before (v0.7):**
```go
es.BatchWrite(ctx, []entitystore.BatchWriteOp{
    {UpsertRelation: &entitystore.UpsertRelationOp{
        SourceID:     personID,
        TargetID:     companyID,
        RelationType: "works_at",
        Confidence:   0.95,
        Data:         map[string]any{"role": "VP", "start_date": "2023-01-15"},
    }},
})
```

**After (v0.8):**
```go
// Option A: Use a proto message for typed relation data.
relData := &employmentv1.Employment{
    Role:      "VP",
    StartDate: "2023-01-15",
}

es.BatchWrite(ctx, []entitystore.BatchWriteOp{
    {UpsertRelation: &entitystore.UpsertRelationOp{
        SourceID:     personID,
        TargetID:     companyID,
        RelationType: "works_at",
        Confidence:   0.95,
        Data:         relData, // DataType auto-set to "employment.v1.Employment"
    }},
})

// Option B: Use structpb.Struct for dynamic data.
relData, _ := structpb.NewStruct(map[string]any{
    "role": "VP", "start_date": "2023-01-15",
})

es.BatchWrite(ctx, []entitystore.BatchWriteOp{
    {UpsertRelation: &entitystore.UpsertRelationOp{
        SourceID:     personID,
        TargetID:     companyID,
        RelationType: "works_at",
        Confidence:   0.95,
        Data:         relData,
    }},
})

// Option C: No relation data — Data field is optional (nil).
es.BatchWrite(ctx, []entitystore.BatchWriteOp{
    {UpsertRelation: &entitystore.UpsertRelationOp{
        SourceID:     personID,
        TargetID:     companyID,
        RelationType: "works_at",
        Confidence:   0.95,
        // Data omitted — DataType will be empty string
    }},
})
```

### 4. Reading relation data — `StoredRelation.GetData()`

**Before (v0.7):**
```go
rels, _ := es.GetRelationsFromEntity(ctx, personID)
role := rels[0].Data["role"].(string) // map[string]any access
```

**After (v0.8):**
```go
rels, _ := es.GetRelationsFromEntity(ctx, personID)

// Check the data type to know what proto message to unmarshal into.
fmt.Println(rels[0].DataType) // e.g., "employment.v1.Employment"

// Unmarshal into the correct proto type.
var emp employmentv1.Employment
rels[0].GetData(&emp)
role := emp.Role

// Or use structpb.Struct for dynamic access.
var s structpb.Struct
rels[0].GetData(&s)
role := s.Fields["role"].GetStringValue()

// Raw JSON is still available via rels[0].Data (json.RawMessage).
```

### 5. Transactions — `TxStore.UpsertRelation`

**Before (v0.7):**
```go
tx, _ := es.Tx(ctx)
tx.UpsertRelation(ctx, matching.StoredRelation{
    SourceID:     personID,
    TargetID:     companyID,
    RelationType: "works_at",
    Confidence:   0.85,
})
```

**After (v0.8):**
```go
tx, _ := es.Tx(ctx)
tx.UpsertRelation(ctx, &entitystore.UpsertRelationOp{
    SourceID:     personID,
    TargetID:     companyID,
    RelationType: "works_at",
    Confidence:   0.85,
})
```

### 6. Removed: `MarshalEntityData()`

This function is no longer needed since `WriteEntityOp.Data` accepts `proto.Message` directly. Remove all calls to `entitystore.MarshalEntityData()`.

## Required import changes

```go
// Remove (if no longer used):
import "encoding/json"

// Add:
import "google.golang.org/protobuf/types/known/structpb" // for dynamic data
// Your generated proto packages are used directly as Data values.
```

## Compiler-assisted migration

The Go compiler will catch most issues:
- `unknown field EntityType` — remove the field from `WriteEntityOp` literals.
- `cannot use json.RawMessage as proto.Message` — switch to `structpb.NewStruct()` or a generated proto type.
- `cannot use map[string]any as proto.Message` — same fix for relation data.
- `cannot use matching.StoredRelation as *store.UpsertRelationOp` — update `TxStore.UpsertRelation` calls.
