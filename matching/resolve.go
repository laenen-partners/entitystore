package matching

import (
	"encoding/json"
	"strings"
	"unicode"
)

// BuildAnchors extracts anchor field values from entity data using the
// EntityMatchConfig and returns normalized AnchorQuery values ready for
// store lookups or upserts.
func BuildAnchors(data json.RawMessage, config EntityMatchConfig) []AnchorQuery {
	fields := extractFields(data)
	var anchors []AnchorQuery

	// Single anchors.
	for _, af := range config.Anchors.SingleAnchors {
		raw, ok := fieldValue(fields, af.ProtoFieldName)
		if !ok || raw == "" {
			continue
		}
		v := raw
		if af.Normalizer != nil {
			v = af.Normalizer(v)
		}
		if v == "" {
			continue
		}
		anchors = append(anchors, AnchorQuery{
			Field: af.ProtoFieldName,
			Value: v,
		})
	}

	// Composite anchors — concatenate normalized field values with "|".
	for _, composite := range config.Anchors.CompositeAnchors {
		var fieldNames []string
		var values []string
		allPresent := true
		for _, af := range composite {
			raw, ok := fieldValue(fields, af.ProtoFieldName)
			if !ok || raw == "" {
				allPresent = false
				break
			}
			v := raw
			if af.Normalizer != nil {
				v = af.Normalizer(v)
			}
			fieldNames = append(fieldNames, af.ProtoFieldName)
			values = append(values, v)
		}
		if allPresent && len(values) > 0 {
			anchors = append(anchors, AnchorQuery{
				Field: strings.Join(fieldNames, "|"),
				Value: strings.Join(values, "|"),
			})
		}
	}

	return anchors
}

// BuildTokens extracts and tokenizes field values for token-based blocking.
func BuildTokens(data json.RawMessage, config EntityMatchConfig) map[string][]string {
	if len(config.TokenFields) == 0 {
		return nil
	}
	fields := extractFields(data)
	result := make(map[string][]string)
	for _, tf := range config.TokenFields {
		raw, ok := fieldValue(fields, tf)
		if !ok || raw == "" {
			continue
		}
		toks := Tokenize(raw)
		if len(toks) > 0 {
			result[tf] = toks
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// Tokenize splits a string into lowercase tokens, stripping punctuation.
func Tokenize(s string) []string {
	s = strings.ToLower(s)
	s = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return ' '
	}, s)
	parts := strings.Fields(s)
	seen := make(map[string]struct{}, len(parts))
	var out []string
	for _, p := range parts {
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}

// NormalizeField applies the normalizer for the given field, if one exists.
func NormalizeField(value string, field string, config EntityMatchConfig) string {
	if config.Normalizers == nil {
		return value
	}
	fn, ok := config.Normalizers[field]
	if !ok || fn == nil {
		return value
	}
	return fn(value)
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

func extractFields(data json.RawMessage) map[string]string {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil
	}
	result := make(map[string]string, len(obj))
	for k, v := range obj {
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			s = strings.TrimSpace(string(v))
			s = strings.Trim(s, `"`)
		}
		result[k] = s
	}
	return result
}

func fieldValue(fields map[string]string, protoName string) (string, bool) {
	if v, ok := fields[protoName]; ok {
		return v, true
	}
	if v, ok := fields[snakeToCamel(protoName)]; ok {
		return v, true
	}
	return "", false
}

func snakeToCamel(s string) string {
	parts := strings.Split(s, "_")
	for i := 1; i < len(parts); i++ {
		if len(parts[i]) > 0 {
			runes := []rune(parts[i])
			runes[0] = unicode.ToUpper(runes[0])
			parts[i] = string(runes)
		}
	}
	return strings.Join(parts, "")
}
