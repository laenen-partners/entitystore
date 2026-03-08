package matching

import (
	"context"
	"encoding/json"
	"strings"
)

// EmbedderFunc computes a vector from text.
type EmbedderFunc func(ctx context.Context, text string) ([]float32, error)

// ComputeEmbedding extracts text from the fields listed in config.EmbedFields,
// concatenates them, and calls the embedder. Returns nil without error if there
// are no embed fields or the concatenated text is empty.
func ComputeEmbedding(ctx context.Context, data json.RawMessage, config EntityMatchConfig, embedder EmbedderFunc) ([]float32, error) {
	if len(config.EmbedFields) == 0 || embedder == nil {
		return nil, nil
	}
	text := ExtractEmbedText(data, config.EmbedFields)
	if text == "" {
		return nil, nil
	}
	return embedder(ctx, text)
}

// ExtractEmbedText concatenates field values from a JSON object for embedding.
func ExtractEmbedText(data json.RawMessage, fields []string) string {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return ""
	}

	var parts []string
	for _, field := range fields {
		raw, ok := obj[field]
		if !ok {
			raw, ok = obj[snakeToCamel(field)]
		}
		if !ok {
			continue
		}
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			s = strings.TrimSpace(string(raw))
			s = strings.Trim(s, `"`)
		}
		s = strings.TrimSpace(s)
		if s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " ")
}
