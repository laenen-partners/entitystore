package matching

import (
	"testing"
)

// ---------------------------------------------------------------------------
// Normalizers
// ---------------------------------------------------------------------------

func TestNormalizeLowercaseTrim(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"  Hello World  ", "hello world"},
		{"UPPERCASE", "uppercase"},
		{"already lowercase", "already lowercase"},
		{"", ""},
		{"  ", ""},
		{"MiXeD CaSe  ", "mixed case"},
	}
	for _, tc := range tests {
		got := NormalizeLowercaseTrim(tc.input)
		if got != tc.want {
			t.Errorf("NormalizeLowercaseTrim(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestNormalizePhone(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"+1 (555) 867-5309", "+15558675309"},
		{"555.867.5309", "5558675309"},
		{"+44 20 7946 0958", "+442079460958"},
		{"", ""},
		{"  ", ""},
		{"(123) 456-7890", "1234567890"},
		{"+1-800-FLOWERS", "+1800"},
	}
	for _, tc := range tests {
		got := NormalizePhone(tc.input)
		if got != tc.want {
			t.Errorf("NormalizePhone(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// MatchThresholds
// ---------------------------------------------------------------------------

func TestDefaultMatchThresholds(t *testing.T) {
	mt := DefaultMatchThresholds()
	if mt.AutoMatch != 0.85 {
		t.Errorf("AutoMatch = %g, want 0.85", mt.AutoMatch)
	}
	if mt.ReviewZone != 0.60 {
		t.Errorf("ReviewZone = %g, want 0.60", mt.ReviewZone)
	}
}

// ---------------------------------------------------------------------------
// MatchConfigRegistry
// ---------------------------------------------------------------------------

func TestMatchConfigRegistry(t *testing.T) {
	r := NewMatchConfigRegistry()

	// Empty registry.
	_, ok := r.Get("nonexistent")
	if ok {
		t.Error("expected Get on empty registry to return false")
	}
	all := r.All()
	if len(all) != 0 {
		t.Errorf("expected empty All(), got %d", len(all))
	}

	// Register and retrieve.
	cfg := EntityMatchConfig{
		EntityType: "test.v1.Person",
		Thresholds: MatchThresholds{AutoMatch: 0.9, ReviewZone: 0.5},
		EmbedFields: []string{"name"},
	}
	r.Register(cfg)

	got, ok := r.Get("test.v1.Person")
	if !ok {
		t.Fatal("expected Get to return true after Register")
	}
	if got.EntityType != "test.v1.Person" {
		t.Errorf("EntityType = %q, want %q", got.EntityType, "test.v1.Person")
	}
	if got.Thresholds.AutoMatch != 0.9 {
		t.Errorf("AutoMatch = %g, want 0.9", got.Thresholds.AutoMatch)
	}

	// All returns copy.
	all = r.All()
	if len(all) != 1 {
		t.Errorf("expected All() to return 1, got %d", len(all))
	}

	// Replace registration.
	cfg2 := EntityMatchConfig{
		EntityType: "test.v1.Person",
		Thresholds: MatchThresholds{AutoMatch: 0.8, ReviewZone: 0.4},
	}
	r.Register(cfg2)
	got, _ = r.Get("test.v1.Person")
	if got.Thresholds.AutoMatch != 0.8 {
		t.Errorf("after replace: AutoMatch = %g, want 0.8", got.Thresholds.AutoMatch)
	}

	// Multiple registrations.
	r.Register(EntityMatchConfig{EntityType: "test.v1.Company"})
	all = r.All()
	if len(all) != 2 {
		t.Errorf("expected All() to return 2, got %d", len(all))
	}
}

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

func TestSimilarityFuncConstants(t *testing.T) {
	if SimilarityExact != "exact" {
		t.Errorf("SimilarityExact = %q", SimilarityExact)
	}
	if SimilarityJaroWinkler != "jaro_winkler" {
		t.Errorf("SimilarityJaroWinkler = %q", SimilarityJaroWinkler)
	}
	if SimilarityLevenshtein != "levenshtein" {
		t.Errorf("SimilarityLevenshtein = %q", SimilarityLevenshtein)
	}
	if SimilarityTokenJaccard != "token_jaccard" {
		t.Errorf("SimilarityTokenJaccard = %q", SimilarityTokenJaccard)
	}
}

func TestConflictStrategyConstants(t *testing.T) {
	if ConflictFlagForReview != "flag_for_review" {
		t.Errorf("ConflictFlagForReview = %q", ConflictFlagForReview)
	}
	if ConflictLatestWins != "latest_wins" {
		t.Errorf("ConflictLatestWins = %q", ConflictLatestWins)
	}
	if ConflictHighestConf != "highest_confidence" {
		t.Errorf("ConflictHighestConf = %q", ConflictHighestConf)
	}
}

func TestMatchActionConstants(t *testing.T) {
	if ActionCreate != "create" {
		t.Errorf("ActionCreate = %q", ActionCreate)
	}
	if ActionUpdate != "update" {
		t.Errorf("ActionUpdate = %q", ActionUpdate)
	}
	if ActionPartialUpdate != "partial_update" {
		t.Errorf("ActionPartialUpdate = %q", ActionPartialUpdate)
	}
	if ActionConflict != "conflict" {
		t.Errorf("ActionConflict = %q", ActionConflict)
	}
	if ActionReview != "review" {
		t.Errorf("ActionReview = %q", ActionReview)
	}
}

func TestMergeOpConstants(t *testing.T) {
	if MergeKeep != "keep" {
		t.Errorf("MergeKeep = %q", MergeKeep)
	}
	if MergeWrite != "write" {
		t.Errorf("MergeWrite = %q", MergeWrite)
	}
	if MergeConflict != "conflict" {
		t.Errorf("MergeConflict = %q", MergeConflict)
	}
}
