package surf

import "strings"

// radixNode is a single node in surf's routing tree. Children are split into
// three fixed slots by kind so search walks each kind directly instead of
// iterating one mixed slice with per-element type filtering.
//
// A node has at most one paramChild and one wildcardChild — insert dedupes
// them. There can be any number of staticChildren.
type radixNode struct {
	path           string       // path segment this node owns; for a param node always ":"
	handler        *route       // nil unless this node is a terminal
	paramKey       string       // parameter name; "*" for wildcard
	staticChildren []*radixNode // ordered by insertion
	paramChild     *radixNode   // at most one
	wildcardChild  *radixNode   // at most one
}

// radixTree is the routing tree for a single HTTP method.
type radixTree struct {
	root *radixNode
}

func newRadixTree() *radixTree {
	return &radixTree{root: &radixNode{}}
}

// insert adds a route to the tree, splitting existing static children when
// needed.
func (t *radixTree) insert(pattern string, handler *route) {
	if pattern == "" {
		pattern = "/"
	}
	current := t.root
	remaining := pattern

	for len(remaining) > 0 {
		// Parameter segment ":name".
		if remaining[0] == ':' {
			end := strings.IndexByte(remaining[1:], '/')
			var name string
			if end == -1 {
				name = remaining[1:]
				remaining = ""
			} else {
				name = remaining[1 : end+1]
				remaining = remaining[end+1:]
			}
			if current.paramChild == nil {
				current.paramChild = &radixNode{
					path:     ":",
					paramKey: name,
				}
			}
			current = current.paramChild
			continue
		}

		// Wildcard "*" — terminal; matches the entire remaining path.
		if remaining[0] == '*' {
			current.wildcardChild = &radixNode{
				path:     "*",
				paramKey: "*",
				handler:  handler,
			}
			return
		}

		// Static segment — find a longest-common-prefix match in
		// staticChildren, splitting the existing child if necessary.
		matched := false
		for _, child := range current.staticChildren {
			commonLen := longestCommonPrefix(remaining, child.path)
			if commonLen == 0 {
				continue
			}
			matched = true
			if commonLen == len(child.path) {
				remaining = remaining[commonLen:]
				current = child
				break
			}
			// Split: insert a new common-prefix node between current and child.
			split := &radixNode{path: child.path[:commonLen]}
			child.path = child.path[commonLen:]
			split.staticChildren = append(split.staticChildren, child)
			for i, c := range current.staticChildren {
				if c == child {
					current.staticChildren[i] = split
					break
				}
			}
			remaining = remaining[commonLen:]
			current = split
			break
		}

		if !matched {
			end := len(remaining)
			for i := 0; i < len(remaining); i++ {
				if remaining[i] == ':' || remaining[i] == '*' {
					end = i
					break
				}
			}
			n := &radixNode{path: remaining[:end]}
			current.staticChildren = append(current.staticChildren, n)
			remaining = remaining[end:]
			current = n
		}
	}

	current.handler = handler
}

// searchKV looks up the route for path and appends matched parameters to
// *params. *params is truncated to zero length on entry, so reusing the
// caller's pooled slice across requests is allocation-free as long as the
// slice's backing array has enough room.
func (t *radixTree) searchKV(path string, params *[]paramKV) *route {
	if path == "" {
		path = "/"
	}
	*params = (*params)[:0]
	return t.searchNodeKV(t.root, path, params)
}

// searchNodeKV walks the tree from node. It tries static children first
// (most specific), then the param child, then the wildcard child.
func (t *radixTree) searchNodeKV(node *radixNode, path string, params *[]paramKV) *route {
	if len(path) == 0 {
		return node.handler
	}

	// Static children — direct slice walk with no type filter.
	for _, child := range node.staticChildren {
		if strings.HasPrefix(path, child.path) {
			if r := t.searchNodeKV(child, path[len(child.path):], params); r != nil {
				return r
			}
		}
	}

	// Param child — at most one. Match up to the next "/" or end.
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
			if r := t.searchNodeKV(pc, remaining, params); r != nil {
				return r
			}
			*params = (*params)[:mark]
		}
	}

	// Wildcard child — at most one. Captures the entire remaining path.
	if wc := node.wildcardChild; wc != nil {
		*params = append(*params, paramKV{key: "*", val: path})
		return wc.handler
	}

	return nil
}

// search returns the matched route plus parameters as a map. Used by tests
// and getAllowedMethods (cold paths). Implemented in terms of searchKV.
func (t *radixTree) search(path string) (*route, map[string]string) {
	var params []paramKV
	rt := t.searchKV(path, &params)
	if rt == nil {
		return nil, nil
	}
	m := make(map[string]string, len(params))
	for _, p := range params {
		m[p.key] = p.val
	}
	return rt, m
}

// longestCommonPrefix returns the length of the longest shared prefix of a
// and b. Path metacharacters (":" and "*") are never considered part of a
// common prefix.
func longestCommonPrefix(a, b string) int {
	maxLen := len(a)
	if len(b) < maxLen {
		maxLen = len(b)
	}
	for i := 0; i < maxLen; i++ {
		if a[i] != b[i] || a[i] == ':' || a[i] == '*' {
			return i
		}
	}
	return maxLen
}
