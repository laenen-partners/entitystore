package store

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"math"
	mrand "math/rand"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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

	// MaxRetries is the number of consecutive times a batch can fail before
	// its events are written to the dead letter table and the cursor advances.
	// Default: 0 (disabled — retry forever, preserving previous behaviour).
	MaxRetries int

	// InitialBackoff is the wait duration after the first failure.
	// Subsequent failures double the backoff up to MaxBackoff.
	// Default: 0 (disabled — no backoff).
	InitialBackoff time.Duration

	// MaxBackoff caps the exponential backoff duration.
	// Default: 5 minutes.
	MaxBackoff time.Duration

	// OnDeadLetter is called when events are moved to the dead letter table.
	// Use for alerting (Slack, PagerDuty, etc.). Optional.
	OnDeadLetter func(consumerName string, events []Event, err error)
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
	if c.MaxBackoff <= 0 && c.InitialBackoff > 0 {
		c.MaxBackoff = 5 * time.Minute
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
type Consumer struct {
	pool    *pgxpool.Pool
	queries *dbgen.Queries
	process ConsumerFunc
	cfg     ConsumerConfig

	// In-memory failure tracking (also persisted to DB).
	consecutiveFailures int
	backoffUntil        time.Time
	lastErr             error
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
		"max_retries", c.cfg.MaxRetries,
		"initial_backoff", c.cfg.InitialBackoff,
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
func (c *Consumer) RunRealtime(ctx context.Context) error {
	c.cfg.Logger.InfoContext(ctx, "consumer starting (realtime)",
		"name", c.cfg.Name,
		"holder_id", c.cfg.HolderID,
		"poll_interval", c.cfg.PollInterval,
		"max_retries", c.cfg.MaxRetries,
	)
	defer c.releaseLock(context.Background())

	for {
		select {
		case <-ctx.Done():
			c.cfg.Logger.InfoContext(ctx, "consumer shutting down", "name", c.cfg.Name)
			return ctx.Err()
		default:
		}

		// If in backoff, sleep until backoff expires instead of listening.
		if time.Now().Before(c.backoffUntil) {
			sleepDuration := time.Until(c.backoffUntil)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(sleepDuration):
			}
		}

		_ = c.listen(ctx, c.cfg.PollInterval)

		if err := c.poll(ctx); err != nil {
			c.cfg.Logger.ErrorContext(ctx, "consumer poll error", "name", c.cfg.Name, "error", err)
		}
	}
}

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
		return nil // timeout expected
	}
	return err
}

func (c *Consumer) poll(ctx context.Context) error {
	// Backoff check for polling mode.
	if time.Now().Before(c.backoffUntil) {
		return nil
	}

	isHolder, err := c.tryAcquireOrRenew(ctx)
	if err != nil {
		return fmt.Errorf("lock: %w", err)
	}
	if !isHolder {
		return nil
	}

	err = c.processBatch(ctx)
	if err == nil {
		// Success — reset failure state.
		if c.consecutiveFailures > 0 {
			c.consecutiveFailures = 0
			c.backoffUntil = time.Time{}
			c.lastErr = nil
			c.persistFailureState(ctx, 0, "", nil)
		}
		return nil
	}

	// Failure path.
	c.consecutiveFailures++
	c.lastErr = err

	// Check if we should dead-letter.
	if c.cfg.MaxRetries > 0 && c.consecutiveFailures >= c.cfg.MaxRetries {
		c.deadLetterCurrentBatch(ctx, err)
		c.consecutiveFailures = 0
		c.backoffUntil = time.Time{}
		c.lastErr = nil
		c.persistFailureState(ctx, 0, "", nil)
		return nil
	}

	// Apply exponential backoff with jitter.
	if c.cfg.InitialBackoff > 0 {
		exp := math.Min(float64(c.consecutiveFailures-1), 10)
		backoff := time.Duration(float64(c.cfg.InitialBackoff) * math.Pow(2, exp))
		if c.cfg.MaxBackoff > 0 && backoff > c.cfg.MaxBackoff {
			backoff = c.cfg.MaxBackoff
		}
		// Add ±10% jitter.
		jitter := time.Duration(mrand.Int63n(int64(backoff / 5)))
		backoff = backoff - backoff/10 + jitter
		c.backoffUntil = time.Now().Add(backoff)

		c.cfg.Logger.WarnContext(ctx, "consumer backing off",
			"name", c.cfg.Name,
			"consecutive_failures", c.consecutiveFailures,
			"backoff", backoff,
			"error", err,
		)
	}

	c.persistFailureState(ctx, c.consecutiveFailures, err.Error(), &c.backoffUntil)
	return nil
}

func (c *Consumer) persistFailureState(ctx context.Context, failures int, errMsg string, backoffUntil *time.Time) {
	var lastError pgtype.Text
	if errMsg != "" {
		lastError = pgtype.Text{String: errMsg, Valid: true}
	}
	var bo pgtype.Timestamptz
	if backoffUntil != nil && !backoffUntil.IsZero() {
		bo = pgtype.Timestamptz{Time: *backoffUntil, Valid: true}
	}
	_ = c.queries.UpdateConsumerFailureState(ctx, dbgen.UpdateConsumerFailureStateParams{
		Name:                c.cfg.Name,
		HolderID:            pgtype.Text{String: c.cfg.HolderID, Valid: true},
		ConsecutiveFailures: int32(failures),
		LastError:           lastError,
		BackoffUntil:        bo,
	})
}

func (c *Consumer) tryAcquireOrRenew(ctx context.Context) (bool, error) {
	ttl := pgIntervalFromDuration(c.cfg.LockTTL)

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
	cursor, err := c.queries.GetConsumerCursor(ctx, c.cfg.Name)
	if err != nil {
		return fmt.Errorf("get cursor: %w", err)
	}

	afterAt := cursor.LastEventAt
	var afterID uuid.UUID
	if cursor.LastEventID.Valid {
		afterID = uuid.UUID(cursor.LastEventID.Bytes)
	}

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

	events := make([]Event, len(rows))
	for i, row := range rows {
		events[i] = eventFromRow(row)
	}

	if err := c.process(ctx, events); err != nil {
		c.cfg.Logger.WarnContext(ctx, "consumer process failed, will retry",
			"name", c.cfg.Name, "error", err, "batch_size", len(events))
		return err // Return error to trigger backoff/dead-letter logic.
	}

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

func (c *Consumer) deadLetterCurrentBatch(ctx context.Context, lastErr error) {
	cursor, err := c.queries.GetConsumerCursor(ctx, c.cfg.Name)
	if err != nil {
		c.cfg.Logger.ErrorContext(ctx, "dead letter: get cursor failed", "error", err)
		return
	}

	var afterID uuid.UUID
	if cursor.LastEventID.Valid {
		afterID = uuid.UUID(cursor.LastEventID.Bytes)
	}

	rows, err := c.queries.GetEventsAfterCursor(ctx, dbgen.GetEventsAfterCursorParams{
		AfterAt:   cursor.LastEventAt,
		AfterID:   afterID,
		BatchSize: int32(c.cfg.BatchSize),
	})
	if err != nil || len(rows) == 0 {
		return
	}

	// Transactional: dead letter inserts + cursor advance in one tx.
	tx, err := c.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		c.cfg.Logger.ErrorContext(ctx, "dead letter: begin tx failed", "error", err)
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	q := c.queries.WithTx(tx)

	for _, row := range rows {
		var entityID string
		if row.EntityID.Valid {
			entityID = uuid.UUID(row.EntityID.Bytes).String()
		}
		_ = q.InsertDeadLetter(ctx, dbgen.InsertDeadLetterParams{
			ConsumerName: c.cfg.Name,
			EventID:      row.ID,
			EventType:    row.EventType,
			PayloadType:  row.PayloadType,
			EntityID:     pgtype.Text{String: entityID, Valid: entityID != ""},
			Payload:      row.Payload,
			ErrorMessage: lastErr.Error(),
			RetryCount:   int32(c.consecutiveFailures),
		})
	}

	last := rows[len(rows)-1]
	_ = q.AdvanceConsumerCursor(ctx, dbgen.AdvanceConsumerCursorParams{
		Name:        c.cfg.Name,
		HolderID:    pgtype.Text{String: c.cfg.HolderID, Valid: true},
		LastEventAt: last.OccurredAt,
		LastEventID: pgtype.UUID{Bytes: last.ID, Valid: true},
	})

	if err := tx.Commit(ctx); err != nil {
		c.cfg.Logger.ErrorContext(ctx, "dead letter: commit failed", "error", err)
		return
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

// ReplayOpts configures dead letter replay.
type ReplayOpts struct {
	Limit      int      // default: 100
	EventTypes []string // empty = all
}

// ReplayDeadLetters re-processes dead-lettered events for this consumer.
// Successfully processed events are removed from the dead letter table.
// Events that still fail remain with an updated retry_count.
func (c *Consumer) ReplayDeadLetters(ctx context.Context, opts *ReplayOpts) (replayed, failed int, err error) {
	limit := 100
	var eventTypes []string
	if opts != nil {
		if opts.Limit > 0 {
			limit = opts.Limit
		}
		eventTypes = opts.EventTypes
	}
	if eventTypes == nil {
		eventTypes = []string{}
	}

	letters, err := c.queries.ListDeadLetters(ctx, dbgen.ListDeadLettersParams{
		ConsumerName: c.cfg.Name,
		EventTypes:   eventTypes,
		MaxResults:   int32(limit),
	})
	if err != nil {
		return 0, 0, fmt.Errorf("list dead letters: %w", err)
	}

	for _, dl := range letters {
		evt := Event{
			ID:          dl.EventID.String(),
			EventType:   dl.EventType,
			PayloadType: dl.PayloadType,
			RawPayload:  dl.Payload,
		}
		if dl.EntityID.Valid {
			evt.EntityID = dl.EntityID.String
		}

		if err := c.process(ctx, []Event{evt}); err != nil {
			failed++
			c.cfg.Logger.WarnContext(ctx, "dead letter replay failed",
				"name", c.cfg.Name, "event_id", dl.EventID, "error", err)
			continue
		}

		_ = c.queries.DeleteDeadLetter(ctx, dl.ID)
		replayed++
	}

	c.cfg.Logger.InfoContext(ctx, "dead letter replay complete",
		"name", c.cfg.Name, "replayed", replayed, "failed", failed)
	return replayed, failed, nil
}

// PurgeDeadLetters removes dead letters older than the given duration.
func (c *Consumer) PurgeDeadLetters(ctx context.Context, olderThan time.Duration) error {
	return c.queries.PurgeOldDeadLetters(ctx, time.Now().Add(-olderThan))
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

// pgIntervalFromDuration converts a Go duration to a pgtype.Interval.
func pgIntervalFromDuration(d time.Duration) pgtype.Interval {
	return pgtype.Interval{
		Microseconds: d.Microseconds(),
		Valid:        true,
	}
}

// ConsumerHealth reports the status of a named consumer.
type ConsumerHealth struct {
	Name                string     `json:"name"`
	LastEventAt         time.Time  `json:"last_event_at"`
	Lag                 string     `json:"lag"`
	HolderID            string     `json:"holder_id,omitempty"`
	LockExpiresAt       *time.Time `json:"lock_expires_at,omitempty"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	LastError           string     `json:"last_error,omitempty"`
	BackoffUntil        *time.Time `json:"backoff_until,omitempty"`
	DeadLetterCount     int64      `json:"dead_letter_count"`
	State               string     `json:"state"` // "healthy", "degraded", "failing"
}
