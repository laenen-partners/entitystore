package matching

import (
	"math"
	"testing"
)

func approxEqual(a, b, epsilon float64) bool {
	return math.Abs(a-b) < epsilon
}

// ---------------------------------------------------------------------------
// Jaro
// ---------------------------------------------------------------------------

func TestJaro(t *testing.T) {
	tests := []struct {
		a, b string
		want float64
	}{
		{"", "", 1.0},
		{"abc", "", 0.0},
		{"", "abc", 0.0},
		{"abc", "abc", 1.0},
		{"martha", "marhta", 0.944},
		{"dwayne", "duane", 0.822},
		{"dixon", "dicksonx", 0.767},
	}
	for _, tc := range tests {
		got := jaro(tc.a, tc.b)
		if !approxEqual(got, tc.want, 0.01) {
			t.Errorf("jaro(%q, %q) = %.3f, want %.3f", tc.a, tc.b, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Jaro-Winkler
// ---------------------------------------------------------------------------

func TestJaroWinkler(t *testing.T) {
	tests := []struct {
		a, b string
		want float64
	}{
		{"", "", 1.0},
		{"abc", "", 0.0},
		{"abc", "abc", 1.0},
		{"martha", "marhta", 0.961},  // prefix "mar" boosts jaro
		{"dwayne", "duane", 0.840},   // prefix "d" gives small boost
		{"jon", "john", 0.933},       // common name typo
		{"alice", "alice", 1.0},
		{"Alice", "alice", 0.867},    // case-sensitive — only first char differs
	}
	for _, tc := range tests {
		got := jaroWinkler(tc.a, tc.b)
		if !approxEqual(got, tc.want, 0.01) {
			t.Errorf("jaroWinkler(%q, %q) = %.3f, want %.3f", tc.a, tc.b, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Levenshtein
// ---------------------------------------------------------------------------

func TestLevenshteinDistance(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "", 3},
		{"", "abc", 3},
		{"abc", "abc", 0},
		{"kitten", "sitting", 3},
		{"saturday", "sunday", 3},
		{"abc", "axc", 1},
	}
	for _, tc := range tests {
		got := levenshteinDistance(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("levenshteinDistance(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestNormalizedLevenshtein(t *testing.T) {
	tests := []struct {
		a, b string
		want float64
	}{
		{"", "", 1.0},
		{"abc", "abc", 1.0},
		{"abc", "axc", 0.667},       // 1 edit / 3 chars
		{"kitten", "sitting", 0.571}, // 3 edits / 7 chars
		{"123 Main St", "123 Main Street", 0.733},
	}
	for _, tc := range tests {
		got := normalizedLevenshtein(tc.a, tc.b)
		if !approxEqual(got, tc.want, 0.01) {
			t.Errorf("normalizedLevenshtein(%q, %q) = %.3f, want %.3f", tc.a, tc.b, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Token Jaccard
// ---------------------------------------------------------------------------

func TestTokenJaccard(t *testing.T) {
	tests := []struct {
		a, b string
		want float64
	}{
		{"", "", 1.0},
		{"hello", "", 0.0},
		{"hello world", "hello world", 1.0},
		{"hello world", "world hello", 1.0},           // order doesn't matter
		{"senior software engineer", "software engineer", 0.667}, // 2/3
		{"alice bob", "bob carol", 0.333},              // 1/3
		{"a b c", "d e f", 0.0},                        // no overlap
		{"The Quick Brown Fox", "the quick brown fox", 1.0}, // case insensitive
	}
	for _, tc := range tests {
		got := tokenJaccard(tc.a, tc.b)
		if !approxEqual(got, tc.want, 0.01) {
			t.Errorf("tokenJaccard(%q, %q) = %.3f, want %.3f", tc.a, tc.b, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// computeSimilarity dispatch
// ---------------------------------------------------------------------------

func TestComputeSimilarity(t *testing.T) {
	// Exact.
	if got := computeSimilarity(SimilarityExact, "abc", "abc"); got != 1.0 {
		t.Errorf("exact match = %g", got)
	}
	if got := computeSimilarity(SimilarityExact, "abc", "abd"); got != 0.0 {
		t.Errorf("exact mismatch = %g", got)
	}

	// Empty strings.
	if got := computeSimilarity(SimilarityExact, "", "abc"); got != 0.0 {
		t.Errorf("empty a = %g", got)
	}
	if got := computeSimilarity(SimilarityJaroWinkler, "abc", ""); got != 0.0 {
		t.Errorf("empty b = %g", got)
	}

	// Jaro-Winkler dispatches.
	if got := computeSimilarity(SimilarityJaroWinkler, "martha", "marhta"); !approxEqual(got, 0.961, 0.01) {
		t.Errorf("jaro-winkler = %.3f", got)
	}

	// Levenshtein dispatches.
	if got := computeSimilarity(SimilarityLevenshtein, "abc", "axc"); !approxEqual(got, 0.667, 0.01) {
		t.Errorf("levenshtein = %.3f", got)
	}

	// Token Jaccard dispatches.
	if got := computeSimilarity(SimilarityTokenJaccard, "hello world", "world hello"); got != 1.0 {
		t.Errorf("token jaccard = %g", got)
	}

	// Unknown similarity function.
	if got := computeSimilarity("unknown", "a", "b"); got != 0.0 {
		t.Errorf("unknown = %g", got)
	}
}
