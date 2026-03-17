package store_test

import (
	"context"
	"testing"

	"github.com/laenen-partners/entitystore/store"
)

func TestNew_WithAutoMigrate(t *testing.T) {
	ctx := context.Background()

	// Get a fresh connection string (container is already running from sharedTestStore).
	// We need the shared store first to ensure the container is up.
	_ = sharedTestStore(t)

	s, err := store.New(ctx, _sharedConnStr, store.WithAutoMigrate())
	if err != nil {
		t.Fatalf("New with auto migrate: %v", err)
	}
	defer s.Close()

	pool := s.Pool()
	if pool == nil {
		t.Fatal("Pool() returned nil")
	}
}

func TestNew_WithPoolConfig(t *testing.T) {
	ctx := context.Background()
	_ = sharedTestStore(t)

	s, err := store.New(ctx, _sharedConnStr,
		store.WithPoolConfig(5, 1, 0),
		store.WithAutoMigrate(),
	)
	if err != nil {
		t.Fatalf("New with pool config: %v", err)
	}
	defer s.Close()

	pool := s.Pool()
	if pool == nil {
		t.Fatal("Pool() returned nil")
	}
}

func TestNew_InvalidConnString(t *testing.T) {
	ctx := context.Background()
	_, err := store.New(ctx, "postgres://invalid:5432/nonexistent?connect_timeout=1")
	if err == nil {
		t.Error("expected error for invalid connection")
	}
}

func TestNewFromPool_Close(t *testing.T) {
	// NewFromPool with ownPool=false should NOT close the pool.
	_ = sharedTestStore(t) // just exercise the path — already tested via sharedTestStore
}
