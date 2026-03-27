// Package ui provides HTTP fragment handlers for the EntityStore explorer.
package ui

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/laenen-partners/dsx/ds"
	"github.com/laenen-partners/entitystore"
	"github.com/laenen-partners/entitystore/matching"
	"github.com/laenen-partners/entitystore/store"
	"github.com/starfederation/datastar-go/datastar"
)

// Handlers serves HTML fragments for the EntityStore explorer UI.
type Handlers struct {
	es entitystore.EntityStorer
}

// NewHandlers creates fragment handlers backed by the given EntityStorer.
func NewHandlers(es entitystore.EntityStorer) *Handlers {
	return &Handlers{es: es}
}

// RegisterRoutes mounts all fragment endpoints on the given router.
func (h *Handlers) RegisterRoutes(r chi.Router) {
	r.Get("/fragments/stats", h.StatsFragment)
	r.Get("/fragments/search", h.SearchFragment)
	r.Get("/fragments/entities", h.EntityListFragment)
	r.Get("/fragments/entities/{id}", h.EntityDetailFragment)
	r.Get("/fragments/entities/{id}/relations", h.EntityRelationsFragment)
	r.Get("/fragments/events/{id}", h.EventDetailFragment)
	r.Get("/fragments/entities/{id}/events", h.EntityEventsFragment)
	r.Get("/fragments/entities/{id}/graph", h.EntityGraphFragment)
	r.Get("/fragments/entity-types", h.EntityTypesFragment)
}

// StatsFragment returns aggregate store statistics.
func (h *Handlers) StatsFragment(w http.ResponseWriter, r *http.Request) {
	stats, err := h.es.Stats(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	sse := datastar.NewSSE(w, r)
	ds.Send.Patch(sse, statsFragment(stats))
}

// SearchSignals holds the client-side search state.
type SearchSignals struct {
	Query string `json:"query"`
}

// SearchFragment searches entities by anchor value or token overlap.
func (h *Handlers) SearchFragment(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)

	var signals SearchSignals
	if err := ds.ReadSignals("search", r, &signals); err != nil {
		slog.Error("read signals", "error", err)
	}
	slog.Debug("search", "query", signals.Query, "raw_datastar", r.URL.Query().Get("datastar"), "url", r.URL.String())

	q := signals.Query
	if q == "" {
		ds.Send.Patch(sse, searchEmpty())
		return
	}

	results, _ := h.es.Search(r.Context(), q, 20, nil)

	ds.Send.Patch(sse, searchResults(q, results))
}

// EntityListFragment returns all entities sorted by last updated.
func (h *Handlers) EntityListFragment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	stats, _ := h.es.Stats(ctx)

	var all []matching.StoredEntity
	for _, tc := range stats.EntityTypes {
		entities, _ := h.es.GetEntitiesByType(ctx, tc.Type, 100, nil, nil)
		all = append(all, entities...)
	}

	sse := datastar.NewSSE(w, r)
	ds.Send.Patch(sse, entityList(all))
}

// EntityDetailFragment returns entity data, tags, and metadata.
func (h *Handlers) EntityDetailFragment(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	entity, err := h.es.GetEntity(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}

	var prettyJSON json.RawMessage
	if err := json.Unmarshal(entity.Data, &prettyJSON); err == nil {
		if pretty, err := json.MarshalIndent(prettyJSON, "", "  "); err == nil {
			prettyJSON = pretty
		}
	}

	anchors, _ := h.es.GetAnchorsForEntity(r.Context(), id)
	outbound, _ := h.es.GetRelationsFromEntity(r.Context(), id, 20, nil)
	inbound, _ := h.es.GetRelationsToEntity(r.Context(), id, 20, nil)

	// Resolve display info via single traverse call.
	resolved := h.resolveEntities(r.Context(), id)

	sse := datastar.NewSSE(w, r)
	ds.Send.Drawer(sse, entityDetail(entity, string(prettyJSON), anchors, outbound, inbound, resolved), ds.WithDrawerMaxWidth("max-w-2xl"))
}

// EntityRelationsFragment returns relations for an entity.
func (h *Handlers) EntityRelationsFragment(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	ctx := r.Context()

	outbound, _ := h.es.GetRelationsFromEntity(ctx, id, 50, nil)
	inbound, _ := h.es.GetRelationsToEntity(ctx, id, 50, nil)

	sse := datastar.NewSSE(w, r)
	ds.Send.Drawer(sse, entityRelations(id, outbound, inbound), ds.WithDrawerMaxWidth("max-w-2xl"))
}

// EventDetailFragment shows a single event's full payload in a drawer.
func (h *Handlers) EventDetailFragment(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	evt, err := h.es.GetEventByID(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}

	prettyPayload := string(evt.RawPayload)
	if raw, err := json.MarshalIndent(json.RawMessage(evt.RawPayload), "", "  "); err == nil {
		prettyPayload = string(raw)
	}

	// Resolve entity display name if present.
	var entityName string
	if evt.EntityID != "" {
		if e, err := h.es.GetEntity(r.Context(), evt.EntityID); err == nil && e.DisplayName != "" {
			entityName = e.DisplayName
		}
	}

	// For relation events, parse the relation key and resolve both entities.
	var sourceName, targetName, sourceID, targetID, relationType string
	if evt.RelationKey != "" {
		parts := strings.Split(evt.RelationKey, "|")
		if len(parts) == 3 {
			sourceID, targetID, relationType = parts[0], parts[1], parts[2]
			if e, err := h.es.GetEntity(r.Context(), sourceID); err == nil {
				if e.DisplayName != "" {
					sourceName = e.DisplayName
				} else {
					sourceName = sourceID[:8] + "..."
				}
			}
			if e, err := h.es.GetEntity(r.Context(), targetID); err == nil {
				if e.DisplayName != "" {
					targetName = e.DisplayName
				} else {
					targetName = targetID[:8] + "..."
				}
			}
		}
	}

	sse := datastar.NewSSE(w, r)
	ds.Send.Drawer(sse, eventDetail(evt, prettyPayload, entityName, sourceID, targetID, sourceName, targetName, relationType), ds.WithDrawerMaxWidth("max-w-xl"))
}

// EntityEventsFragment returns events for an entity.
func (h *Handlers) EntityEventsFragment(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)
	ds.Send.Patch(sse, entityEventsPlaceholder())
}

// EntityGraphFragment returns a traversal graph for an entity.
func (h *Handlers) EntityGraphFragment(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	results, _ := h.es.Traverse(r.Context(), id, &store.TraverseOpts{MaxDepth: 2, MaxResults: 30})

	center, _ := h.es.GetEntity(r.Context(), id)

	sse := datastar.NewSSE(w, r)
	ds.Send.Patch(sse, entityGraph(center, results))
}

// ResolvedEntity holds display info for a related entity.
type ResolvedEntity struct {
	Name       string
	EntityType string
}

// resolveEntities builds a map of entity ID → display info using Traverse depth 1.
func (h *Handlers) resolveEntities(ctx context.Context, entityID string) map[string]ResolvedEntity {
	resolved := make(map[string]ResolvedEntity)
	neighbors, _ := h.es.Traverse(ctx, entityID, &store.TraverseOpts{MaxDepth: 1, MaxResults: 50})
	for _, n := range neighbors {
		name := n.Entity.ID[:8] + "..."
		if n.Entity.DisplayName != "" {
			name = n.Entity.DisplayName
		}
		resolved[n.Entity.ID] = ResolvedEntity{
			Name:       name,
			EntityType: shortEntityType(n.Entity.EntityType),
		}
	}
	return resolved
}

func shortEntityType(t string) string {
	parts := strings.Split(t, ".")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return t
}

// EntityTypesFragment returns a list of entity types with counts.
func (h *Handlers) EntityTypesFragment(w http.ResponseWriter, r *http.Request) {
	stats, _ := h.es.Stats(r.Context())

	sse := datastar.NewSSE(w, r)
	ds.Send.Patch(sse, entityTypesList(stats.EntityTypes, stats.RelationTypes))
}
