package matching

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

// mockEmbedder implements the Embedder interface for testing.
type mockEmbedder struct {
	fn func(ctx context.Context, texts []string) ([][]float32, error)
}

func (m *mockEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	return m.fn(ctx, texts)
}

// ---------------------------------------------------------------------------
// TextToEmbed
// ---------------------------------------------------------------------------

func Test_TextToEmbed(t *testing.T) {
	data := json.RawMessage(`{
		"email": "alice@example.com",
		"full_name": "Alice Johnson",
		"job_title": "Product Manager",
		"phone": "+1-555-123"
	}`)

	tests := []struct {
		name   string
		fields []string
		want   string
	}{
		{"single field", []string{"full_name"}, "Alice Johnson"},
		{"multiple fields", []string{"full_name", "job_title"}, "Alice Johnson Product Manager"},
		{"all fields", []string{"email", "full_name", "job_title"}, "alice@example.com Alice Johnson Product Manager"},
		{"missing field ignored", []string{"full_name", "nonexistent", "job_title"}, "Alice Johnson Product Manager"},
		{"no fields", []string{}, ""},
		{"nil fields", nil, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := TextToEmbed(data, tc.fields)
			if got != tc.want {
				t.Errorf("TextToEmbed() = %q, want %q", got, tc.want)
			}
		})
	}
}

func Test_TextToEmbed_InvalidJSON(t *testing.T) {
	got := TextToEmbed(json.RawMessage(`not json`), []string{"name"})
	if got != "" {
		t.Errorf("expected empty for invalid JSON, got %q", got)
	}
}

func Test_TextToEmbed_EmptyValues(t *testing.T) {
	data := json.RawMessage(`{"name":"","title":"  "}`)
	got := TextToEmbed(data, []string{"name", "title"})
	if got != "" {
		t.Errorf("expected empty for blank values, got %q", got)
	}
}

func Test_TextToEmbed_CamelCaseFallback(t *testing.T) {
	data := json.RawMessage(`{"fullName":"Alice","jobTitle":"PM"}`)
	got := TextToEmbed(data, []string{"full_name", "job_title"})
	if got != "Alice PM" {
		t.Errorf("camelCase fallback = %q, want %q", got, "Alice PM")
	}
}

// TextToEmbed backward compat alias.
func Test_TextToEmbed_BackwardCompat(t *testing.T) {
	data := json.RawMessage(`{"name":"Alice"}`)
	got := TextToEmbed(data, []string{"name"})
	if got != "Alice" {
		t.Errorf("TextToEmbed = %q", got)
	}
}

// ---------------------------------------------------------------------------
// ComputeEmbedding
// ---------------------------------------------------------------------------

func TestComputeEmbedding(t *testing.T) {
	ctx := context.Background()
	cfg := EntityMatchConfig{
		EmbedFields: []string{"name", "title"},
	}
	data := json.RawMessage(`{"name":"Alice","title":"Engineer"}`)

	expectedVec := []float32{0.1, 0.2, 0.3}
	var capturedTexts []string

	emb := &mockEmbedder{fn: func(_ context.Context, texts []string) ([][]float32, error) {
		capturedTexts = texts
		return [][]float32{expectedVec}, nil
	}}

	vec, err := ComputeEmbedding(ctx, data, cfg, emb)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(vec, expectedVec) {
		t.Errorf("vec = %v, want %v", vec, expectedVec)
	}
	if len(capturedTexts) != 1 || capturedTexts[0] != "Alice Engineer" {
		t.Errorf("embedder received %v, want [\"Alice Engineer\"]", capturedTexts)
	}
}

func TestComputeEmbedding_NoEmbedFields(t *testing.T) {
	ctx := context.Background()
	cfg := EntityMatchConfig{}
	data := json.RawMessage(`{"name":"Alice"}`)

	emb := &mockEmbedder{fn: func(_ context.Context, _ []string) ([][]float32, error) {
		t.Error("embedder should not be called")
		return nil, nil
	}}

	vec, err := ComputeEmbedding(ctx, data, cfg, emb)
	if err != nil || vec != nil {
		t.Errorf("expected nil, nil; got %v, %v", vec, err)
	}
}

func TestComputeEmbedding_NilEmbedder(t *testing.T) {
	ctx := context.Background()
	cfg := EntityMatchConfig{EmbedFields: []string{"name"}}
	data := json.RawMessage(`{"name":"Alice"}`)

	vec, err := ComputeEmbedding(ctx, data, cfg, nil)
	if err != nil || vec != nil {
		t.Errorf("expected nil, nil; got %v, %v", vec, err)
	}
}

func TestComputeEmbedding_EmptyText(t *testing.T) {
	ctx := context.Background()
	cfg := EntityMatchConfig{EmbedFields: []string{"name"}}
	data := json.RawMessage(`{"name":""}`)

	emb := &mockEmbedder{fn: func(_ context.Context, _ []string) ([][]float32, error) {
		t.Error("embedder should not be called for empty text")
		return nil, nil
	}}

	vec, err := ComputeEmbedding(ctx, data, cfg, emb)
	if err != nil || vec != nil {
		t.Errorf("expected nil, nil; got %v, %v", vec, err)
	}
}

func TestComputeEmbedding_EmbedderError(t *testing.T) {
	ctx := context.Background()
	cfg := EntityMatchConfig{EmbedFields: []string{"name"}}
	data := json.RawMessage(`{"name":"Alice"}`)

	wantErr := errors.New("embedding API down")
	emb := &mockEmbedder{fn: func(_ context.Context, _ []string) ([][]float32, error) {
		return nil, wantErr
	}}

	_, err := ComputeEmbedding(ctx, data, cfg, emb)
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want %v", err, wantErr)
	}
}
