package matching

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"
)

// ---------------------------------------------------------------------------
// Tokenize
// ---------------------------------------------------------------------------

func TestTokenize(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{"simple words", "Hello World", []string{"hello", "world"}},
		{"with punctuation", "John Doe, Jr.", []string{"john", "doe", "jr"}},
		{"dedup", "the the dog", []string{"the", "dog"}},
		{"empty", "", nil},
		{"only spaces", "   ", nil},
		{"numbers", "item42 code99", []string{"item42", "code99"}},
		{"mixed case and symbols", "Alice-Marie O'Brien", []string{"alice", "marie", "o", "brien"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Tokenize(tc.input)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Tokenize(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// NormalizeField
// ---------------------------------------------------------------------------

func TestNormalizeField(t *testing.T) {
	cfg := EntityMatchConfig{
		Normalizers: map[string]func(string) string{
			"email": NormalizeLowercaseTrim,
			"phone": NormalizePhone,
		},
	}

	// Field with normalizer.
	got := NormalizeField("  ALICE@EXAMPLE.COM  ", "email", cfg)
	if got != "alice@example.com" {
		t.Errorf("email normalize = %q", got)
	}

	got = NormalizeField("+1 (555) 123-4567", "phone", cfg)
	if got != "+15551234567" {
		t.Errorf("phone normalize = %q", got)
	}

	// Field without normalizer — returns unchanged.
	got = NormalizeField("unchanged", "name", cfg)
	if got != "unchanged" {
		t.Errorf("no normalizer = %q", got)
	}

	// Nil normalizers map.
	got = NormalizeField("unchanged", "email", EntityMatchConfig{})
	if got != "unchanged" {
		t.Errorf("nil normalizers = %q", got)
	}
}

// ---------------------------------------------------------------------------
// buildAnchors
// ---------------------------------------------------------------------------

func Test_buildAnchors_SingleAnchors(t *testing.T) {
	cfg := EntityMatchConfig{
		Anchors: AnchorConfig{
			SingleAnchors: []AnchorField{
				{ProtoFieldName: "email", Normalizer: NormalizeLowercaseTrim},
				{ProtoFieldName: "crm_id", Normalizer: nil},
			},
		},
	}

	data := json.RawMessage(`{"email":"  ALICE@Example.COM  ","crm_id":"CRM-001"}`)
	anchors := buildAnchors(data, cfg)

	if len(anchors) != 2 {
		t.Fatalf("expected 2 anchors, got %d", len(anchors))
	}
	if anchors[0].Field != "email" || anchors[0].Value != "alice@example.com" {
		t.Errorf("anchor[0] = %+v", anchors[0])
	}
	if anchors[1].Field != "crm_id" || anchors[1].Value != "CRM-001" {
		t.Errorf("anchor[1] = %+v", anchors[1])
	}
}

func Test_buildAnchors_MissingField(t *testing.T) {
	cfg := EntityMatchConfig{
		Anchors: AnchorConfig{
			SingleAnchors: []AnchorField{
				{ProtoFieldName: "email", Normalizer: NormalizeLowercaseTrim},
			},
		},
	}

	data := json.RawMessage(`{"name":"Alice"}`)
	anchors := buildAnchors(data, cfg)
	if len(anchors) != 0 {
		t.Errorf("expected 0 anchors for missing field, got %d", len(anchors))
	}
}

func Test_buildAnchors_EmptyValue(t *testing.T) {
	cfg := EntityMatchConfig{
		Anchors: AnchorConfig{
			SingleAnchors: []AnchorField{
				{ProtoFieldName: "email", Normalizer: NormalizeLowercaseTrim},
			},
		},
	}

	data := json.RawMessage(`{"email":""}`)
	anchors := buildAnchors(data, cfg)
	if len(anchors) != 0 {
		t.Errorf("expected 0 anchors for empty field, got %d", len(anchors))
	}
}

func Test_buildAnchors_CompositeAnchors(t *testing.T) {
	cfg := EntityMatchConfig{
		Anchors: AnchorConfig{
			CompositeAnchors: [][]AnchorField{
				{
					{ProtoFieldName: "full_name", Normalizer: NormalizeLowercaseTrim},
					{ProtoFieldName: "date_of_birth", Normalizer: nil},
				},
			},
		},
	}

	data := json.RawMessage(`{"full_name":"Alice Johnson","date_of_birth":"1990-05-15"}`)
	anchors := buildAnchors(data, cfg)

	if len(anchors) != 1 {
		t.Fatalf("expected 1 composite anchor, got %d", len(anchors))
	}
	if anchors[0].Field != "full_name|date_of_birth" {
		t.Errorf("field = %q", anchors[0].Field)
	}
	if anchors[0].Value != "alice johnson|1990-05-15" {
		t.Errorf("value = %q", anchors[0].Value)
	}
}

func Test_buildAnchors_CompositeAnchor_PartialMissing(t *testing.T) {
	cfg := EntityMatchConfig{
		Anchors: AnchorConfig{
			CompositeAnchors: [][]AnchorField{
				{
					{ProtoFieldName: "full_name", Normalizer: nil},
					{ProtoFieldName: "date_of_birth", Normalizer: nil},
				},
			},
		},
	}

	// Missing date_of_birth — composite anchor should not fire.
	data := json.RawMessage(`{"full_name":"Alice"}`)
	anchors := buildAnchors(data, cfg)
	if len(anchors) != 0 {
		t.Errorf("expected 0 anchors for partial composite, got %d", len(anchors))
	}
}

func Test_buildAnchors_InvalidJSON(t *testing.T) {
	cfg := EntityMatchConfig{
		Anchors: AnchorConfig{
			SingleAnchors: []AnchorField{
				{ProtoFieldName: "email"},
			},
		},
	}
	anchors := buildAnchors(json.RawMessage(`not json`), cfg)
	if len(anchors) != 0 {
		t.Errorf("expected 0 anchors for invalid JSON, got %d", len(anchors))
	}
}

// ---------------------------------------------------------------------------
// buildTokens
// ---------------------------------------------------------------------------

func Test_buildTokens(t *testing.T) {
	cfg := EntityMatchConfig{
		TokenFields: []string{"full_name", "job_title"},
	}

	data := json.RawMessage(`{
		"full_name": "Alice Marie Johnson",
		"job_title": "Senior Product Manager",
		"email": "alice@example.com"
	}`)

	tokens := buildTokens(data, cfg)
	if len(tokens) != 2 {
		t.Fatalf("expected 2 token fields, got %d", len(tokens))
	}

	nameTokens := tokens["full_name"]
	sort.Strings(nameTokens)
	want := []string{"alice", "johnson", "marie"}
	if !reflect.DeepEqual(nameTokens, want) {
		t.Errorf("full_name tokens = %v, want %v", nameTokens, want)
	}

	titleTokens := tokens["job_title"]
	sort.Strings(titleTokens)
	want = []string{"manager", "product", "senior"}
	if !reflect.DeepEqual(titleTokens, want) {
		t.Errorf("job_title tokens = %v, want %v", titleTokens, want)
	}
}

func Test_buildTokens_NoTokenFields(t *testing.T) {
	cfg := EntityMatchConfig{}
	data := json.RawMessage(`{"name":"Alice"}`)
	tokens := buildTokens(data, cfg)
	if tokens != nil {
		t.Errorf("expected nil for no token fields, got %v", tokens)
	}
}

func Test_buildTokens_MissingField(t *testing.T) {
	cfg := EntityMatchConfig{
		TokenFields: []string{"missing_field"},
	}
	data := json.RawMessage(`{"name":"Alice"}`)
	tokens := buildTokens(data, cfg)
	if tokens != nil {
		t.Errorf("expected nil for missing field, got %v", tokens)
	}
}

func Test_buildTokens_EmptyField(t *testing.T) {
	cfg := EntityMatchConfig{
		TokenFields: []string{"name"},
	}
	data := json.RawMessage(`{"name":""}`)
	tokens := buildTokens(data, cfg)
	if tokens != nil {
		t.Errorf("expected nil for empty field, got %v", tokens)
	}
}

// ---------------------------------------------------------------------------
// snakeToCamel (internal but tested via fieldValue)
// ---------------------------------------------------------------------------

func TestSnakeToCamel(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"full_name", "fullName"},
		{"date_of_birth", "dateOfBirth"},
		{"email", "email"},
		{"a_b_c", "aBC"},
		{"", ""},
	}
	for _, tc := range tests {
		got := snakeToCamel(tc.input)
		if got != tc.want {
			t.Errorf("snakeToCamel(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// Test that camelCase JSON keys are resolved via fieldValue fallback.
func Test_buildAnchors_CamelCaseJSON(t *testing.T) {
	cfg := EntityMatchConfig{
		Anchors: AnchorConfig{
			SingleAnchors: []AnchorField{
				{ProtoFieldName: "full_name", Normalizer: nil},
			},
		},
	}
	data := json.RawMessage(`{"fullName":"Alice"}`)
	anchors := buildAnchors(data, cfg)
	if len(anchors) != 1 {
		t.Fatalf("expected 1 anchor from camelCase key, got %d", len(anchors))
	}
	if anchors[0].Value != "Alice" {
		t.Errorf("value = %q, want %q", anchors[0].Value, "Alice")
	}
}
