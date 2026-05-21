package surf

import (
	"errors"
	"strings"
)

// errExample is a generic error used across tests to verify that internal
// error detail is never leaked to clients.
var errExample = errors.New("example internal failure")

// contains is a short alias for strings.Contains used in assertions.
func contains(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}
