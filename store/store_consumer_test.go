package store_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/laenen-partners/entitystore/matching"
	"github.com/laenen-partners/entitystore/store"
)

// createEntityForConsumer creates a test entity and returns its ID.
func createEntityForConsumer(t *testing.T, s *store.Store) string {
	t.Helper()
	ctx := context.Background()
	results, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action:     store.WriteActionCreate,
			Data:       testData(t, map[string]any{"name": "consumer-test"}),
			Confidence: 0.9,
			Anchors:    []matching.AnchorQuery{{Field: "ref", Value: time.Now().String()}},
		}},
	})
	if err != nil {
		t.Fatalf("create entity: %v", err)
	}
	return results[0].Entity.ID
}

func TestConsumer_ProcessesEvents(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	// Create an entity to generate an event.
	createEntityForConsumer(t, s)

	var processed atomic.Int32
	consumer := store.NewConsumer(s.Pool(), func(ctx context.Context, events []store.Event) error {
		processed.Add(int32(len(events)))
		return nil
	}, store.ConsumerConfig{
		Name:         "test-basic-" + time.Now().Format("150405.000"),
		BatchSize:    100,
		PollInterval: 100 * time.Millisecond,
	})

	runCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	_ = consumer.Run(runCtx)

	if processed.Load() == 0 {
		t.Error("expected consumer to process at least one event")
	}
}

func TestConsumer_BackoffOnFailure(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	createEntityForConsumer(t, s)

	var attempts atomic.Int32
	consumer := store.NewConsumer(s.Pool(), func(ctx context.Context, events []store.Event) error {
		attempts.Add(1)
		return errors.New("simulated failure")
	}, store.ConsumerConfig{
		Name:           "test-backoff-" + time.Now().Format("150405.000"),
		BatchSize:      100,
		PollInterval:   50 * time.Millisecond,
		InitialBackoff: 200 * time.Millisecond,
		MaxBackoff:     1 * time.Second,
	})

	runCtx, cancel := context.WithTimeout(ctx, 600*time.Millisecond)
	defer cancel()
	_ = consumer.Run(runCtx)

	// With 200ms initial backoff and 600ms runtime, we expect ~2-3 attempts
	// (first immediate, then 200ms backoff, then 400ms backoff).
	// Without backoff at 50ms poll, we'd get ~12 attempts.
	a := attempts.Load()
	if a > 5 {
		t.Errorf("expected backoff to limit attempts, got %d", a)
	}
	if a == 0 {
		t.Error("expected at least one attempt")
	}
}

func TestConsumer_DeadLetterAfterMaxRetries(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	createEntityForConsumer(t, s)

	consumerName := "test-deadletter-" + time.Now().Format("150405.000")
	var deadLettered atomic.Bool

	consumer := store.NewConsumer(s.Pool(), func(ctx context.Context, events []store.Event) error {
		return errors.New("permanent failure")
	}, store.ConsumerConfig{
		Name:         consumerName,
		BatchSize:    100,
		PollInterval: 50 * time.Millisecond,
		MaxRetries:   3,
		OnDeadLetter: func(name string, events []store.Event, err error) {
			deadLettered.Store(true)
		},
	})

	runCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()
	_ = consumer.Run(runCtx)

	if !deadLettered.Load() {
		t.Error("expected OnDeadLetter callback to fire")
	}

	// Check dead letters in the table.
	health, _ := s.Health(ctx)
	for _, ch := range health.Consumers {
		if ch.Name == consumerName {
			if ch.DeadLetterCount == 0 {
				t.Error("expected dead letter count > 0")
			}
			return
		}
	}
}

func TestConsumer_DeadLetterUnblocksConsumer(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	// Create two entities = two events.
	createEntityForConsumer(t, s)
	createEntityForConsumer(t, s)

	consumerName := "test-unblock-" + time.Now().Format("150405.000")
	var callCount atomic.Int32

	consumer := store.NewConsumer(s.Pool(), func(ctx context.Context, events []store.Event) error {
		n := callCount.Add(1)
		if n <= 3 {
			return errors.New("fail first 3 tries")
		}
		return nil // succeed after dead-lettering
	}, store.ConsumerConfig{
		Name:         consumerName,
		BatchSize:    100,
		PollInterval: 50 * time.Millisecond,
		MaxRetries:   3,
	})

	runCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()
	_ = consumer.Run(runCtx)

	// After 3 failures, batch is dead-lettered and cursor advances.
	// The consumer was unblocked (didn't hang on the same batch forever).
	if callCount.Load() < 3 {
		t.Errorf("expected at least 3 attempts before dead-lettering, got %d", callCount.Load())
	}

	// Verify dead letters exist.
	health, _ := s.Health(ctx)
	for _, ch := range health.Consumers {
		if ch.Name == consumerName {
			if ch.DeadLetterCount == 0 {
				t.Error("expected dead letters after max retries")
			}
			return
		}
	}
}

func TestConsumer_ReplayDeadLetters(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	createEntityForConsumer(t, s)

	consumerName := "test-replay-" + time.Now().Format("150405.000")
	var shouldFail atomic.Bool
	shouldFail.Store(true)

	consumer := store.NewConsumer(s.Pool(), func(ctx context.Context, events []store.Event) error {
		if shouldFail.Load() {
			return errors.New("fail")
		}
		return nil
	}, store.ConsumerConfig{
		Name:         consumerName,
		BatchSize:    100,
		PollInterval: 50 * time.Millisecond,
		MaxRetries:   2,
	})

	// Run until dead letters are created.
	runCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	_ = consumer.Run(runCtx)

	// Now fix the "failure" and replay.
	shouldFail.Store(false)
	replayed, failed, err := consumer.ReplayDeadLetters(ctx, nil)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if replayed == 0 {
		t.Error("expected at least one replayed event")
	}
	if failed != 0 {
		t.Errorf("expected 0 failed, got %d", failed)
	}
}

func TestConsumer_HealthState(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	createEntityForConsumer(t, s)

	consumerName := "test-health-" + time.Now().Format("150405.000")

	// Run a consumer that fails — check health state.
	consumer := store.NewConsumer(s.Pool(), func(ctx context.Context, events []store.Event) error {
		return errors.New("test error")
	}, store.ConsumerConfig{
		Name:         consumerName,
		BatchSize:    100,
		PollInterval: 50 * time.Millisecond,
		MaxRetries:   5,
	})

	runCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	_ = consumer.Run(runCtx)

	health, _ := s.Health(ctx)
	for _, ch := range health.Consumers {
		if ch.Name == consumerName {
			if ch.State == "healthy" {
				t.Error("expected state 'degraded' or 'failing', got 'healthy'")
			}
			// State should be either degraded (still retrying) or failing (dead-lettered).
			if ch.State != "degraded" && ch.State != "failing" {
				t.Errorf("expected state 'degraded' or 'failing', got %q", ch.State)
			}
			return
		}
	}
	t.Error("consumer not found in health")
}

func TestConsumer_ZeroConfigPreservesOldBehaviour(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	createEntityForConsumer(t, s)

	// Zero MaxRetries + zero InitialBackoff = retry forever, no backoff.
	var attempts atomic.Int32
	consumer := store.NewConsumer(s.Pool(), func(ctx context.Context, events []store.Event) error {
		attempts.Add(1)
		return errors.New("always fail")
	}, store.ConsumerConfig{
		Name:         "test-compat-" + time.Now().Format("150405.000"),
		BatchSize:    100,
		PollInterval: 50 * time.Millisecond,
		// MaxRetries: 0 (default — no dead letter)
		// InitialBackoff: 0 (default — no backoff)
	})

	runCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	_ = consumer.Run(runCtx)

	// Without backoff, should attempt many times in 300ms at 50ms intervals.
	if attempts.Load() < 3 {
		t.Errorf("expected multiple rapid retries, got %d", attempts.Load())
	}
}

func TestConsumer_PurgeDeadLetters(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	createEntityForConsumer(t, s)

	consumerName := "test-purge-" + time.Now().Format("150405.000")

	consumer := store.NewConsumer(s.Pool(), func(ctx context.Context, events []store.Event) error {
		return errors.New("fail")
	}, store.ConsumerConfig{
		Name:         consumerName,
		BatchSize:    100,
		PollInterval: 50 * time.Millisecond,
		MaxRetries:   1,
	})

	runCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	_ = consumer.Run(runCtx)

	// Purge dead letters older than 0 (all of them).
	err := consumer.PurgeDeadLetters(ctx, 0)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}

	// Verify count is 0.
	health, _ := s.Health(ctx)
	for _, ch := range health.Consumers {
		if ch.Name == consumerName && ch.DeadLetterCount != 0 {
			t.Errorf("expected 0 dead letters after purge, got %d", ch.DeadLetterCount)
		}
	}
}
