package surf

import (
	"math/rand"
	"strings"
	"testing"
)

// referenceSearch is the recursive tree walk that searchNodeKV replaced. It
// is kept here as an oracle: the iterative walk must agree with it on both
// the matched route and the captured parameters, for every generated tree and
// path below.
func referenceSearch(node *radixNode, path string, params *[]paramKV) *route {
	if len(path) == 0 {
		return node.handler
	}

	for _, child := range node.staticChildren {
		if strings.HasPrefix(path, child.path) {
			if r := referenceSearch(child, path[len(child.path):], params); r != nil {
				return r
			}
		}
	}

	if pc := node.paramChild; pc != nil {
		end := strings.IndexByte(path, '/')
		var value, remaining string
		if end == -1 {
			value, remaining = path, ""
		} else {
			value, remaining = path[:end], path[end:]
		}
		if value != "" {
			mark := len(*params)
			*params = append(*params, paramKV{key: pc.paramKey, val: value})
			if r := referenceSearch(pc, remaining, params); r != nil {
				return r
			}
			*params = (*params)[:mark]
		}
	}

	if wc := node.wildcardChild; wc != nil {
		*params = append(*params, paramKV{key: "*", val: path})
		return wc.handler
	}

	return nil
}

// randomPatterns builds a set of route patterns from a small alphabet of
// segments, so generated trees share prefixes densely and exercise node
// splitting, param backtracking, and wildcards.
func randomPatterns(rng *rand.Rand, n int) []string {
	segments := []string{"a", "ab", "abc", "b", "users", "user", "u"}
	seen := make(map[string]bool, n)
	patterns := make([]string, 0, n)

	for len(patterns) < n {
		var sb strings.Builder
		depth := 1 + rng.Intn(4)
		for i := 0; i < depth; i++ {
			sb.WriteByte('/')
			switch rng.Intn(10) {
			case 0, 1:
				sb.WriteByte(':')
				sb.WriteString(segments[rng.Intn(len(segments))])
			case 2:
				sb.WriteByte('*')
				i = depth // wildcard is terminal
			default:
				sb.WriteString(segments[rng.Intn(len(segments))])
			}
		}
		p := sb.String()
		if seen[p] {
			continue
		}
		seen[p] = true
		patterns = append(patterns, p)
	}
	return patterns
}

// randomPaths returns request paths: every pattern with its params filled in
// (so most lookups hit), plus arbitrary paths (so misses and backtracking are
// covered too).
func randomPaths(rng *rand.Rand, patterns []string) []string {
	values := []string{"1", "42", "ab", "abc", "b", "users", "x"}
	paths := make([]string, 0, len(patterns)*2)

	for _, p := range patterns {
		var sb strings.Builder
		for _, seg := range strings.Split(strings.TrimPrefix(p, "/"), "/") {
			sb.WriteByte('/')
			switch {
			case strings.HasPrefix(seg, ":"), strings.HasPrefix(seg, "*"):
				sb.WriteString(values[rng.Intn(len(values))])
			default:
				sb.WriteString(seg)
			}
		}
		paths = append(paths, sb.String())
	}

	for i := 0; i < len(patterns); i++ {
		var sb strings.Builder
		for d := 1 + rng.Intn(4); d > 0; d-- {
			sb.WriteByte('/')
			sb.WriteString(values[rng.Intn(len(values))])
		}
		paths = append(paths, sb.String())
	}
	return paths
}

// TestRadixSearchMatchesReference is the property test guarding the switch
// from a recursive walk to an iterative one with an explicit backtracking
// stack: over many randomly generated trees, both must resolve every path
// identically.
func TestRadixSearchMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(1))

	for iter := 0; iter < 300; iter++ {
		patterns := randomPatterns(rng, 1+rng.Intn(24))

		tree := newRadixTree()
		for _, p := range patterns {
			tree.insert(p, &route{pattern: p})
		}

		for _, path := range randomPaths(rng, patterns) {
			var gotParams, wantParams []paramKV
			got := tree.searchKV(path, &gotParams)
			want := referenceSearch(tree.root, path, &wantParams)

			if got != want {
				gotPattern, wantPattern := "<nil>", "<nil>"
				if got != nil {
					gotPattern = got.pattern
				}
				if want != nil {
					wantPattern = want.pattern
				}
				t.Fatalf("path %q in tree %v: matched %s, want %s",
					path, patterns, gotPattern, wantPattern)
			}
			if got == nil {
				continue
			}
			if len(gotParams) != len(wantParams) {
				t.Fatalf("path %q in tree %v: params %v, want %v",
					path, patterns, gotParams, wantParams)
			}
			for i := range gotParams {
				if gotParams[i] != wantParams[i] {
					t.Fatalf("path %q in tree %v: params %v, want %v",
						path, patterns, gotParams, wantParams)
				}
			}
		}
	}
}

// TestRadixStaticSiblingsAreDisjoint asserts the invariant the iterative walk
// relies on: insert splits static children on their common prefix, so a
// node's static children never share a leading byte and at most one of them
// can prefix-match a given path. If this ever stops holding, the walk needs
// to backtrack over static children too.
func TestRadixStaticSiblingsAreDisjoint(t *testing.T) {
	rng := rand.New(rand.NewSource(2))

	for iter := 0; iter < 200; iter++ {
		tree := newRadixTree()
		for _, p := range randomPatterns(rng, 1+rng.Intn(24)) {
			tree.insert(p, &route{pattern: p})
		}

		var walk func(n *radixNode)
		walk = func(n *radixNode) {
			seen := make(map[byte]string, len(n.staticChildren))
			for _, child := range n.staticChildren {
				if child.path == "" {
					t.Fatalf("static child with empty path under %q", n.path)
				}
				if other, dup := seen[child.path[0]]; dup {
					t.Fatalf("static siblings %q and %q share a leading byte under %q",
						other, child.path, n.path)
				}
				seen[child.path[0]] = child.path
				walk(child)
			}
			if n.paramChild != nil {
				walk(n.paramChild)
			}
			if n.wildcardChild != nil {
				walk(n.wildcardChild)
			}
		}
		walk(tree.root)
	}
}
