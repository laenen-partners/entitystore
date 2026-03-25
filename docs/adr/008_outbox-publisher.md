# ADR-008: Outbox Publisher with TTL-Based Leader Election

**Date**: 2026-03-25
**Status**: Accepted
**Authors**: Pascal Laenen

---

## Context

The event store (ADR-008's prerequisite: `entity_events` table with `published_at` column) captures lifecycle and domain events inside write transactions. For these events to reach external systems (webhooks, message queues, analytics pipelines), a publisher must poll unpublished events and deliver them reliably.

### Goals

- Exactly-once delivery semantics (at-least-once with idempotent consumers)
- Single active publisher across all instances (leader election)
- Auto-recovery when the leader crashes or hangs (TTL-based lock expiry)
- Caller-defined delivery via a `PublishFunc` — the library doesn't prescribe Kafka, SQS, or HTTP
- Observable lock state (who holds it, when it expires)
- Works with existing `pgxpool.Pool` — no new infrastructure

### Non-goals

- Guaranteed ordering across partitions (events are ordered per-entity, not globally)
- Fan-out to multiple consumers (that's the consumer's responsibility)
- Event replay or projection

---

## Decision

### Leader Election via TTL Lock Table

Use a dedicated `publisher_lock` table with a TTL-based lease instead of `pg_advisory_lock`:

```sql
CREATE TABLE publisher_lock (
    id          TEXT PRIMARY KEY DEFAULT 'singleton',
    holder_id   TEXT NOT NULL,
    acquired_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL,
    renewed_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

**Why not `pg_advisory_lock`?**

| Concern | Advisory lock | TTL table |
|---------|--------------|-----------|
| Release on crash (connection stays open) | No — lock held until session ends | Yes — TTL expires |
| Works across connection pool reconnects | Fragile — lock tied to session | Yes — any connection can renew |
| Observable | `pg_locks` (opaque int) | `SELECT * FROM publisher_lock` (clear) |
| Configurable timeout | No | Yes |

**Acquire** — atomic upsert that only succeeds if the lock is unheld or expired:

```sql
INSERT INTO publisher_lock (holder_id, expires_at, renewed_at)
VALUES ($1, now() + $2::interval, now())
ON CONFLICT (id) DO UPDATE
SET holder_id = EXCLUDED.holder_id,
    acquired_at = now(),
    expires_at = EXCLUDED.expires_at,
    renewed_at = now()
WHERE publisher_lock.expires_at < now();
```

**Renew** — the active holder extends the TTL on each poll cycle:

```sql
UPDATE publisher_lock
SET expires_at = now() + $2::interval, renewed_at = now()
WHERE holder_id = $1;
```

**Release** — on graceful shutdown:

```sql
DELETE FROM publisher_lock WHERE holder_id = $1;
```

### Publisher Loop

```
1. Try acquire lock
2. If not holder → sleep PollInterval, goto 1
3. If holder:
   a. BEGIN
   b. SELECT ... WHERE published_at IS NULL
      ORDER BY occurred_at LIMIT batch_size
      FOR UPDATE SKIP LOCKED
   c. Call PublishFunc(ctx, events)
   d. If success → UPDATE SET published_at = now(); COMMIT
   e. If error → ROLLBACK (events stay unpublished, retried next poll)
   f. Renew lock TTL
   g. Sleep PollInterval, goto 1
4. On ctx.Done() → release lock, return
```

`FOR UPDATE SKIP LOCKED` prevents the publisher from blocking on rows still being inserted by concurrent `BatchWrite` transactions.

### PublishFunc API

```go
type PublishFunc func(ctx context.Context, events []Event) error
```

The caller provides the delivery mechanism. Returning `nil` means all events were delivered successfully. Returning an error causes the batch to be rolled back and retried on the next poll.

Examples:
- Kafka: produce to a topic, return after acks
- Webhook: POST to an endpoint, check status
- Channel: send to a Go channel for in-process consumers

### Configuration

```go
type PublisherConfig struct {
    BatchSize    int           // max events per poll (default 100)
    PollInterval time.Duration // time between polls (default 5s)
    LockTTL      time.Duration // lock lease duration (default 30s)
    HolderID     string        // unique instance ID (default: hostname-pid-random)
}
```

The lock TTL should be significantly larger than PollInterval to avoid flapping. Default ratio: TTL = 6x PollInterval.

### Package Location

The publisher lives in the `store` package since it operates directly on `entity_events` and `publisher_lock` tables using the same `pgxpool.Pool`.

---

## Consequences

### Positive

- **No new infrastructure** — uses the same PostgreSQL database
- **Crash-safe** — TTL expiry handles unclean shutdowns
- **Observable** — `SELECT * FROM publisher_lock` shows current leader
- **Flexible** — `PublishFunc` decouples delivery mechanism from the publisher loop
- **Idempotent** — `published_at` prevents double-processing; consumers should also be idempotent

### Negative

- **Polling-based** — up to `PollInterval` latency between event insertion and delivery. Acceptable for most use cases; LISTEN/NOTIFY can be added later to reduce latency.
- **Single writer** — only one publisher runs at a time. Throughput is bounded by `BatchSize / PollInterval`. For high-volume scenarios, partition by entity_type and run multiple publishers.
- **Lock table maintenance** — the `publisher_lock` row persists. Not a problem since it's a single row.

### Alternatives Considered

- **pg_advisory_lock**: Simpler but no TTL, fragile with connection pools, opaque.
- **LISTEN/NOTIFY**: Lower latency but requires persistent connection and doesn't replace the need for a polling fallback.
- **External lock (Redis, etcd)**: New infrastructure dependency for a single-row lock.
