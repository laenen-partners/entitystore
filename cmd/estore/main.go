package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"

	entitystore "github.com/laenen-partners/entitystore"
	"github.com/laenen-partners/entitystore/store"
)

func main() {
	cfg := entitystore.ConfigFromEnv()

	handler, s, err := entitystore.New(cfg)
	if err != nil {
		slog.Error("failed to create entity store", "error", err)
		os.Exit(1)
	}
	defer s.Close()

	// Apply embedded migrations.
	if err := store.Migrate(context.Background(), s.Pool()); err != nil {
		slog.Error("failed to apply migrations", "error", err)
		os.Exit(1)
	}

	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":3002"
	}

	slog.Info("entitystore server starting", "addr", addr)
	if err := http.ListenAndServe(addr, handler); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
