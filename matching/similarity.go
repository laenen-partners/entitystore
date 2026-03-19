package matching

import (
	"strings"
)

// computeSimilarity dispatches to the appropriate similarity function.
func computeSimilarity(fn SimilarityFunc, a, b string) float64 {
	if a == "" || b == "" {
		return 0.0
	}
	switch fn {
	case SimilarityExact:
		if a == b {
			return 1.0
		}
		return 0.0
	case SimilarityJaroWinkler:
		return jaroWinkler(a, b)
	case SimilarityLevenshtein:
		return normalizedLevenshtein(a, b)
	case SimilarityTokenJaccard:
		return tokenJaccard(a, b)
	default:
		return 0.0
	}
}

// ---------------------------------------------------------------------------
// Jaro-Winkler
// ---------------------------------------------------------------------------

// jaro computes the Jaro similarity between two strings (0.0–1.0).
func jaro(s1, s2 string) float64 {
	if s1 == s2 {
		return 1.0
	}

	r1 := []rune(s1)
	r2 := []rune(s2)
	l1 := len(r1)
	l2 := len(r2)

	if l1 == 0 || l2 == 0 {
		return 0.0
	}

	// Match window: max(len(s1), len(s2))/2 - 1, at least 0.
	matchWindow := 0
	if l1 > l2 {
		matchWindow = l1/2 - 1
	} else {
		matchWindow = l2/2 - 1
	}
	if matchWindow < 0 {
		matchWindow = 0
	}

	s1Matches := make([]bool, l1)
	s2Matches := make([]bool, l2)

	matches := 0
	transpositions := 0

	// Find matches.
	for i := 0; i < l1; i++ {
		start := i - matchWindow
		if start < 0 {
			start = 0
		}
		end := i + matchWindow + 1
		if end > l2 {
			end = l2
		}
		for j := start; j < end; j++ {
			if s2Matches[j] || r1[i] != r2[j] {
				continue
			}
			s1Matches[i] = true
			s2Matches[j] = true
			matches++
			break
		}
	}

	if matches == 0 {
		return 0.0
	}

	// Count transpositions.
	k := 0
	for i := 0; i < l1; i++ {
		if !s1Matches[i] {
			continue
		}
		for !s2Matches[k] {
			k++
		}
		if r1[i] != r2[k] {
			transpositions++
		}
		k++
	}

	m := float64(matches)
	return (m/float64(l1) + m/float64(l2) + (m-float64(transpositions)/2)/m) / 3.0
}

// jaroWinkler computes the Jaro-Winkler similarity (0.0–1.0).
// It gives a bonus for matching prefixes up to 4 characters.
func jaroWinkler(s1, s2 string) float64 {
	j := jaro(s1, s2)
	if j == 0 {
		return 0
	}

	r1 := []rune(s1)
	r2 := []rune(s2)

	// Common prefix up to 4 characters.
	prefixLen := 0
	maxPrefix := 4
	if len(r1) < maxPrefix {
		maxPrefix = len(r1)
	}
	if len(r2) < maxPrefix {
		maxPrefix = len(r2)
	}
	for i := 0; i < maxPrefix; i++ {
		if r1[i] != r2[i] {
			break
		}
		prefixLen++
	}

	// Winkler scaling factor (standard p = 0.1).
	return j + float64(prefixLen)*0.1*(1-j)
}

// ---------------------------------------------------------------------------
// Levenshtein
// ---------------------------------------------------------------------------

// levenshteinDistance computes the edit distance between two strings.
func levenshteinDistance(s1, s2 string) int {
	r1 := []rune(s1)
	r2 := []rune(s2)
	l1 := len(r1)
	l2 := len(r2)

	if l1 == 0 {
		return l2
	}
	if l2 == 0 {
		return l1
	}

	// Single-row DP.
	prev := make([]int, l2+1)
	curr := make([]int, l2+1)
	for j := 0; j <= l2; j++ {
		prev[j] = j
	}

	for i := 1; i <= l1; i++ {
		curr[0] = i
		for j := 1; j <= l2; j++ {
			cost := 1
			if r1[i-1] == r2[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost

			m := del
			if ins < m {
				m = ins
			}
			if sub < m {
				m = sub
			}
			curr[j] = m
		}
		prev, curr = curr, prev
	}

	return prev[l2]
}

// normalizedLevenshtein returns a similarity score (0.0–1.0) based on
// Levenshtein distance normalized by the length of the longer string.
func normalizedLevenshtein(s1, s2 string) float64 {
	if s1 == s2 {
		return 1.0
	}
	maxLen := len([]rune(s1))
	if l2 := len([]rune(s2)); l2 > maxLen {
		maxLen = l2
	}
	if maxLen == 0 {
		return 1.0
	}
	dist := levenshteinDistance(s1, s2)
	return 1.0 - float64(dist)/float64(maxLen)
}

// ---------------------------------------------------------------------------
// Token Jaccard
// ---------------------------------------------------------------------------

// tokenJaccard computes the Jaccard similarity over token sets of two strings.
// Tokens are lowercased words. Returns |intersection| / |union|.
func tokenJaccard(s1, s2 string) float64 {
	tokens1 := tokenSet(s1)
	tokens2 := tokenSet(s2)

	if len(tokens1) == 0 && len(tokens2) == 0 {
		return 1.0
	}
	if len(tokens1) == 0 || len(tokens2) == 0 {
		return 0.0
	}

	intersection := 0
	for t := range tokens1 {
		if tokens2[t] {
			intersection++
		}
	}

	union := len(tokens1)
	for t := range tokens2 {
		if !tokens1[t] {
			union++
		}
	}

	if union == 0 {
		return 0.0
	}
	return float64(intersection) / float64(union)
}

// tokenSet splits a string into a set of lowercase tokens.
func tokenSet(s string) map[string]bool {
	words := strings.Fields(strings.ToLower(s))
	set := make(map[string]bool, len(words))
	for _, w := range words {
		set[w] = true
	}
	return set
}
