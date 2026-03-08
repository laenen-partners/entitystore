-- migrate:up

-- Extensions
CREATE EXTENSION IF NOT EXISTS vector;

-- Entities
CREATE TABLE entities (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    entity_type TEXT NOT NULL,
    data        JSONB NOT NULL,
    confidence  DOUBLE PRECISION NOT NULL DEFAULT 0.0,
    tags        TEXT[] NOT NULL DEFAULT '{}',
    embedding   vector(768),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_entities_type ON entities (entity_type);
CREATE INDEX idx_entities_tags_gin ON entities USING GIN (tags);
CREATE INDEX idx_entities_embedding_hnsw ON entities
  USING hnsw (embedding vector_cosine_ops) WITH (m = 16, ef_construction = 128);

-- Anchors (dedup keys)
CREATE TABLE entity_anchors (
    entity_id        UUID NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    entity_type      TEXT NOT NULL,
    anchor_field     TEXT NOT NULL,
    normalized_value TEXT NOT NULL,
    PRIMARY KEY (entity_type, anchor_field, normalized_value)
);

CREATE INDEX idx_anchors_entity ON entity_anchors (entity_id);

-- Tokens (fuzzy match fields)
CREATE TABLE entity_tokens (
    entity_id   UUID NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    entity_type TEXT NOT NULL,
    token_field TEXT NOT NULL,
    tokens      TEXT[] NOT NULL,
    PRIMARY KEY (entity_id, token_field)
);

CREATE INDEX idx_entity_tokens_gin ON entity_tokens USING GIN (tokens);

-- Provenance
CREATE TABLE entity_provenance (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    entity_id        UUID NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    document_id      TEXT NOT NULL,
    extracted_at     TIMESTAMPTZ NOT NULL,
    model_id         TEXT NOT NULL,
    confidence       DOUBLE PRECISION NOT NULL,
    fields           TEXT[] NOT NULL,
    match_method     TEXT NOT NULL,
    match_confidence DOUBLE PRECISION NOT NULL
);

CREATE INDEX idx_provenance_entity ON entity_provenance (entity_id);
CREATE INDEX idx_provenance_document ON entity_provenance (document_id);

-- Relations
CREATE TABLE entity_relations (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source_id     UUID NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    target_id     UUID NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    relation_type TEXT NOT NULL,
    confidence    DOUBLE PRECISION NOT NULL,
    evidence      TEXT,
    implied       BOOLEAN NOT NULL DEFAULT false,
    document_id   TEXT,
    data          JSONB NOT NULL DEFAULT '{}',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_rel_source ON entity_relations (source_id);
CREATE INDEX idx_rel_target ON entity_relations (target_id);
CREATE INDEX idx_rel_type ON entity_relations (relation_type);
CREATE INDEX idx_rel_source_type ON entity_relations (source_id, relation_type);
CREATE UNIQUE INDEX idx_rel_dedup ON entity_relations (source_id, target_id, relation_type) WHERE document_id IS NULL;

-- migrate:down

DROP TABLE IF EXISTS entity_relations;
DROP TABLE IF EXISTS entity_provenance;
DROP TABLE IF EXISTS entity_tokens;
DROP TABLE IF EXISTS entity_anchors;
DROP TABLE IF EXISTS entities;
DROP EXTENSION IF EXISTS vector;
