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
package explorer

import (
	"context"
	"sort"
	"time"
	"log/slog"
	"net/http"
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

			// Server-side rendered pages (fetch data, render full page).
			r.Get("/stats", pageHandler(cfg.Store, "stats"))
			r.Get("/entities", pageHandler(cfg.Store, "entities"))
			r.Get("/events", pageHandler(cfg.Store, "events"))

			return nil
		},
		Pages: map[string]templ.Component{
			"/":       Search(),
			"/search": Search(),
		},
	})
}

// pageHandler returns an HTTP handler that fetches data and renders a page.
func pageHandler(es entitystore.EntityStorer, page string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		switch page {
		case "home", "stats":
			stats, _ := es.Stats(ctx)
			templ.Handler(StatsPage(stats)).ServeHTTP(w, r)
		case "entities":
			var all []entitystore.StoredEntity
			stats, _ := es.Stats(ctx)
			for _, tc := range stats.EntityTypes {
				entities, _ := es.GetEntitiesByType(ctx, tc.Type, 100, nil, nil)
				all = append(all, entities...)
			}
			templ.Handler(EntitiesPage(all)).ServeHTTP(w, r)
		case "events":
			limit := 50
			var eventTypes []string
			if et := r.URL.Query().Get("type"); et != "" {
				eventTypes = []string{et}
			}
			var cursor *time.Time
			if c := r.URL.Query().Get("cursor"); c != "" {
				if t, err := time.Parse(time.RFC3339Nano, c); err == nil {
					cursor = &t
				}
			}
			events, _ := es.GetAllEvents(ctx, &entitystore.EventQueryOpts{
				Limit:      limit,
				EventTypes: eventTypes,
			}, cursor)
			// Get distinct event types for the filter dropdown.
			allEvents, _ := es.GetAllEvents(ctx, &entitystore.EventQueryOpts{Limit: 200}, nil)
			typeSet := make(map[string]struct{})
			for _, e := range allEvents {
				typeSet[e.EventType] = struct{}{}
			}
			var types []string
			for t := range typeSet {
				types = append(types, t)
			}
			sort.Strings(types)
			activeType := r.URL.Query().Get("type")
			templ.Handler(EventsPage(events, types, activeType)).ServeHTTP(w, r)
		}
	}
}

// Mount registers the explorer fragment handlers on an existing chi router.
func Mount(r chi.Router, es entitystore.EntityStorer) {
	h := explorerui.NewHandlers(es)
	h.RegisterRoutes(r)
}

// RunInBackground starts the explorer server in a goroutine.
func RunInBackground(ctx context.Context, cfg Config) {
	go func() {
		if err := Run(cfg); err != nil {
			slog.ErrorContext(ctx, "explorer stopped", "error", err)
		}
	}()
	_ = os.Stderr
}
