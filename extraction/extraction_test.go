package extraction

import (
	"testing"
)

func TestExtractionSchemaRegistry(t *testing.T) {
	r := NewExtractionSchemaRegistry()

	_, ok := r.Get("nonexistent")
	if ok {
		t.Error("expected Get on empty registry to return false")
	}
	all := r.All()
	if len(all) != 0 {
		t.Errorf("expected empty All(), got %d", len(all))
	}

	schema := ExtractionSchema{
		EntityType:  "test.v1.Person",
		DisplayName: "Person",
		Prompt:      "Extract person fields.",
		Fields: []ExtractionField{
			{
				Name:        "email",
				Description: "Email address",
				Type:        ExtractionFieldTypeString,
				Required:    true,
			},
			{
				Name:        "age",
				Description: "Age in years",
				Type:        ExtractionFieldTypeNumber,
			},
		},
	}
	r.Register(schema)

	got, ok := r.Get("test.v1.Person")
	if !ok {
		t.Fatal("expected Get to return true after Register")
	}
	if got.DisplayName != "Person" {
		t.Errorf("DisplayName = %q", got.DisplayName)
	}
	if got.Prompt != "Extract person fields." {
		t.Errorf("Prompt = %q", got.Prompt)
	}
	if len(got.Fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(got.Fields))
	}
	if got.Fields[0].Name != "email" || !got.Fields[0].Required {
		t.Errorf("field[0] = %+v", got.Fields[0])
	}
	if got.Fields[1].Type != ExtractionFieldTypeNumber {
		t.Errorf("field[1].Type = %q", got.Fields[1].Type)
	}

	all = r.All()
	if len(all) != 1 {
		t.Errorf("expected 1 in All(), got %d", len(all))
	}

	schema2 := ExtractionSchema{
		EntityType:  "test.v1.Person",
		DisplayName: "Updated Person",
	}
	r.Register(schema2)
	got, _ = r.Get("test.v1.Person")
	if got.DisplayName != "Updated Person" {
		t.Errorf("after replace: DisplayName = %q", got.DisplayName)
	}

	r.Register(ExtractionSchema{EntityType: "test.v1.Company"})
	all = r.All()
	if len(all) != 2 {
		t.Errorf("expected 2 in All(), got %d", len(all))
	}
}

func TestExtractionFieldTypeConstants(t *testing.T) {
	tests := []struct {
		got  ExtractionFieldType
		want string
	}{
		{ExtractionFieldTypeString, "string"},
		{ExtractionFieldTypeNumber, "number"},
		{ExtractionFieldTypeBoolean, "boolean"},
		{ExtractionFieldTypeDate, "date"},
		{ExtractionFieldTypeArray, "array"},
	}
	for _, tc := range tests {
		if string(tc.got) != tc.want {
			t.Errorf("got %q, want %q", tc.got, tc.want)
		}
	}
}

func TestExtractionField_AllFields(t *testing.T) {
	f := ExtractionField{
		Name:        "salary",
		Description: "Annual salary",
		Hint:        "Convert to annual if stated as monthly",
		Type:        ExtractionFieldTypeNumber,
		Required:    true,
		Repeated:    false,
		Examples:    []string{"50000", "120000"},
	}

	if f.Name != "salary" {
		t.Errorf("Name = %q", f.Name)
	}
	if f.Hint != "Convert to annual if stated as monthly" {
		t.Errorf("Hint = %q", f.Hint)
	}
	if !f.Required {
		t.Error("expected Required to be true")
	}
	if f.Repeated {
		t.Error("expected Repeated to be false")
	}
	if len(f.Examples) != 2 {
		t.Errorf("expected 2 examples, got %d", len(f.Examples))
	}
}

func TestExtractionSchema_AllFields(t *testing.T) {
	s := ExtractionSchema{
		EntityType:   "test.v1.Job",
		DisplayName:  "Job Posting",
		Prompt:       "Extract job posting fields.",
		Instructions: "If salary is a range, extract the midpoint.",
		Fields: []ExtractionField{
			{Name: "title", Type: ExtractionFieldTypeString},
		},
	}

	if s.EntityType != "test.v1.Job" {
		t.Errorf("EntityType = %q", s.EntityType)
	}
	if s.Instructions != "If salary is a range, extract the midpoint." {
		t.Errorf("Instructions = %q", s.Instructions)
	}
}
