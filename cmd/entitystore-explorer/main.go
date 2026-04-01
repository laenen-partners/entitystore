// Command entitystore-explorer starts a standalone EntityStore Explorer server.
//
// It connects to an existing PostgreSQL database (with entitystore tables)
// and serves a read-only UI for browsing entities, relations, events, and
// graph traversals.
//
// Usage:
//
//	entitystore-explorer                           # uses DATABASE_URL or default
//	entitystore-explorer -dsn "postgres://..."     # explicit DSN
//	entitystore-explorer -port 3336                # custom port
//
// Install:
//
//	go install github.com/laenen-partners/entitystore/cmd/entitystore-explorer@latest
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/laenen-partners/entitystore"
	"github.com/laenen-partners/entitystore/ui/explorer"
)

func main() {
	var (
		dsn  string
		port int
	)
	flag.StringVar(&dsn, "dsn", "", "PostgreSQL connection string (default: DATABASE_URL env or localhost)")
	flag.IntVar(&port, "port", 3336, "HTTP port to listen on")
	flag.Parse()

	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		dsn = "postgres://localhost:5432/entitystore?sslmode=disable"
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect: %v\n", err)
		os.Exit(1)
	}
	if err := pool.Ping(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "ping: %v\n", err)
		os.Exit(1)
	}
	slog.Info("connected to database", "dsn", dsn)

	es, err := entitystore.New(entitystore.WithPgStore(pool))
	if err != nil {
		fmt.Fprintf(os.Stderr, "create store: %v\n", err)
		os.Exit(1)
	}

	slog.Info("starting explorer", "port", port)
	if err := explorer.Run(explorer.Config{
		Store: es,
		Port:  port,
	}); err != nil {
		slog.Error("explorer failed", "error", err)
		os.Exit(1)
	}
}
