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

// Stages of the per-node match order: static children (most specific), then
// the param child, then the wildcard child.
const (
	stageArrive = iota // just landed on a node; test it for a terminal match
	stageStatic
	stageParam
	stageWildcard
	stageBacktrack
)

// radixFrame is a backtracking point: a node that still has an untried param
// or wildcard alternative, the path that remained at that node, and the
// parameter count to restore before the alternative is tried.
type radixFrame struct {
	node  *radixNode
	path  string
	mark  int
	param bool // true if the param child is the next alternative, else wildcard
}

// inlineFrames is the number of backtracking frames kept inline on the stack.
// A frame is only pushed at a node that has a param or wildcard child, so
// realistic route trees never come close; deeper ones spill to the heap and
// still match correctly.
const inlineFrames = 8

// staticChildFor returns the one static child whose path prefixes the given
// path, or nil. At most one can match: insert splits static children on their
// longest common prefix, so siblings never share a leading byte — which is
// why the walk below only ever backtracks over param and wildcard children.
func (n *radixNode) staticChildFor(path string) *radixNode {
	first := path[0]
	for _, child := range n.staticChildren {
		if child.path[0] == first && strings.HasPrefix(path, child.path) {
			return child
		}
	}
	return nil
}

// searchNodeKV walks the tree from node iteratively, with an explicit
// backtracking stack in place of recursion. Match order at each node is
// static child, then param child, then wildcard child.
func (t *radixTree) searchNodeKV(node *radixNode, path string, params *[]paramKV) *route {
	var buf [inlineFrames]radixFrame
	stack := buf[:0]

	entry := len(*params)
	rest, mark, stage := path, entry, stageArrive

	for {
		switch stage {
		case stageArrive:
			// A node reached with nothing left to match is a hit only if it
			// is itself terminal; its children are not consulted.
			if len(rest) == 0 {
				if node.handler != nil {
					return node.handler
				}
				stage = stageBacktrack
				continue
			}
			stage = stageStatic

		case stageStatic:
			stage = stageParam
			child := node.staticChildFor(rest)
			if child == nil {
				continue
			}
			if node.paramChild != nil || node.wildcardChild != nil {
				stack = append(stack, radixFrame{node: node, path: rest, mark: mark, param: true})
			}
			node, rest, stage = child, rest[len(child.path):], stageArrive

		case stageParam:
			stage = stageWildcard
			pc := node.paramChild
			if pc == nil {
				continue
			}
			// A param matches up to the next "/" or the end of the path.
			var value, remaining string
			if end := strings.IndexByte(rest, '/'); end == -1 {
				value = rest
			} else {
				value, remaining = rest[:end], rest[end:]
			}
			if value == "" {
				continue
			}
			if node.wildcardChild != nil {
				stack = append(stack, radixFrame{node: node, path: rest, mark: mark, param: false})
			}
			*params = append(*params, paramKV{key: pc.paramKey, val: value})
			node, rest, mark, stage = pc, remaining, len(*params), stageArrive

		case stageWildcard:
			stage = stageBacktrack
			if wc := node.wildcardChild; wc != nil && wc.handler != nil {
				*params = append(*params, paramKV{key: "*", val: rest})
				return wc.handler
			}

		case stageBacktrack:
			if len(stack) == 0 {
				*params = (*params)[:entry]
				return nil
			}
			f := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			*params = (*params)[:f.mark]
			node, rest, mark = f.node, f.path, f.mark
			if f.param {
				stage = stageParam
			} else {
				stage = stageWildcard
			}
		}
	}
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
