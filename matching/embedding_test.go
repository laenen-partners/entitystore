package matching

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

// ---------------------------------------------------------------------------
// ExtractEmbedText
// ---------------------------------------------------------------------------

func TestExtractEmbedText(t *testing.T) {
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
			got := ExtractEmbedText(data, tc.fields)
			if got != tc.want {
				t.Errorf("ExtractEmbedText() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExtractEmbedText_InvalidJSON(t *testing.T) {
	got := ExtractEmbedText(json.RawMessage(`not json`), []string{"name"})
	if got != "" {
		t.Errorf("expected empty for invalid JSON, got %q", got)
	}
}

func TestExtractEmbedText_EmptyValues(t *testing.T) {
	data := json.RawMessage(`{"name":"","title":"  "}`)
	got := ExtractEmbedText(data, []string{"name", "title"})
	if got != "" {
		t.Errorf("expected empty for blank values, got %q", got)
	}
}

func TestExtractEmbedText_CamelCaseFallback(t *testing.T) {
	data := json.RawMessage(`{"fullName":"Alice","jobTitle":"PM"}`)
	got := ExtractEmbedText(data, []string{"full_name", "job_title"})
	if got != "Alice PM" {
		t.Errorf("camelCase fallback = %q, want %q", got, "Alice PM")
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
	var capturedText string

	embedder := func(_ context.Context, text string) ([]float32, error) {
		capturedText = text
		return expectedVec, nil
	}

	vec, err := ComputeEmbedding(ctx, data, cfg, embedder)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(vec, expectedVec) {
		t.Errorf("vec = %v, want %v", vec, expectedVec)
	}
	if capturedText != "Alice Engineer" {
		t.Errorf("embedder received %q, want %q", capturedText, "Alice Engineer")
	}
}

func TestComputeEmbedding_NoEmbedFields(t *testing.T) {
	ctx := context.Background()
	cfg := EntityMatchConfig{}
	data := json.RawMessage(`{"name":"Alice"}`)

	vec, err := ComputeEmbedding(ctx, data, cfg, func(_ context.Context, _ string) ([]float32, error) {
		t.Error("embedder should not be called")
		return nil, nil
	})
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

	vec, err := ComputeEmbedding(ctx, data, cfg, func(_ context.Context, _ string) ([]float32, error) {
		t.Error("embedder should not be called for empty text")
		return nil, nil
	})
	if err != nil || vec != nil {
		t.Errorf("expected nil, nil; got %v, %v", vec, err)
	}
}

func TestComputeEmbedding_EmbedderError(t *testing.T) {
	ctx := context.Background()
	cfg := EntityMatchConfig{EmbedFields: []string{"name"}}
	data := json.RawMessage(`{"name":"Alice"}`)

	wantErr := errors.New("embedding API down")
	_, err := ComputeEmbedding(ctx, data, cfg, func(_ context.Context, _ string) ([]float32, error) {
		return nil, wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want %v", err, wantErr)
	}
}
