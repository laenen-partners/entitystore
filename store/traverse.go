package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/laenen-partners/entitystore/matching"
)

// Direction controls which edge directions the traversal follows.
type Direction int

const (
	// DirectionBoth follows edges in both directions (default).
	DirectionBoth Direction = iota
	// DirectionOutbound follows source→target edges only.
	DirectionOutbound
	// DirectionInbound follows target→source edges only.
	DirectionInbound
)

// TraverseOpts configures a graph traversal.
type TraverseOpts struct {
	// Direction controls which edge directions to follow. Default: DirectionBoth.
	Direction Direction
	// MaxDepth limits how many hops to traverse. Default: 2, max: 10.
	MaxDepth int
	// MaxResults caps the number of returned entities. Default: 100.
	MaxResults int
	// RelationTypes filters edges by type at each hop. Empty means all.
	RelationTypes []string
	// EntityType filters discovered entities by type. Empty means all.
	EntityType string
	// MinConfidence is the minimum relation confidence per hop. 0 means no filter.
	MinConfidence float64
	// Filter applies tag filtering on traversed entities.
	Filter *matching.QueryFilter
}

// TraverseResult represents a single entity discovered during traversal.
type TraverseResult struct {
	Entity matching.StoredEntity
	Depth  int
	Path   []TraverseEdge
}

// TraverseEdge represents a single edge in a traversal path.
type TraverseEdge struct {
	RelationType string  `json:"relation_type"`
	FromID       string  `json:"from_id"`
	ToID         string  `json:"to_id"`
	Confidence   float64 `json:"confidence"`
}

func (o *TraverseOpts) defaults() {
	if o.MaxDepth <= 0 {
		o.MaxDepth = 2
	}
	if o.MaxDepth > 10 {
		o.MaxDepth = 10
	}
	if o.MaxResults <= 0 {
		o.MaxResults = 100
	}
	if o.RelationTypes == nil {
		o.RelationTypes = []string{}
	}
}

const traverseSQL = `
WITH RECURSIVE traverse AS (
    SELECT e.id, e.entity_type, e.data, e.confidence, e.tags,
           e.created_at, e.updated_at,
           0 AS depth, ARRAY[e.id] AS visited, '[]'::jsonb AS path
    FROM entities e WHERE e.id = $1

    UNION ALL

    SELECT next_e.id, next_e.entity_type, next_e.data, next_e.confidence, next_e.tags,
           next_e.created_at, next_e.updated_at,
           t.depth + 1, t.visited || next_e.id,
           t.path || jsonb_build_object(
               'relation_type', r.relation_type,
               'from_id', r.source_id, 'to_id', r.target_id,
               'confidence', r.confidence)
    FROM traverse t
    JOIN entity_relations r ON (($2::bool AND r.source_id = t.id) OR ($3::bool AND r.target_id = t.id))
    JOIN entities next_e ON next_e.id = CASE
        WHEN r.source_id = t.id THEN r.target_id ELSE r.source_id END
    WHERE t.depth < $4
      AND NOT (next_e.id = ANY(t.visited))
      AND (cardinality($5::text[]) = 0 OR r.relation_type = ANY($5::text[]))
      AND ($6::text = '' OR next_e.entity_type = $6::text)
      AND ($7::float8 = 0 OR r.confidence >= $7::float8)
      AND (cardinality($8::text[]) = 0 OR next_e.tags @> $8::text[])
      AND (cardinality($9::text[]) = 0 OR next_e.tags && $9::text[])
      AND ($10::text = '' OR NOT ($10::text = ANY(next_e.tags)) OR next_e.tags && $11::text[])
)
SELECT id, entity_type, data, confidence, tags, created_at, updated_at, depth, path
FROM traverse WHERE depth > 0
ORDER BY depth, created_at
LIMIT $12;
`

// Traverse performs a multi-hop graph traversal starting from the given entity.
func (s *Store) Traverse(ctx context.Context, entityID string, opts *TraverseOpts) ([]TraverseResult, error) {
	startID, err := uuid.Parse(entityID)
	if err != nil {
		return nil, fmt.Errorf("parse entity id: %w", err)
	}

	o := TraverseOpts{}
	if opts != nil {
		o = *opts
	}
	o.defaults()

	allowOutbound := o.Direction == DirectionBoth || o.Direction == DirectionOutbound
	allowInbound := o.Direction == DirectionBoth || o.Direction == DirectionInbound

	tags := tagsParam(o.Filter)
	anyTags := anyTagsParam(o.Filter)
	excludeTag := excludeTagParam(o.Filter)
	unlessTags := unlessTagsParam(o.Filter)

	var querier interface {
		Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	}
	if s.tx != nil {
		querier = s.tx
	} else {
		querier = s.pool
	}

	rows, err := querier.Query(ctx, traverseSQL,
		startID,        // $1
		allowOutbound,  // $2
		allowInbound,   // $3
		o.MaxDepth,     // $4
		o.RelationTypes, // $5
		o.EntityType,   // $6
		o.MinConfidence, // $7
		tags,           // $8
		anyTags,        // $9
		excludeTag,     // $10
		unlessTags,     // $11
		o.MaxResults,   // $12
	)
	if err != nil {
		return nil, fmt.Errorf("traverse: %w", err)
	}
	defer rows.Close()

	seen := make(map[uuid.UUID]struct{})
	var results []TraverseResult

	for rows.Next() {
		var (
			id         uuid.UUID
			entityType string
			data       json.RawMessage
			confidence float64
			rowTags    []string
			createdAt  time.Time
			updatedAt  time.Time
			depth      int
			pathJSON   json.RawMessage
		)
		if err := rows.Scan(&id, &entityType, &data, &confidence, &rowTags, &createdAt, &updatedAt, &depth, &pathJSON); err != nil {
			return nil, fmt.Errorf("traverse scan: %w", err)
		}

		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}

		if rowTags == nil {
			rowTags = []string{}
		}

		var path []TraverseEdge
		if err := json.Unmarshal(pathJSON, &path); err != nil {
			return nil, fmt.Errorf("traverse unmarshal path: %w", err)
		}

		results = append(results, TraverseResult{
			Entity: matching.StoredEntity{
				ID:         id.String(),
				EntityType: entityType,
				Data:       data,
				Confidence: confidence,
				Tags:       rowTags,
				CreatedAt:  createdAt,
				UpdatedAt:  updatedAt,
			},
			Depth: depth,
			Path:  path,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("traverse rows: %w", err)
	}

	return results, nil
}
