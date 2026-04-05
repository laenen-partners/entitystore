package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	"github.com/laenen-partners/entitystore/store/internal/dbgen"
)

// Event is a stored event with its metadata.
type Event struct {
	ID                string          `json:"id"`
	EventType         string          `json:"event_type"`
	PayloadType       string          `json:"payload_type"`
	Payload           proto.Message   `json:"payload"`
	RawPayload        json.RawMessage `json:"raw_payload"`
	EntityID          string          `json:"entity_id,omitempty"`
	EntityType        string          `json:"entity_type,omitempty"`
	EntityDisplayName string          `json:"entity_display_name,omitempty"`
	RelationKey       string          `json:"relation_key,omitempty"`
	Tags              []string        `json:"tags,omitempty"`
	OccurredAt        time.Time       `json:"occurred_at"`
	PublishedAt       *time.Time      `json:"published_at,omitempty"`
}

// EventQueryOpts filters event queries.
type EventQueryOpts struct {
	EventTypes []string  // filter by exact event types
	Since      time.Time // only events after this time
	Limit      int       // max results (default 100)
}

// GetEventByID returns a single event by ID.
func (s *Store) GetEventByID(ctx context.Context, eventID string) (Event, error) {
	uid, err := uuid.Parse(eventID)
	if err != nil {
		return Event{}, fmt.Errorf("parse event id: %w", err)
	}
	row, err := s.queries.GetEventByID(ctx, uid)
	if err != nil {
		return Event{}, fmt.Errorf("get event: %w", err)
	}
	return eventFromRow(row), nil
}

// GetEventsForEntity returns events for the given entity, newest first.
func (s *Store) GetEventsForEntity(ctx context.Context, entityID string, opts *EventQueryOpts) ([]Event, error) {
	uid, err := uuid.Parse(entityID)
	if err != nil {
		return nil, fmt.Errorf("parse entity id: %w", err)
	}

	limit := int32(100)
	var eventTypes []string
	var since time.Time
	if opts != nil {
		if opts.Limit > 0 {
			limit = int32(opts.Limit)
		}
		if len(opts.EventTypes) > 0 {
			eventTypes = opts.EventTypes
		}
		if !opts.Since.IsZero() {
			since = opts.Since
		}
	}
	if eventTypes == nil {
		eventTypes = []string{}
	}

	rows, err := s.queries.GetEventsForEntity(ctx, dbgen.GetEventsForEntityParams{
		EntityID:   pgtype.UUID{Bytes: uid, Valid: true},
		EventTypes: eventTypes,
		Since:      since,
		MaxResults: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("get events: %w", err)
	}

	result := make([]Event, len(rows))
	for i, row := range rows {
		result[i] = eventFromRow(row)
	}
	return result, nil
}

// GetAllEvents returns all events across all entities, newest first, with cursor pagination.
func (s *Store) GetAllEvents(ctx context.Context, opts *EventQueryOpts, cursor *time.Time) ([]Event, error) {
	limit := int32(50)
	var eventTypes []string
	if opts != nil {
		if opts.Limit > 0 {
			limit = int32(opts.Limit)
		}
		if len(opts.EventTypes) > 0 {
			eventTypes = opts.EventTypes
		}
	}
	if eventTypes == nil {
		eventTypes = []string{}
	}

	var pgCursor pgtype.Timestamptz
	if cursor != nil {
		pgCursor = pgtype.Timestamptz{Time: *cursor, Valid: true}
	}

	rows, err := s.queries.GetAllEvents(ctx, dbgen.GetAllEventsParams{
		EventTypes: eventTypes,
		Cursor:     pgCursor,
		MaxResults: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("get all events: %w", err)
	}

	result := make([]Event, len(rows))
	for i, row := range rows {
		result[i] = eventFromAllEventsRow(row)
	}
	return result, nil
}

func eventFromAllEventsRow(row dbgen.GetAllEventsRow) Event {
	e := Event{
		ID:                row.ID.String(),
		EventType:         row.EventType,
		PayloadType:       row.PayloadType,
		RawPayload:        row.Payload,
		EntityType:        row.EntityType,
		EntityDisplayName: row.EntityDisplayName,
		Tags:              row.Tags,
		OccurredAt:        row.OccurredAt,
	}
	if row.EntityID.Valid {
		e.EntityID = uuid.UUID(row.EntityID.Bytes).String()
	}
	if row.RelationKey.Valid {
		e.RelationKey = row.RelationKey.String
	}
	if row.PublishedAt.Valid {
		t := row.PublishedAt.Time
		e.PublishedAt = &t
	}
	fullName := protoreflect.FullName(row.PayloadType)
	msgType, err := protoregistry.GlobalTypes.FindMessageByName(fullName)
	if err == nil {
		msg := msgType.New().Interface()
		if err := protojson.Unmarshal(row.Payload, msg); err == nil {
			e.Payload = msg
		}
	}
	return e
}

type eventRow interface {
	dbgen.EntityEvent | dbgen.GetEventByIDRow | dbgen.GetEventsForEntityRow | dbgen.GetEventsAfterCursorRow
}

func eventFromRow[R eventRow](row R) Event {
	var e Event
	switch r := any(row).(type) {
	case dbgen.EntityEvent:
		e = toEvent(r.ID, r.EventType, r.PayloadType, r.Payload, r.EntityID, r.RelationKey, r.Tags, r.EntityType, r.OccurredAt, r.PublishedAt)
	case dbgen.GetEventByIDRow:
		e = toEvent(r.ID, r.EventType, r.PayloadType, r.Payload, r.EntityID, r.RelationKey, r.Tags, r.EntityType, r.OccurredAt, r.PublishedAt)
	case dbgen.GetEventsForEntityRow:
		e = toEvent(r.ID, r.EventType, r.PayloadType, r.Payload, r.EntityID, r.RelationKey, r.Tags, r.EntityType, r.OccurredAt, r.PublishedAt)
	case dbgen.GetEventsAfterCursorRow:
		e = toEvent(r.ID, r.EventType, r.PayloadType, r.Payload, r.EntityID, r.RelationKey, r.Tags, r.EntityType, r.OccurredAt, r.PublishedAt)
	}

	return e
}

func toEvent(id uuid.UUID, eventType, payloadType string, payload json.RawMessage, entityID pgtype.UUID, relationKey pgtype.Text, tags []string, entityType string, occurredAt time.Time, publishedAt pgtype.Timestamptz) Event {
	e := Event{
		ID:          id.String(),
		EventType:   eventType,
		PayloadType: payloadType,
		RawPayload:  payload,
		EntityType:  entityType,
		Tags:        tags,
		OccurredAt:  occurredAt,
	}
	if entityID.Valid {
		e.EntityID = uuid.UUID(entityID.Bytes).String()
	}
	if relationKey.Valid {
		e.RelationKey = relationKey.String
	}
	if publishedAt.Valid {
		t := publishedAt.Time
		e.PublishedAt = &t
	}

	// Try to resolve the proto type and unmarshal.
	fullName := protoreflect.FullName(payloadType)
	msgType, err := protoregistry.GlobalTypes.FindMessageByName(fullName)
	if err == nil {
		msg := msgType.New().Interface()
		if err := protojson.Unmarshal(payload, msg); err == nil {
			e.Payload = msg
		}
	}

	return e
}

// ---------------------------------------------------------------------------
// Event insertion helpers
// ---------------------------------------------------------------------------

// deriveEventType strips the version segment from a full proto message name.
// "entitystore.events.v1.EntityCreated" → "entitystore.events.EntityCreated"
func deriveEventType(fullName string) string {
	parts := strings.Split(fullName, ".")
	if len(parts) < 3 {
		return fullName
	}
	// Remove the second-to-last segment (version).
	stripped := append(parts[:len(parts)-2], parts[len(parts)-1])
	return strings.Join(stripped, ".")
}

// insertEvents inserts a batch of proto events into entity_events within the
// current transaction. entityID and relationKey are optional (pass uuid.Nil
// or empty string to omit).
func insertEvents(ctx context.Context, q *dbgen.Queries, entityID uuid.UUID, relationKey string, tags []string, entityType string, events []proto.Message) error {
	for _, evt := range events {
		payload, err := protojson.Marshal(evt)
		if err != nil {
			return fmt.Errorf("marshal event: %w", err)
		}
		id, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("generate event id: %w", err)
		}
		fullName := string(proto.MessageName(evt))

		var pgEntityID pgtype.UUID
		if entityID != uuid.Nil {
			pgEntityID = pgtype.UUID{Bytes: entityID, Valid: true}
		}
		var pgRelationKey pgtype.Text
		if relationKey != "" {
			pgRelationKey = pgtype.Text{String: relationKey, Valid: true}
		}
		if tags == nil {
			tags = []string{}
		}

		if err := q.InsertEvent(ctx, dbgen.InsertEventParams{
			ID:          id,
			EventType:   deriveEventType(fullName),
			PayloadType: fullName,
			Payload:     payload,
			EntityID:    pgEntityID,
			RelationKey: pgRelationKey,
			Tags:        tags,
			EntityType:  entityType,
		}); err != nil {
			return fmt.Errorf("insert event: %w", err)
		}
	}
	return nil
}

// relationKeyStr builds a relation key string for event storage.
func relationKeyStr(sourceID, targetID uuid.UUID, relationType string) string {
	return sourceID.String() + ":" + targetID.String() + ":" + relationType
}
