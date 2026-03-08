-- name: InsertProvenance :one
INSERT INTO entity_provenance (entity_id, document_id, extracted_at, model_id, confidence, fields, match_method, match_confidence)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING id, entity_id, document_id, extracted_at, model_id, confidence, fields, match_method, match_confidence;

-- name: GetProvenanceForEntity :many
SELECT id, entity_id, document_id, extracted_at, model_id, confidence, fields, match_method, match_confidence
FROM entity_provenance
WHERE entity_id = $1
ORDER BY extracted_at DESC;

-- name: GetProvenanceForDocument :many
SELECT id, entity_id, document_id, extracted_at, model_id, confidence, fields, match_method, match_confidence
FROM entity_provenance
WHERE document_id = $1
ORDER BY extracted_at DESC;

-- name: DeleteProvenanceForEntity :exec
DELETE FROM entity_provenance WHERE entity_id = $1;
