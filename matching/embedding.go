package matching

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Embedder computes vector embeddings from text. Implementations should
// return one []float32 per input text in the same order.
//
// This interface is compatible with github.com/laenen-partners/embedder.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// ComputeEmbedding extracts text from the fields listed in config.EmbedFields,
// concatenates them, and calls the embedder. Returns nil without error if there
// are no embed fields or the concatenated text is empty.
func ComputeEmbedding(ctx context.Context, data json.RawMessage, config EntityMatchConfig, embedder Embedder) ([]float32, error) {
	if len(config.EmbedFields) == 0 || embedder == nil {
		return nil, nil
	}
	text := TextToEmbed(data, config.EmbedFields)
	if text == "" {
		return nil, nil
	}
	vecs, err := embedder.Embed(ctx, []string{text})
	if err != nil {
		return nil, fmt.Errorf("embed: %w", err)
	}
	if len(vecs) == 0 {
		return nil, nil
	}
	return vecs[0], nil
}

// TextToEmbed extracts and concatenates field values from entity data for
// embedding. Fields are matched by snake_case or camelCase name. Values are
// trimmed and joined with spaces. Returns empty string if no fields match.
func TextToEmbed(data json.RawMessage, fields []string) string {
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

