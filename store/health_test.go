package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/laenen-partners/entitystore/store"
)

func TestHealth_DBHealthy(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	status, err := s.Health(ctx)
	if err != nil {
		t.Fatalf("health: %v", err)
	}

	if !status.DB.OK {
		t.Errorf("DB.OK = false, want true; error: %s", status.DB.Error)
	}
	if status.DB.Latency <= 0 {
		t.Error("DB.Latency should be > 0")
	}
	if status.DB.MaxConns <= 0 {
		t.Error("DB.MaxConns should be > 0")
	}
}

func TestHealth_EventActivity(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	// Create an entity to generate an event.
	createEntityForPublisher(t, s)

	status, err := s.Health(ctx)
	if err != nil {
		t.Fatalf("health: %v", err)
	}

	if status.Events.LastEventAt == nil {
		t.Error("Events.LastEventAt should not be nil after creating an entity")
	}
	if status.Events.UnpublishedCount == 0 {
		t.Error("Events.UnpublishedCount should be > 0 before publishing")
	}
}

func TestHealth_PublisherStatus(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	createEntityForPublisher(t, s)

	// Before any publisher runs, Publisher should be nil.
	status, err := s.Health(ctx)
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if status.Publisher != nil {
		t.Error("Publisher should be nil before any publisher runs")
	}

	// Run a publisher briefly.
	pub := store.NewPublisher(s.Pool(), func(ctx context.Context, evts []store.Event) error {
		return nil
	}, store.PublisherConfig{
		BatchSize:    100,
		PollInterval: 100 * time.Millisecond,
		LockTTL:      5 * time.Second,
		HolderID:     "health-test-publisher",
	})

	pubCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	_ = pub.Run(pubCtx)

	// After publisher ran, we should see publisher health.
	// Note: the lock was released on graceful shutdown, so IsLeader may be false.
	// But LastPublishAt should be set.
	status, err = s.Health(ctx)
	if err != nil {
		t.Fatalf("health after publish: %v", err)
	}

	// Events should now be published.
	if status.Events.UnpublishedCount != 0 {
		t.Errorf("UnpublishedCount = %d, want 0 after publishing", status.Events.UnpublishedCount)
	}
}

func TestHealth_Healthy(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	status, err := s.Health(ctx)
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if !status.Healthy() {
		t.Error("Healthy() should return true when DB is OK")
	}
}

func TestHealthError(t *testing.T) {
	s := sharedTestStore(t)
	ctx := context.Background()

	if err := s.HealthError(ctx); err != nil {
		t.Errorf("HealthError should return nil for healthy store: %v", err)
	}
}
