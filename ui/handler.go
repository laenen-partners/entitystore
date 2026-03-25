// Package ui provides HTTP fragment handlers for the EntityStore explorer.
package ui

import (
	"encoding/json"
	"net/http"

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
	_ = ds.ReadSignals("search", r, &signals)

	q := signals.Query
	if q == "" {
		ds.Send.Patch(sse, searchEmpty())
		return
	}

	ctx := r.Context()
	entityType := "google.protobuf.Struct"

	// Try anchor lookup first, then token search.
	results, _ := h.es.FindByAnchors(ctx, entityType, []matching.AnchorQuery{
		{Field: "email", Value: q},
		{Field: "domain", Value: q},
		{Field: "invoice_number", Value: q},
	}, nil)

	if len(results) == 0 {
		tokens := matching.Tokenize(q)
		results, _ = h.es.FindByTokens(ctx, entityType, tokens, 20, nil)
	}

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

	relCount, _ := h.es.CountRelationsForEntity(r.Context(), id)

	sse := datastar.NewSSE(w, r)
	ds.Send.Patch(sse, entityDetail(entity, string(prettyJSON), relCount))
}

// EntityRelationsFragment returns relations for an entity.
func (h *Handlers) EntityRelationsFragment(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	ctx := r.Context()

	outbound, _ := h.es.GetRelationsFromEntity(ctx, id, 50, nil)
	inbound, _ := h.es.GetRelationsToEntity(ctx, id, 50, nil)

	sse := datastar.NewSSE(w, r)
	ds.Send.Patch(sse, entityRelations(id, outbound, inbound))
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

// EntityTypesFragment returns a list of entity types with counts.
func (h *Handlers) EntityTypesFragment(w http.ResponseWriter, r *http.Request) {
	stats, _ := h.es.Stats(r.Context())

	sse := datastar.NewSSE(w, r)
	ds.Send.Patch(sse, entityTypesList(stats.EntityTypes, stats.RelationTypes))
}
