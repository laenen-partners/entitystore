// Package explorer provides an embeddable EntityStore Explorer server.
//
// Mount it in any service that has an EntityStore to get a visual debug tool
// for browsing entities, relations, events, and graph traversals.
//
// Usage:
//
//	explorer.Run(explorer.Config{
//	    Store: es,       // your EntityStorer (EntityStore or ScopedStore)
//	    Port:  3336,     // optional, default 3336
//	})
//
// Or embed into an existing chi router:
//
//	explorer.Mount(r, es)  // mounts at /explorer/
package explorer

import (
	"context"
	"log/slog"
	"os"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"
	"github.com/laenen-partners/dsx/showcase"
	"github.com/laenen-partners/dsx/stream"
	"github.com/laenen-partners/entitystore"
	explorerui "github.com/laenen-partners/entitystore/ui"
	"github.com/laenen-partners/pubsub"
)

// Config configures the explorer server.
type Config struct {
	// Store is the EntityStorer to explore. Required.
	Store entitystore.EntityStorer

	// Port to listen on. Default: 3336.
	Port int
}

// Run starts a standalone explorer server and blocks until interrupted.
// This is the simplest way to add an explorer to any service.
func Run(cfg Config) error {
	if cfg.Port == 0 {
		cfg.Port = 3336
	}

	return showcase.Run(showcase.Config{
		Port: cfg.Port,
		Identities: []showcase.Identity{
			{Name: "Explorer", TenantID: "explorer", WorkspaceID: "ws-1", PrincipalID: "explorer-1", Roles: []string{"admin"}},
		},
		Setup: func(ctx context.Context, r chi.Router, bus *pubsub.Bus, relay *stream.Relay) error {
			h := explorerui.NewHandlers(cfg.Store)
			h.RegisterRoutes(r)
			return nil
		},
		Pages: map[string]templ.Component{
			"/":         Home(),
			"/search":   Search(),
			"/stats":    Stats(),
			"/entities": Entities(),
		},
	})
}

// Mount registers the explorer fragment handlers on an existing chi router.
// Pages are not mounted — use this when you only need the API endpoints
// (e.g., for embedding in a larger application with its own layout).
func Mount(r chi.Router, es entitystore.EntityStorer) {
	h := explorerui.NewHandlers(es)
	h.RegisterRoutes(r)
}

// RunInBackground starts the explorer server in a goroutine.
// Returns immediately. The server stops when ctx is cancelled.
func RunInBackground(ctx context.Context, cfg Config) {
	go func() {
		if err := Run(cfg); err != nil {
			slog.ErrorContext(ctx, "explorer stopped", "error", err)
		}
	}()
	_ = os.Stderr // ensure os imported
}
