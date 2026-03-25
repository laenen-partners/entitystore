package store

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/laenen-partners/entitystore/store/internal/dbgen"
)

// PublishFunc is called by the publisher to deliver a batch of events.
// Return nil if all events were delivered successfully.
// Return an error to roll back the batch and retry on the next poll.
type PublishFunc func(ctx context.Context, events []Event) error

// PublisherConfig configures the outbox publisher.
type PublisherConfig struct {
	// BatchSize is the maximum number of events per poll cycle (default 100).
	BatchSize int
	// PollInterval is the time between poll cycles (default 5s).
	PollInterval time.Duration
	// LockTTL is the lock lease duration (default 30s).
	// Must be significantly larger than PollInterval to avoid flapping.
	LockTTL time.Duration
	// HolderID uniquely identifies this publisher instance.
	// Default: hostname-pid-random.
	HolderID string
	// Logger for structured logging. Default: slog.Default().
	Logger *slog.Logger
}

func (c *PublisherConfig) withDefaults() {
	if c.BatchSize <= 0 {
		c.BatchSize = 100
	}
	if c.PollInterval <= 0 {
		c.PollInterval = 5 * time.Second
	}
	if c.LockTTL <= 0 {
		c.LockTTL = 30 * time.Second
	}
	if c.HolderID == "" {
		hostname, _ := os.Hostname()
		b := make([]byte, 4)
		_, _ = rand.Read(b)
		c.HolderID = fmt.Sprintf("%s-%d-%x", hostname, os.Getpid(), b)
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
}

// Publisher polls entity_events for unpublished rows and delivers them
// via a caller-provided PublishFunc. Only one publisher runs at a time
// across all instances, enforced by a TTL-based lock.
type Publisher struct {
	pool    *pgxpool.Pool
	queries *dbgen.Queries
	publish PublishFunc
	cfg     PublisherConfig
}

// NewPublisher creates an outbox publisher.
func NewPublisher(pool *pgxpool.Pool, fn PublishFunc, cfg PublisherConfig) *Publisher {
	cfg.withDefaults()
	return &Publisher{
		pool:    pool,
		queries: dbgen.New(pool),
		publish: fn,
		cfg:     cfg,
	}
}

// Run starts the publisher loop. It blocks until ctx is cancelled.
// On graceful shutdown (ctx cancelled), the lock is released.
func (p *Publisher) Run(ctx context.Context) error {
	p.cfg.Logger.InfoContext(ctx, "publisher starting",
		"holder_id", p.cfg.HolderID,
		"poll_interval", p.cfg.PollInterval,
		"lock_ttl", p.cfg.LockTTL,
		"batch_size", p.cfg.BatchSize,
	)

	defer p.releaseLock(context.Background()) //nolint:errcheck

	ticker := time.NewTicker(p.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			p.cfg.Logger.InfoContext(ctx, "publisher shutting down", "holder_id", p.cfg.HolderID)
			return ctx.Err()
		case <-ticker.C:
			if err := p.poll(ctx); err != nil {
				p.cfg.Logger.ErrorContext(ctx, "publisher poll error", "error", err)
			}
		}
	}
}

func (p *Publisher) poll(ctx context.Context) error {
	// Try to acquire or renew the lock.
	isHolder, err := p.tryAcquireOrRenew(ctx)
	if err != nil {
		return fmt.Errorf("lock: %w", err)
	}
	if !isHolder {
		return nil // Another instance holds the lock.
	}

	// We hold the lock — poll for unpublished events.
	return p.publishBatch(ctx)
}

func (p *Publisher) tryAcquireOrRenew(ctx context.Context) (bool, error) {
	ttl := pgIntervalFromDuration(p.cfg.LockTTL)

	// First try to renew (cheaper if we already hold the lock).
	tag, err := p.queries.RenewLock(ctx, dbgen.RenewLockParams{
		HolderID: p.cfg.HolderID,
		Ttl:      ttl,
	})
	if err != nil {
		return false, fmt.Errorf("renew lock: %w", err)
	}
	if tag.RowsAffected() > 0 {
		return true, nil // We renewed our existing lock.
	}

	// We don't hold the lock — try to acquire it.
	tag, err = p.queries.TryAcquireLock(ctx, dbgen.TryAcquireLockParams{
		HolderID: p.cfg.HolderID,
		Ttl:      ttl,
	})
	if err != nil {
		return false, fmt.Errorf("acquire lock: %w", err)
	}
	if tag.RowsAffected() > 0 {
		p.cfg.Logger.InfoContext(ctx, "acquired publisher lock", "holder_id", p.cfg.HolderID)
		return true, nil
	}

	return false, nil // Lock held by another instance and not expired.
}

func (p *Publisher) publishBatch(ctx context.Context) error {
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	q := p.queries.WithTx(tx)
	rows, err := q.GetUnpublishedEvents(ctx, int32(p.cfg.BatchSize))
	if err != nil {
		return fmt.Errorf("get unpublished: %w", err)
	}
	if len(rows) == 0 {
		return nil // Nothing to publish.
	}

	// Convert to Event slice for the PublishFunc.
	events := make([]Event, len(rows))
	ids := make([]uuid.UUID, len(rows))
	occurredAts := make([]time.Time, len(rows))
	for i, row := range rows {
		events[i] = eventFromRow(row)
		ids[i] = row.ID
		occurredAts[i] = row.OccurredAt
	}

	// Deliver to the caller's publish function.
	if err := p.publish(ctx, events); err != nil {
		p.cfg.Logger.WarnContext(ctx, "publish failed, will retry",
			"error", err, "batch_size", len(events))
		return nil // Rollback tx — events stay unpublished for next poll.
	}

	// Mark as published.
	if err := q.MarkEventsPublished(ctx, dbgen.MarkEventsPublishedParams{
		Ids:         ids,
		OccurredAts: occurredAts,
	}); err != nil {
		return fmt.Errorf("mark published: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	p.cfg.Logger.DebugContext(ctx, "published events", "count", len(events))
	return nil
}

func (p *Publisher) releaseLock(ctx context.Context) error {
	if err := p.queries.ReleaseLock(ctx, p.cfg.HolderID); err != nil {
		p.cfg.Logger.ErrorContext(ctx, "failed to release lock", "error", err)
		return err
	}
	p.cfg.Logger.InfoContext(ctx, "released publisher lock", "holder_id", p.cfg.HolderID)
	return nil
}

// pgIntervalFromDuration converts a Go duration to a pgtype.Interval.
func pgIntervalFromDuration(d time.Duration) pgtype.Interval {
	return pgtype.Interval{
		Microseconds: d.Microseconds(),
		Valid:        true,
	}
}
