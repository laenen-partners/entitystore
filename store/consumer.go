package store

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/laenen-partners/entitystore/store/internal/dbgen"
)

// ConsumerFunc receives a batch of events. Return nil to advance the cursor.
// Must be idempotent — events may be delivered more than once on error.
type ConsumerFunc func(ctx context.Context, events []Event) error

// ConsumerConfig configures a named event consumer.
type ConsumerConfig struct {
	// Name uniquely identifies this consumer (e.g. "notifier", "projector").
	Name string
	// BatchSize is the maximum number of events per cycle (default 100).
	BatchSize int
	// PollInterval is the time between poll cycles (default 5s).
	PollInterval time.Duration
	// LockTTL is the lock lease duration (default 30s).
	LockTTL time.Duration
	// HolderID uniquely identifies this instance. Default: hostname-pid-random.
	HolderID string
	// Logger for structured logging. Default: slog.Default().
	Logger *slog.Logger
}

func (c *ConsumerConfig) withDefaults() {
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

// Consumer reads entity events from a named cursor position.
// Multiple independent consumers can each track their own progress
// through the event stream. Only one instance of each named consumer
// runs at a time, enforced by a TTL-based lock.
type Consumer struct {
	pool    *pgxpool.Pool
	queries *dbgen.Queries
	process ConsumerFunc
	cfg     ConsumerConfig
}

// NewConsumer creates a named event consumer.
func NewConsumer(pool *pgxpool.Pool, fn ConsumerFunc, cfg ConsumerConfig) *Consumer {
	cfg.withDefaults()
	return &Consumer{
		pool:    pool,
		queries: dbgen.New(pool),
		process: fn,
		cfg:     cfg,
	}
}

// Run starts the consumer loop with polling. Blocks until ctx is cancelled.
func (c *Consumer) Run(ctx context.Context) error {
	c.cfg.Logger.InfoContext(ctx, "consumer starting",
		"name", c.cfg.Name,
		"holder_id", c.cfg.HolderID,
		"poll_interval", c.cfg.PollInterval,
	)
	defer c.releaseLock(context.Background())

	ticker := time.NewTicker(c.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			c.cfg.Logger.InfoContext(ctx, "consumer shutting down", "name", c.cfg.Name)
			return ctx.Err()
		case <-ticker.C:
			if err := c.poll(ctx); err != nil {
				c.cfg.Logger.ErrorContext(ctx, "consumer poll error", "name", c.cfg.Name, "error", err)
			}
		}
	}
}

// RunRealtime starts the consumer loop with LISTEN/NOTIFY + polling fallback.
// Wakes instantly on new events. Use for low-latency consumers (e.g. notifier).
func (c *Consumer) RunRealtime(ctx context.Context) error {
	c.cfg.Logger.InfoContext(ctx, "consumer starting (realtime)",
		"name", c.cfg.Name,
		"holder_id", c.cfg.HolderID,
		"poll_interval", c.cfg.PollInterval,
	)
	defer c.releaseLock(context.Background())

	for {
		select {
		case <-ctx.Done():
			c.cfg.Logger.InfoContext(ctx, "consumer shutting down", "name", c.cfg.Name)
			return ctx.Err()
		default:
		}

		// Block until NOTIFY or timeout.
		_ = c.listen(ctx, c.cfg.PollInterval)

		if err := c.poll(ctx); err != nil {
			c.cfg.Logger.ErrorContext(ctx, "consumer poll error", "name", c.cfg.Name, "error", err)
		}
	}
}

// listen acquires a connection, LISTENs for notifications, and blocks until
// a notification arrives or the timeout expires. The connection is returned
// to the pool after each call.
func (c *Consumer) listen(ctx context.Context, timeout time.Duration) error {
	conn, err := c.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire conn for listen: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "LISTEN entity_events"); err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	_, err = conn.Conn().WaitForNotification(waitCtx)
	if err != nil && ctx.Err() == nil {
		// Timeout is expected — not an error.
		return nil
	}
	return err
}

func (c *Consumer) poll(ctx context.Context) error {
	isHolder, err := c.tryAcquireOrRenew(ctx)
	if err != nil {
		return fmt.Errorf("lock: %w", err)
	}
	if !isHolder {
		return nil
	}

	return c.processBatch(ctx)
}

func (c *Consumer) tryAcquireOrRenew(ctx context.Context) (bool, error) {
	ttl := pgIntervalFromDuration(c.cfg.LockTTL)

	// Try to renew first (cheaper).
	tag, err := c.queries.RenewConsumerLock(ctx, dbgen.RenewConsumerLockParams{
		Name:     c.cfg.Name,
		HolderID: pgtype.Text{String: c.cfg.HolderID, Valid: true},
		Ttl:      ttl,
	})
	if err != nil {
		return false, fmt.Errorf("renew lock: %w", err)
	}
	if tag.RowsAffected() > 0 {
		return true, nil
	}

	// Try to acquire.
	tag, err = c.queries.TryAcquireConsumerLock(ctx, dbgen.TryAcquireConsumerLockParams{
		Name:     c.cfg.Name,
		HolderID: pgtype.Text{String: c.cfg.HolderID, Valid: true},
		Ttl:      ttl,
	})
	if err != nil {
		return false, fmt.Errorf("acquire lock: %w", err)
	}
	if tag.RowsAffected() > 0 {
		c.cfg.Logger.InfoContext(ctx, "acquired consumer lock",
			"name", c.cfg.Name, "holder_id", c.cfg.HolderID)
		return true, nil
	}

	return false, nil
}

func (c *Consumer) processBatch(ctx context.Context) error {
	// Read cursor.
	cursor, err := c.queries.GetConsumerCursor(ctx, c.cfg.Name)
	if err != nil {
		return fmt.Errorf("get cursor: %w", err)
	}

	afterAt := cursor.LastEventAt
	var afterID uuid.UUID
	if cursor.LastEventID.Valid {
		afterID = uuid.UUID(cursor.LastEventID.Bytes)
	}

	// Fetch events after cursor.
	rows, err := c.queries.GetEventsAfterCursor(ctx, dbgen.GetEventsAfterCursorParams{
		AfterAt:   afterAt,
		AfterID:   afterID,
		BatchSize: int32(c.cfg.BatchSize),
	})
	if err != nil {
		return fmt.Errorf("get events: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}

	// Convert to Event slice.
	events := make([]Event, len(rows))
	for i, row := range rows {
		events[i] = eventFromRow(row)
	}

	// Deliver to the consumer function.
	if err := c.process(ctx, events); err != nil {
		c.cfg.Logger.WarnContext(ctx, "consumer process failed, will retry",
			"name", c.cfg.Name, "error", err, "batch_size", len(events))
		return nil // Don't advance cursor — retry next poll.
	}

	// Advance cursor to last processed event.
	last := rows[len(rows)-1]
	if err := c.queries.AdvanceConsumerCursor(ctx, dbgen.AdvanceConsumerCursorParams{
		Name:        c.cfg.Name,
		HolderID:    pgtype.Text{String: c.cfg.HolderID, Valid: true},
		LastEventAt: last.OccurredAt,
		LastEventID: pgtype.UUID{Bytes: last.ID, Valid: true},
	}); err != nil {
		return fmt.Errorf("advance cursor: %w", err)
	}

	c.cfg.Logger.DebugContext(ctx, "consumer processed events",
		"name", c.cfg.Name, "count", len(events))
	return nil
}

func (c *Consumer) releaseLock(ctx context.Context) {
	if err := c.queries.ReleaseConsumerLock(ctx, dbgen.ReleaseConsumerLockParams{
		Name:     c.cfg.Name,
		HolderID: pgtype.Text{String: c.cfg.HolderID, Valid: true},
	}); err != nil {
		c.cfg.Logger.ErrorContext(ctx, "failed to release consumer lock",
			"name", c.cfg.Name, "error", err)
		return
	}
	c.cfg.Logger.InfoContext(ctx, "released consumer lock",
		"name", c.cfg.Name, "holder_id", c.cfg.HolderID)
}

// ConsumerHealth reports the status of a named consumer.
type ConsumerHealth struct {
	Name          string     `json:"name"`
	LastEventAt   time.Time  `json:"last_event_at"`
	Lag           string     `json:"lag"`
	HolderID      string     `json:"holder_id,omitempty"`
	LockExpiresAt *time.Time `json:"lock_expires_at,omitempty"`
}
