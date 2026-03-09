package store

import (
	"context"
	"embed"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed db/migrations/*.sql
var migrationsFS embed.FS

// Migrate applies all migrations to the database. It reads embedded SQL files
// and executes them in order. For production use, prefer dbmate or another
// migration tool.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	entries, err := migrationsFS.ReadDir("db/migrations")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}
	for _, entry := range entries {
		raw, err := migrationsFS.ReadFile("db/migrations/" + entry.Name())
		if err != nil {
			return fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}
		sql := extractUpSection(string(raw))
		if _, err := pool.Exec(ctx, sql); err != nil {
			return fmt.Errorf("apply migration %s: %w", entry.Name(), err)
		}
	}
	return nil
}

// extractUpSection extracts the "-- migrate:up" section from a dbmate-formatted
// SQL file, stopping at "-- migrate:down" if present.
func extractUpSection(sql string) string {
	const upMarker = "-- migrate:up"
	const downMarker = "-- migrate:down"

	start := 0
	if idx := strings.Index(sql, upMarker); idx >= 0 {
		start = idx + len(upMarker)
	}

	end := len(sql)
	if idx := strings.Index(sql[start:], downMarker); idx >= 0 {
		end = start + idx
	}

	return strings.TrimSpace(sql[start:end])
}
