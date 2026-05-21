package surf

import "strings"

// matchGlob reports whether s matches pattern. A pattern ending in "*" matches
// any string with the preceding prefix; any other pattern must match exactly.
func matchGlob(pattern, s string) bool {
	if pattern == "*" {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(s, strings.TrimSuffix(pattern, "*"))
	}
	return pattern == s
}

// matchAnyGlob reports whether s matches any of the given patterns.
func matchAnyGlob(s string, patterns []string) bool {
	for _, p := range patterns {
		if matchGlob(p, s) {
			return true
		}
	}
	return false
}
