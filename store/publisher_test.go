package store_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/laenen-partners/entitystore/store"
)

func createEntityForPublisher(t *testing.T, s *store.Store) string {
	t.Helper()
	ctx := context.Background()
	results, err := s.BatchWrite(ctx, []store.BatchWriteOp{
		{WriteEntity: &store.WriteEntityOp{
			Action:     store.WriteActionCreate,
			Data:       testData(t, map[string]any{"name": "pub-test"}),
			Confidence: 0.9,
		}},
	})
	if err != nil {
		t.Fatalf("create entity: %v", err)
	}
	return results[0].Entity.ID
}

func TestPublisher_PublishesUnpublishedEvents(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	// Create an entity — this emits an EntityCreated event.
	id := createEntityForPublisher(t, s)

	// Verify event is unpublished.
	events, err := s.GetEventsForEntity(ctx, id, nil)
	if err != nil {
		t.Fatalf("get events: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("expected at least 1 event")
	}
	if events[0].PublishedAt != nil {
		t.Fatal("event should be unpublished initially")
	}

	// Set up publisher with a channel to collect published events.
	var published []store.Event
	var mu sync.Mutex

	pub := store.NewPublisher(s.Pool(), func(ctx context.Context, evts []store.Event) error {
		mu.Lock()
		defer mu.Unlock()
		published = append(published, evts...)
		return nil
	}, store.PublisherConfig{
		BatchSize:    100,
		PollInterval: 100 * time.Millisecond,
		LockTTL:      5 * time.Second,
		HolderID:     "test-publisher-1",
	})

	// Run publisher briefly.
	pubCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	_ = pub.Run(pubCtx)

	// Verify events were published.
	mu.Lock()
	count := len(published)
	mu.Unlock()

	if count == 0 {
		t.Fatal("expected published events, got 0")
	}

	// Verify event is now marked as published in DB.
	events, err = s.GetEventsForEntity(ctx, id, nil)
	if err != nil {
		t.Fatalf("get events after publish: %v", err)
	}
	for _, evt := range events {
		if evt.PublishedAt == nil {
			t.Errorf("event %s should be marked as published", evt.ID)
		}
	}
}

func TestPublisher_RetriesOnError(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	createEntityForPublisher(t, s)

	var callCount atomic.Int32

	pub := store.NewPublisher(s.Pool(), func(ctx context.Context, evts []store.Event) error {
		n := callCount.Add(1)
		if n == 1 {
			return errors.New("transient failure")
		}
		return nil // Second call succeeds.
	}, store.PublisherConfig{
		BatchSize:    100,
		PollInterval: 100 * time.Millisecond,
		LockTTL:      5 * time.Second,
		HolderID:     "test-publisher-retry",
	})

	pubCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	_ = pub.Run(pubCtx)

	calls := callCount.Load()
	if calls < 2 {
		t.Errorf("expected at least 2 publish calls (1 fail + 1 success), got %d", calls)
	}
}

func TestPublisher_SingleLeader(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	createEntityForPublisher(t, s)

	var pub1Count, pub2Count atomic.Int32

	pub1 := store.NewPublisher(s.Pool(), func(ctx context.Context, evts []store.Event) error {
		pub1Count.Add(int32(len(evts)))
		return nil
	}, store.PublisherConfig{
		BatchSize:    100,
		PollInterval: 100 * time.Millisecond,
		LockTTL:      5 * time.Second,
		HolderID:     "leader-1",
	})

	pub2 := store.NewPublisher(s.Pool(), func(ctx context.Context, evts []store.Event) error {
		pub2Count.Add(int32(len(evts)))
		return nil
	}, store.PublisherConfig{
		BatchSize:    100,
		PollInterval: 100 * time.Millisecond,
		LockTTL:      5 * time.Second,
		HolderID:     "leader-2",
	})

	// Run both publishers concurrently.
	pubCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _ = pub1.Run(pubCtx) }()
	go func() { defer wg.Done(); _ = pub2.Run(pubCtx) }()
	wg.Wait()

	c1 := pub1Count.Load()
	c2 := pub2Count.Load()

	// Only one publisher should have delivered events.
	// (The other should have failed to acquire the lock.)
	if c1 > 0 && c2 > 0 {
		t.Errorf("both publishers delivered events (pub1=%d, pub2=%d); expected only one leader", c1, c2)
	}
	if c1 == 0 && c2 == 0 {
		t.Error("neither publisher delivered events")
	}
}

func TestPublisher_LockExpiresOnCrash(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	createEntityForPublisher(t, s)

	// Publisher 1 acquires the lock with a short TTL then stops.
	pub1 := store.NewPublisher(s.Pool(), func(ctx context.Context, evts []store.Event) error {
		return nil
	}, store.PublisherConfig{
		BatchSize:    100,
		PollInterval: 50 * time.Millisecond,
		LockTTL:      200 * time.Millisecond, // Short TTL.
		HolderID:     "crash-holder",
	})

	// Run pub1 briefly then stop (simulates crash).
	pub1Ctx, cancel1 := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancel1()
	_ = pub1.Run(pub1Ctx)

	// Create a new event after pub1 stops.
	createEntityForPublisher(t, s)

	// Wait for the lock to expire.
	time.Sleep(300 * time.Millisecond)

	// Publisher 2 should be able to acquire the expired lock.
	var pub2Published atomic.Int32
	pub2 := store.NewPublisher(s.Pool(), func(ctx context.Context, evts []store.Event) error {
		pub2Published.Add(int32(len(evts)))
		return nil
	}, store.PublisherConfig{
		BatchSize:    100,
		PollInterval: 100 * time.Millisecond,
		LockTTL:      5 * time.Second,
		HolderID:     "takeover-holder",
	})

	pub2Ctx, cancel2 := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel2()
	_ = pub2.Run(pub2Ctx)

	if pub2Published.Load() == 0 {
		t.Error("publisher 2 should have taken over and published events")
	}
}

func TestPublisher_NothingToPublish(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	// Don't create any entities — no events to publish.
	var callCount atomic.Int32

	pub := store.NewPublisher(s.Pool(), func(ctx context.Context, evts []store.Event) error {
		callCount.Add(1)
		return nil
	}, store.PublisherConfig{
		BatchSize:    100,
		PollInterval: 100 * time.Millisecond,
		LockTTL:      5 * time.Second,
		HolderID:     "empty-publisher",
	})

	pubCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	_ = pub.Run(pubCtx)

	// PublishFunc should never be called when there are no events.
	if callCount.Load() > 0 {
		t.Error("PublishFunc should not be called when there are no unpublished events")
	}
}

func TestPublisher_GracefulShutdown(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	pub := store.NewPublisher(s.Pool(), func(ctx context.Context, evts []store.Event) error {
		return nil
	}, store.PublisherConfig{
		BatchSize:    100,
		PollInterval: 50 * time.Millisecond,
		LockTTL:      5 * time.Second,
		HolderID:     "graceful-publisher",
	})

	// Run and cancel immediately — should exit without error (other than context).
	pubCtx, cancel := context.WithCancel(ctx)

	done := make(chan error, 1)
	go func() { done <- pub.Run(pubCtx) }()

	// Let it run one cycle.
	time.Sleep(100 * time.Millisecond)
	cancel()

	err := <-done
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}
