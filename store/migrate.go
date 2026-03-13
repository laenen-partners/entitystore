package store

import (
	"context"
	"embed"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/laenen-partners/migrate"
)

//go:embed db/migrations/*.sql
var migrationsFS embed.FS

const migrationsScope = "entitystore"

// Migrate applies all pending migrations for the entitystore scope.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	fsys, err := fs.Sub(migrationsFS, "db/migrations")
	if err != nil {
		return fmt.Errorf("migrate: sub fs: %w", err)
	}
	if err := migrate.Up(ctx, pool, fsys, migrationsScope); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}
