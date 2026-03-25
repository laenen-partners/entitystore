// EntityStore Explorer — browse entities, relations, events, and graph traversals.
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

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/laenen-partners/dsx/showcase"
	"github.com/laenen-partners/dsx/stream"
	"github.com/laenen-partners/entitystore"
	explorerui "github.com/laenen-partners/entitystore/ui"
	"github.com/laenen-partners/entitystore/ui/cmd/showcase/internal/pages"
	"github.com/laenen-partners/entitystore/ui/cmd/showcase/seed"
	"github.com/laenen-partners/pubsub"
)

const defaultDSN = "postgres://showcase:showcase@localhost:5489/entitystore_showcase?sslmode=disable"

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
	if err := showcase.Run(showcase.Config{
		Port: 3336,
		Identities: []showcase.Identity{
			{Name: "Admin", TenantID: "showcase", WorkspaceID: "ws-1", PrincipalID: "admin-1", Roles: []string{"admin"}},
			{Name: "Viewer", TenantID: "showcase", WorkspaceID: "ws-1", PrincipalID: "viewer-1", Roles: []string{"viewer"}},
		},
		Setup: func(ctx context.Context, r chi.Router, bus *pubsub.Bus, relay *stream.Relay) error {
			es, pool, err := setupStore(ctx)
			if err != nil {
				return err
			}

			h := explorerui.NewHandlers(es)
			h.RegisterRoutes(r)

			// Seed sample data.
			if err := seed.Run(ctx, pool, es); err != nil {
				return fmt.Errorf("seed data: %w", err)
			}
			slog.Info("seed migrations complete")

			return nil
		},
		Pages: map[string]templ.Component{
			"/":         pages.Home(),
			"/search":   pages.Search(),
			"/stats":    pages.Stats(),
			"/entities": pages.Entities(),
		},
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
