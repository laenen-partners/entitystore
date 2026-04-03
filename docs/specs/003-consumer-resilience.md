# Spec: EntityStore — Consumer Resilience (v0.28.0)

## Context

The named consumer API (v0.27.0, spec 001) provides at-least-once delivery with cursor-based progress tracking. When `ConsumerFunc` returns an error, the cursor does not advance and the batch retries on the next poll cycle.

This works for transient failures but has no protection against:

| Failure mode | Current behaviour | Problem |
|---|---|---|
| External service down (API, pubsub) | Retry every PollInterval (5s) forever | Hammers a down service, no backoff |
| Poison event (malformed proto, deleted entity, bug) | Same batch retries forever | Consumer permanently stuck |
| One bad event in batch of 100 | Entire batch retries | 99 good events blocked |
| Sustained failure (schema mismatch, permission error) | Retry forever | Lag grows unbounded, no alerting signal |

## Goals

1. Exponential backoff on consecutive failures — stop hammering failing services
2. Poison event detection — skip events that always fail after N retries
3. Dead letter table — persist skipped events for investigation and replay
4. Health extensions — surface failure state for monitoring and alerting
5. No breaking changes to `ConsumerFunc` signature or existing `ConsumerConfig` fields

## Non-goals

- Per-event processing (batch-first, fallback to one-by-one) — this is application-level policy, implemented by the consumer func itself
- Circuit breaker — can be layered on top by the consumer func; adding it to the consumer loop introduces complexity for a pattern only some consumers need
- Metrics/tracing — out of scope; consumers log via slog and expose state via Health()

## Design

### ConsumerConfig additions

```go
type ConsumerConfig struct {
    // ... existing fields unchanged ...

    // MaxRetries is the number of consecutive times a batch can fail before
    // its events are written to the dead letter table and the cursor advances.
    // Default: 0 (disabled — retry forever, preserving v0.27.0 behaviour).
    MaxRetries int

    // InitialBackoff is the wait duration after the first failure.
    // Subsequent failures double the backoff up to MaxBackoff.
    // Default: 0 (disabled — no backoff, preserving v0.27.0 behaviour).
    InitialBackoff time.Duration

    // MaxBackoff caps the exponential backoff duration.
    // Default: 5 minutes.
    MaxBackoff time.Duration

    // OnDeadLetter is called when events are moved to the dead letter table.
    // Use for alerting (Slack, PagerDuty, etc.). Optional.
    OnDeadLetter func(consumerName string, events []Event, err error)
}
```

All new fields default to zero values which preserve exact v0.27.0 behaviour. Existing consumers are unaffected.

### Backoff behaviour

State on the `Consumer` struct:

```go
type Consumer struct {
    // ... existing fields ...
    consecutiveFailures int
    backoffUntil        time.Time
    lastErr             error
}
```

In the poll loop:

```go
func (c *Consumer) poll(ctx context.Context) error {
    // Backoff check — skip processing if we're still in the backoff window.
    if time.Now().Before(c.backoffUntil) {
        return nil
    }

    isHolder, err := c.tryAcquireOrRenew(ctx)
    if err != nil || !isHolder {
        return err
    }

    err = c.processBatch(ctx)
    if err == nil {
        c.consecutiveFailures = 0
        c.backoffUntil = time.Time{}
        c.lastErr = nil
        return nil
    }

    // Failure path.
    c.consecutiveFailures++
    c.lastErr = err

    if c.cfg.MaxRetries > 0 && c.consecutiveFailures >= c.cfg.MaxRetries {
        // Poison batch — dead letter and advance cursor.
        c.deadLetterCurrentBatch(ctx, err)
        c.consecutiveFailures = 0
        c.backoffUntil = time.Time{}
        return nil
    }

    // Apply backoff.
    if c.cfg.InitialBackoff > 0 {
        backoff := c.cfg.InitialBackoff * time.Duration(1<<min(c.consecutiveFailures-1, 10))
        if c.cfg.MaxBackoff > 0 && backoff > c.cfg.MaxBackoff {
            backoff = c.cfg.MaxBackoff
        }
        c.backoffUntil = time.Now().Add(backoff)
        c.cfg.Logger.WarnContext(ctx, "consumer backing off",
            "name", c.cfg.Name,
            "consecutive_failures", c.consecutiveFailures,
            "backoff", backoff,
            "error", err,
        )
    }

    return nil
}
```

### Dead letter table

```sql
-- migrate:up
CREATE TABLE entity_event_dead_letters (
    id              BIGSERIAL PRIMARY KEY,
    consumer_name   TEXT NOT NULL,
    event_id        TEXT NOT NULL,
    event_type      TEXT NOT NULL,
    payload_type    TEXT NOT NULL,
    entity_id       TEXT,
    payload         JSONB,
    error_message   TEXT NOT NULL,
    retry_count     INT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_dead_letters_consumer ON entity_event_dead_letters(consumer_name, created_at DESC);
CREATE INDEX idx_dead_letters_event ON entity_event_dead_letters(event_id);

-- migrate:down
DROP TABLE entity_event_dead_letters;
```

### Dead letter flow

When `consecutiveFailures >= MaxRetries`:

1. Read the current batch (same events that keep failing)
2. Write each event to `entity_event_dead_letters` with the consumer name and last error
3. Advance the cursor past the batch (same as a successful process)
4. Call `OnDeadLetter` callback if configured
5. Log at ERROR level
6. Reset consecutive failures and backoff

```go
func (c *Consumer) deadLetterCurrentBatch(ctx context.Context, lastErr error) {
    cursor, _ := c.queries.GetConsumerCursor(ctx, c.cfg.Name)
    rows, _ := c.queries.GetEventsAfterCursor(ctx, dbgen.GetEventsAfterCursorParams{
        AfterAt:   cursor.LastEventAt,
        AfterID:   cursor.LastEventID,
        BatchSize: int32(c.cfg.BatchSize),
    })

    for _, row := range rows {
        c.queries.InsertDeadLetter(ctx, dbgen.InsertDeadLetterParams{
            ConsumerName: c.cfg.Name,
            EventID:      row.ID.String(),
            EventType:    row.EventType,
            PayloadType:  row.PayloadType,
            EntityID:     entityIDFromRow(row),
            Payload:      row.Payload,
            ErrorMessage: lastErr.Error(),
            RetryCount:   c.consecutiveFailures,
        })
    }

    // Advance cursor past the dead-lettered batch.
    if len(rows) > 0 {
        last := rows[len(rows)-1]
        c.queries.AdvanceConsumerCursor(ctx, dbgen.AdvanceConsumerCursorParams{
            Name:        c.cfg.Name,
            HolderID:    pgtype.Text{String: c.cfg.HolderID, Valid: true},
            LastEventAt: last.OccurredAt,
            LastEventID: pgtype.UUID{Bytes: last.ID, Valid: true},
        })
    }

    c.cfg.Logger.ErrorContext(ctx, "consumer dead-lettered batch",
        "name", c.cfg.Name,
        "event_count", len(rows),
        "retry_count", c.consecutiveFailures,
        "error", lastErr,
    )

    if c.cfg.OnDeadLetter != nil {
        events := make([]Event, len(rows))
        for i, row := range rows {
            events[i] = eventFromRow(row)
        }
        c.cfg.OnDeadLetter(c.cfg.Name, events, lastErr)
    }
}
```

### Health extensions

Extend `ConsumerHealth`:

```go
type ConsumerHealth struct {
    Name                string     `json:"name"`
    LastEventAt         time.Time  `json:"last_event_at"`
    Lag                 string     `json:"lag"`
    HolderID            string     `json:"holder_id,omitempty"`
    LockExpiresAt       *time.Time `json:"lock_expires_at,omitempty"`

    // New in v0.28.0
    ConsecutiveFailures int        `json:"consecutive_failures"`
    LastError           string     `json:"last_error,omitempty"`
    BackoffUntil        *time.Time `json:"backoff_until,omitempty"`
    DeadLetterCount     int64      `json:"dead_letter_count"`
    State               string     `json:"state"` // "healthy", "degraded", "failing"
}
```

State derivation:
- `healthy`: 0 consecutive failures
- `degraded`: 1+ consecutive failures but below MaxRetries
- `failing`: at MaxRetries or dead letters written recently

The `Health()` method queries `entity_event_dead_letters` for the count per consumer. The `ConsecutiveFailures`, `LastError`, and `BackoffUntil` come from the consumer's in-memory state — they need to be persisted or exposed via the lock row.

**Persistence option**: Add columns to `entity_event_consumers`:

```sql
ALTER TABLE entity_event_consumers
    ADD COLUMN consecutive_failures INT NOT NULL DEFAULT 0,
    ADD COLUMN last_error TEXT,
    ADD COLUMN backoff_until TIMESTAMPTZ;
```

This makes failure state visible to Health() even when the consumer instance is different from the one that experienced the failure (e.g. after a restart or leader change).

### Dead letter management

SQLC queries for dead letter operations:

```sql
-- name: InsertDeadLetter :exec
INSERT INTO entity_event_dead_letters (consumer_name, event_id, event_type, payload_type, entity_id, payload, error_message, retry_count)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: ListDeadLetters :many
SELECT * FROM entity_event_dead_letters
WHERE consumer_name = $1
ORDER BY created_at DESC
LIMIT $2;

-- name: CountDeadLetters :one
SELECT COUNT(*) FROM entity_event_dead_letters WHERE consumer_name = $1;

-- name: DeleteDeadLetter :exec
DELETE FROM entity_event_dead_letters WHERE id = $1;

-- name: DeleteDeadLettersByConsumer :exec
DELETE FROM entity_event_dead_letters WHERE consumer_name = $1;

-- name: GetDeadLetter :one
SELECT * FROM entity_event_dead_letters WHERE id = $1;
```

### Replay API

```go
// ReplayDeadLetters re-processes dead-lettered events for a consumer.
// Successfully processed events are removed from the dead letter table.
// Events that still fail remain in the table with an updated retry_count.
func (c *Consumer) ReplayDeadLetters(ctx context.Context) (replayed, failed int, err error)
```

This reads all dead letters for the consumer, calls `ConsumerFunc` for each one individually, and removes successful ones. This is an explicit admin action, not part of the normal consumer loop.

## Migration plan

1. Add migration for `entity_event_dead_letters` table
2. Add migration to extend `entity_event_consumers` with failure state columns
3. Add SQLC queries for dead letter operations
4. Extend `ConsumerConfig` with new fields (zero-value defaults preserve v0.27.0 behaviour)
5. Implement backoff logic in `poll()`
6. Implement `deadLetterCurrentBatch()`
7. Extend `ConsumerHealth` and `Health()` method
8. Add `ReplayDeadLetters()` method
9. Tests with testcontainers

## Acceptance criteria

- [ ] `InitialBackoff: 1s` causes exponential backoff: 1s, 2s, 4s, 8s... capped at MaxBackoff
- [ ] `MaxRetries: 10` dead-letters the batch after 10 consecutive failures
- [ ] Dead-lettered events appear in `entity_event_dead_letters` with consumer name, error, retry count
- [ ] Cursor advances past dead-lettered events (consumer unblocked)
- [ ] `OnDeadLetter` callback fires with the dead-lettered events and error
- [ ] `ConsumerHealth` reports `ConsecutiveFailures`, `LastError`, `State`
- [ ] `ReplayDeadLetters()` re-processes and removes successful dead letters
- [ ] Zero-value config preserves exact v0.27.0 behaviour (no backoff, no dead letter, retry forever)
- [ ] All existing tests pass without changes
