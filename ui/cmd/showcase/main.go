// EntityStore Explorer showcase — standalone demo with seed data.
//
// Prerequisites: docker compose up -d (from repo root)
// Run with: go run ./ui/cmd/showcase
// Then open http://localhost:3336
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/laenen-partners/entitystore"
	"github.com/laenen-partners/entitystore/ui/cmd/showcase/seed"
	"github.com/laenen-partners/entitystore/ui/explorer"
)

const defaultDSN = "postgres://showcase:showcase@localhost:5489/entitystore_showcase?sslmode=disable"

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))

	ctx := context.Background()
	es, pool, err := setupStore(ctx)
	if err != nil {
		slog.Error("setup failed", "error", err)
		os.Exit(1)
	}

	// Seed sample data.
	if err := seed.Run(ctx, pool, es); err != nil {
		slog.Error("seed failed", "error", err)
		os.Exit(1)
	}
	slog.Info("seed migrations complete")

	if err := explorer.Run(explorer.Config{
		Store: es,
		Port:  3336,
	}); err != nil {
		slog.Error("explorer failed", "error", err)
		os.Exit(1)
	}
}

func setupStore(ctx context.Context) (*entitystore.EntityStore, *pgxpool.Pool, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = defaultDSN
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("connect to postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, nil, fmt.Errorf("ping postgres (is docker compose up?): %w", err)
	}

	if err := entitystore.Migrate(ctx, pool); err != nil {
		return nil, nil, fmt.Errorf("run migrations: %w", err)
	}
	slog.Info("migrations complete")

	es, err := entitystore.New(entitystore.WithPgStore(pool))
	if err != nil {
		return nil, nil, fmt.Errorf("create entitystore: %w", err)
	}

	return es, pool, nil
}
