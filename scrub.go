package rewindrewind

import (
	"regexp"
	"strings"
)

// filteredPlaceholder replaces any value whose key matched the sensitive-data
// denylist before a payload is marshaled and sent.
const filteredPlaceholder = "[FILTERED]"

// sensitiveKeyPattern matches map keys that commonly carry secrets. It is
// matched case-insensitively against the whole key. The list is intentionally
// conservative (exact-ish tokens) to avoid over-redacting benign keys.
var sensitiveKeyPattern = regexp.MustCompile(`(?i)^(password|passwd|secret|token|authorization|auth|api[-_]?key|access[-_]?key|client[-_]?secret|cookie|session|credential|card|cvv|ssn|private[-_]?key)$`)

// isSensitiveKey reports whether key matches the default denylist.
func isSensitiveKey(key string) bool {
	return sensitiveKeyPattern.MatchString(strings.TrimSpace(key))
}

// scrubStringMap returns a copy of m with values under sensitive keys redacted.
// It is used for the option-provided tags bag (map[string]string). nil/empty in
// yields the same map unchanged (no allocation when there is nothing to do).
func scrubStringMap(m map[string]string) map[string]string {
	if len(m) == 0 {
		return m
	}
	// Only allocate a copy once we know a redaction is needed.
	var out map[string]string
	for k := range m {
		if isSensitiveKey(k) {
			if out == nil {
				out = make(map[string]string, len(m))
				for kk, vv := range m {
					out[kk] = vv
				}
			}
			out[k] = filteredPlaceholder
		}
	}
	if out == nil {
		return m
	}
	return out
}

// scrubAnyMap recursively redacts sensitive values in a map[string]any bag
// (extra, request). Nested maps and slices are walked. A redaction-free input
// is returned unchanged.
func scrubAnyMap(m map[string]any) map[string]any {
	if len(m) == 0 {
		return m
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		if isSensitiveKey(k) {
			out[k] = filteredPlaceholder
			continue
		}
		out[k] = scrubValue(v)
	}
	return out
}

// scrubValue recurses into nested containers, redacting sensitive keys at any
// depth. Scalars are returned untouched.
func scrubValue(v any) any {
	switch val := v.(type) {
	case map[string]any:
		return scrubAnyMap(val)
	case map[string]string:
		return scrubStringMap(val)
	case []any:
		out := make([]any, len(val))
		for i, e := range val {
			out[i] = scrubValue(e)
		}
		return out
	default:
		return v
	}
}
